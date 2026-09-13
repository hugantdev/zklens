package zk

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/go-zookeeper/zk"
)

func TestConnectInvalidConfig(t *testing.T) {
	_, err := Connect(context.Background(), Config{})
	if err == nil {
		t.Fatal("Connect() with no hosts = nil error, want error")
	}
}

func TestConnectAndWaitInvalidConfig(t *testing.T) {
	_, err := ConnectAndWait(context.Background(), Config{}, time.Second)
	if err == nil {
		t.Fatal("ConnectAndWait() with no hosts = nil error, want error")
	}
}

// closedPortHost returns a host:port very likely to have nothing listening
// on it: a TCP listener is opened then immediately closed, so dialing it
// should fail fast (connection refused) instead of hanging.
func closedPortHost(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}
	return addr
}

func TestConnectAndWaitTimesOutOnUnreachableEnsemble(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}

	start := time.Now()
	_, err := ConnectAndWait(context.Background(), cfg, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ConnectAndWait() against an unreachable host = nil error, want timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("ConnectAndWait() error = %q, want it to mention timing out", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("ConnectAndWait() took %v, want it to respect the requested timeout", elapsed)
	}
}

func TestConnectAndWaitRespectsCanceledContext(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := ConnectAndWait(ctx, cfg, 10*time.Second)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ConnectAndWait() with an already-canceled context = nil error, want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ConnectAndWait() did not return promptly after context cancellation; want it to observe ctx.Done() rather than waiting out the timeout")
	}
}

func TestConnectAndWaitZeroTimeoutFallsBackToDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the real DefaultConnectTimeout; skipped in -short")
	}

	cfg := Config{Hosts: []string{closedPortHost(t)}}

	start := time.Now()
	_, err := ConnectAndWait(context.Background(), cfg, 0)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ConnectAndWait() with timeout<=0 against an unreachable host = nil error, want error")
	}
	if elapsed < DefaultConnectTimeout {
		t.Fatalf("ConnectAndWait() with timeout<=0 returned after %v, want it to wait at least DefaultConnectTimeout (%v)", elapsed, DefaultConnectTimeout)
	}
	if elapsed > DefaultConnectTimeout+3*time.Second {
		t.Fatalf("ConnectAndWait() with timeout<=0 took %v, want it close to DefaultConnectTimeout (%v)", elapsed, DefaultConnectTimeout)
	}
}

// TestConnectAndWaitWithAuthUnreachableEnsembleRespectsTimeout is a
// regression test for a bug where Connect called conn.AddAuth synchronously
// right after dialing: against a single unreachable host, go-zookeeper/zk's
// connect loop discards any queued request (AddAuth included) with
// zk.ErrNoServer as soon as it completes one pass over the host list without
// connecting, which for a single host happens almost immediately. That made
// Connect (and therefore ConnectAndWait) fail near-instantly whenever
// cfg.Auth was set, regardless of the requested timeout. If the retry fix
// holds, this must instead keep retrying AddAuth until ctx's deadline, so
// elapsed time should track the requested timeout, not be near-zero.
func TestConnectAndWaitWithAuthUnreachableEnsembleRespectsTimeout(t *testing.T) {
	cfg := Config{
		Hosts: []string{closedPortHost(t)},
		Auth:  &Auth{Scheme: AuthSchemeDigest, Credential: "user:pass"},
	}

	const timeout = 500 * time.Millisecond
	start := time.Now()
	_, err := ConnectAndWait(context.Background(), cfg, timeout)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("ConnectAndWait() with Auth against an unreachable host = nil error, want error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("ConnectAndWait() with Auth error = %q, want it to mention timing out", err)
	}
	if elapsed < timeout/2 {
		t.Fatalf("ConnectAndWait() with Auth against an unreachable host returned after only %v (requested timeout %v); it failed suspiciously fast instead of retrying AddAuth past zk.ErrNoServer", elapsed, timeout)
	}
	if elapsed > timeout+3*time.Second {
		t.Fatalf("ConnectAndWait() with Auth took %v, want it bounded close to the requested timeout (%v)", elapsed, timeout)
	}
}

// TestConnectWithAuthUnreachableEnsembleRespectsContextDeadline exercises
// Connect directly (not via ConnectAndWait) with a context deadline of its
// own, confirming the AddAuth retry loop is actually bounded by ctx and does
// not hang forever when the ensemble never becomes reachable.
func TestConnectWithAuthUnreachableEnsembleRespectsContextDeadline(t *testing.T) {
	cfg := Config{
		Hosts: []string{closedPortHost(t)},
		Auth:  &Auth{Scheme: AuthSchemeDigest, Credential: "user:pass"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := Connect(ctx, cfg)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Connect() with Auth against an unreachable host and a bounded ctx = nil error, want error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Connect() with a 300ms ctx deadline took %v, want it bounded by ctx instead of retrying indefinitely", elapsed)
	}
}

// TestAddAuthWithRetryPropagatesNonErrNoServerImmediately checks the other
// half of the retry fix: it must only swallow zk.ErrNoServer, not every
// error. Once the underlying connection is definitively closed, AddAuth
// fails with zk.ErrConnectionClosed, which is not transient, and
// addAuthWithRetry must return it right away instead of retrying until ctx
// expires.
func TestAddAuthWithRetryPropagatesNonErrNoServerImmediately(t *testing.T) {
	conn, _, err := zk.Connect([]string{closedPortHost(t)}, 5*time.Second)
	if err != nil {
		t.Fatalf("zk.Connect() setup error = %v", err)
	}
	conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	err = addAuthWithRetry(ctx, conn, &Auth{Scheme: AuthSchemeDigest, Credential: "user:pass"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("addAuthWithRetry() on a closed connection = nil error, want error")
	}
	if errors.Is(err, zk.ErrNoServer) {
		t.Fatalf("addAuthWithRetry() error = %v, should not be (or wrap) zk.ErrNoServer once the connection is definitively closed", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("addAuthWithRetry() on a closed connection took %v, want it to return immediately instead of retrying for up to the 10s ctx budget", elapsed)
	}
}
