package zk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-zookeeper/zk"
)

// Client wraps a connection to a ZooKeeper ensemble.
//
// The underlying go-zookeeper/zk connection reconnects automatically: it
// keeps cycling through Config.Hosts and re-establishing the session
// whenever the socket drops, for as long as the session itself has not
// expired. Callers that care about connectivity changes (e.g. to surface
// them in a status bar) should watch the Events channel rather than treat a
// dropped connection as fatal.
type Client struct {
	conn   *zk.Conn
	events <-chan zk.Event
}

// State re-exports the underlying ZooKeeper connection state type so
// callers do not need to import go-zookeeper/zk directly.
type State = zk.State

// Event re-exports the underlying ZooKeeper event type so callers do not
// need to import go-zookeeper/zk directly.
type Event = zk.Event

// Stat re-exports the underlying ZooKeeper znode metadata type (returned by
// Client.Get) so callers do not need to import go-zookeeper/zk directly.
type Stat = zk.Stat

// Connection state values a caller may observe on Client.Events, re-exported
// so callers do not need to import go-zookeeper/zk directly.
const (
	StateUnknown           = zk.StateUnknown
	StateDisconnected      = zk.StateDisconnected
	StateConnecting        = zk.StateConnecting
	StateConnected         = zk.StateConnected
	StateHasSession        = zk.StateHasSession
	StateConnectedReadOnly = zk.StateConnectedReadOnly
	StateSaslAuthenticated = zk.StateSaslAuthenticated
	StateExpired           = zk.StateExpired
	StateAuthFailed        = zk.StateAuthFailed
)

// addAuthRetryInterval is how long to wait between AddAuth attempts that
// fail with zk.ErrNoServer.
const addAuthRetryInterval = 100 * time.Millisecond

// discardLogger silences the underlying go-zookeeper/zk client's default
// logging (which otherwise writes connect/disconnect chatter straight to
// stderr via the stdlib log package, corrupting the terminal once the TUI's
// alt screen exits). Callers that want connection visibility should watch
// Client.Events instead.
type discardLogger struct{}

func (discardLogger) Printf(string, ...interface{}) {}

// Connect dials the ensemble described by cfg. The returned Client is
// usable immediately, but the session is established asynchronously; use
// ConnectAndWait to block until the session is ready, or watch Events
// yourself.
//
// If cfg.Auth is set, Connect also submits those credentials before
// returning, retrying past zk.ErrNoServer for as long as ctx allows: the
// underlying go-zookeeper/zk client queues AddAuth like any other request
// and discards whatever is still queued with that error every time it
// finishes a pass over Config.Hosts without having connected yet, which
// with a slow-starting ensemble (or a single host whose very first dial
// fails) can happen almost immediately. That doesn't mean auth was
// rejected — the background connection loop keeps retrying regardless — so
// simply resubmitting AddAuth converges once a session exists. Pass a
// context with a deadline (ConnectAndWait does this for you) so a fully
// unreachable ensemble doesn't retry forever.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	conn, events, err := zk.Connect(cfg.Hosts, cfg.sessionTimeoutOrDefault(), zk.WithLogger(discardLogger{}))
	if err != nil {
		return nil, fmt.Errorf("zk: connect to %v: %w", cfg.Hosts, err)
	}

	if cfg.Auth != nil {
		if err := addAuthWithRetry(ctx, conn, cfg.Auth); err != nil {
			conn.Close()
			return nil, fmt.Errorf("zk: add %s auth: %w", cfg.Auth.Scheme, err)
		}
	}

	return &Client{conn: conn, events: events}, nil
}

// addAuthWithRetry calls conn.AddAuth, retrying while it fails with
// zk.ErrNoServer (see Connect's doc comment for why that error is
// transient here) until it succeeds, ctx is done, or a different error
// occurs.
func addAuthWithRetry(ctx context.Context, conn *zk.Conn, auth *Auth) error {
	for {
		err := conn.AddAuth(string(auth.Scheme), []byte(auth.Credential))
		if err == nil {
			return nil
		}
		if !errors.Is(err, zk.ErrNoServer) {
			return err
		}
		select {
		case <-time.After(addAuthRetryInterval):
		case <-ctx.Done():
			return fmt.Errorf("%w (last error: %v)", ctx.Err(), err)
		}
	}
}

// ConnectAndWait dials the ensemble like Connect, then blocks until the
// initial session is established (zk.StateHasSession), the connection
// fails outright (zk.StateAuthFailed, zk.StateExpired), ctx is done, or
// timeout elapses. The same deadline bounds both the initial AddAuth retry
// (see Connect) and the wait for the session below, so timeout is the
// total time budget regardless of whether cfg.Auth is set.
func ConnectAndWait(ctx context.Context, cfg Config, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		timeout = DefaultConnectTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, err := Connect(ctx, cfg)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("zk: timed out waiting for session with %v: %w", cfg.Hosts, err)
		}
		return nil, err
	}

	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				c.Close()
				return nil, errors.New("zk: connection closed before session was established")
			}
			switch ev.State {
			case zk.StateHasSession:
				return c, nil
			case zk.StateAuthFailed:
				c.Close()
				return nil, fmt.Errorf("zk: authentication failed connecting to %v", cfg.Hosts)
			case zk.StateExpired:
				c.Close()
				return nil, fmt.Errorf("zk: session expired connecting to %v", cfg.Hosts)
			}
		case <-ctx.Done():
			c.Close()
			return nil, fmt.Errorf("zk: timed out waiting for session with %v: %w", cfg.Hosts, ctx.Err())
		}
	}
}

// Events streams connection/session state changes for as long as the
// client is open. It is closed after Close is called.
func (c *Client) Events() <-chan Event {
	return c.events
}

// State returns the current connection state.
func (c *Client) State() State {
	return c.conn.State()
}

// Close terminates the session and releases the underlying connection.
func (c *Client) Close() {
	c.conn.Close()
}
