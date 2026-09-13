// Package zk provides a ZooKeeper client used by zklens. It knows nothing
// about the TUI (bubbletea/bubbles/lipgloss) and only deals with connecting
// to an ensemble and talking to it.
package zk

import (
	"errors"
	"fmt"
	"time"
)

// DefaultSessionTimeout is used when Config.SessionTimeout is zero.
const DefaultSessionTimeout = 10 * time.Second

// DefaultConnectTimeout is how long ConnectAndWait waits for the initial
// session to be established before giving up, when the caller does not
// specify one.
const DefaultConnectTimeout = 10 * time.Second

// AuthScheme identifies a ZooKeeper authentication scheme understood by the
// server (see the ZooKeeper "digest" ACL provider).
type AuthScheme string

// AuthSchemeDigest is the only auth scheme supported by zklens's MVP.
const AuthSchemeDigest AuthScheme = "digest"

// Auth holds optional credentials added to a connection right after it is
// established.
type Auth struct {
	// Scheme is the ZooKeeper auth scheme, e.g. AuthSchemeDigest.
	Scheme AuthScheme
	// Credential is the scheme-specific credential, e.g. "user:password"
	// for digest auth.
	Credential string
}

// Config describes how to connect to a ZooKeeper ensemble.
type Config struct {
	// Hosts is the list of "host:port" ensemble members.
	Hosts []string
	// SessionTimeout is the ZooKeeper session timeout negotiated with the
	// server. Falls back to DefaultSessionTimeout when zero.
	SessionTimeout time.Duration
	// Auth is optional; when nil no authentication is added to the
	// connection.
	Auth *Auth
}

// Validate checks that cfg is usable, returning a descriptive error
// otherwise.
func (c Config) Validate() error {
	if len(c.Hosts) == 0 {
		return errors.New("zk: at least one host is required")
	}
	for _, h := range c.Hosts {
		if h == "" {
			return errors.New("zk: host entries must not be empty")
		}
	}
	if c.SessionTimeout < 0 {
		return fmt.Errorf("zk: session timeout must not be negative, got %s", c.SessionTimeout)
	}
	if c.Auth != nil {
		if c.Auth.Scheme == "" {
			return errors.New("zk: auth scheme must not be empty when auth is set")
		}
		if c.Auth.Credential == "" {
			return errors.New("zk: auth credential must not be empty when auth is set")
		}
	}
	return nil
}

// sessionTimeoutOrDefault returns c.SessionTimeout, falling back to
// DefaultSessionTimeout when unset.
func (c Config) sessionTimeoutOrDefault() time.Duration {
	if c.SessionTimeout == 0 {
		return DefaultSessionTimeout
	}
	return c.SessionTimeout
}
