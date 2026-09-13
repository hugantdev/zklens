//go:build integration

// Integration tests that exercise a real ZooKeeper server via Docker. They
// are excluded from the default `go test ./...` run (no embedded test
// ZooKeeper is available to this module: go-zookeeper/zk only exposes its
// own test-cluster helpers to its own _test.go files, not to importers) and
// must be requested explicitly:
//
//	go test -tags integration ./internal/zk/...
//
// They are skipped automatically when Docker is not available.
package zk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	upstream "github.com/go-zookeeper/zk"
)

const testImage = "zookeeper:3.9"

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available; skipping ZooKeeper integration test")
	}
}

func startZKContainer(t *testing.T) (hostPort string, stop func()) {
	t.Helper()
	requireDocker(t)

	name := fmt.Sprintf("zklens-test-%d", time.Now().UnixNano())

	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	runCmd := exec.CommandContext(runCtx, "docker", "run", "-d", "--rm",
		"-p", "127.0.0.1::2181", "--name", name, testImage)
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Skipf("could not start %s container (docker unavailable/offline?): %v: %s", testImage, err, out)
	}

	stop = func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer killCancel()
		_ = exec.CommandContext(killCtx, "docker", "rm", "-f", name).Run()
	}

	portCtx, portCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer portCancel()
	portOut, err := exec.CommandContext(portCtx, "docker", "port", name, "2181/tcp").Output()
	if err != nil {
		stop()
		t.Fatalf("docker port %s 2181/tcp: %v", name, err)
	}
	hostPort = strings.TrimSpace(strings.Split(strings.TrimSpace(string(portOut)), "\n")[0])
	if hostPort == "" {
		stop()
		t.Fatalf("docker port %s 2181/tcp returned no mapping", name)
	}

	return hostPort, stop
}

func dockerStop(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "stop", "-t", "2", name).CombinedOutput(); err != nil {
		t.Fatalf("docker stop %s: %v: %s", name, err, out)
	}
}

func dockerStart(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "start", name).CombinedOutput(); err != nil {
		t.Fatalf("docker start %s: %v: %s", name, err, out)
	}
}

// TestIntegrationConnectAndReconnect verifies, against a real ZooKeeper
// server, that: (1) ConnectAndWait establishes a session, including with
// digest auth configured, and (2) the connection survives a temporary
// ensemble outage: after the server is stopped the client observes a
// disconnect event, and after it is restarted the client observes a fresh
// StateHasSession event without the caller having to reconnect manually.
func TestIntegrationConnectAndReconnect(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	cfg := Config{
		Hosts:          []string{hostPort},
		SessionTimeout: 6 * time.Second,
		Auth:           &Auth{Scheme: AuthSchemeDigest, Credential: "zklens-tester:s3cret"},
	}

	// The container's JVM can take a while to accept connections after
	// `docker run` returns, so allow a generous timeout for the first
	// session.
	client, err := ConnectAndWait(context.Background(), cfg, 60*time.Second)
	if err != nil {
		t.Fatalf("initial ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if got := client.State(); got != upstream.StateHasSession {
		t.Fatalf("client.State() after connect = %v, want upstream.StateHasSession", got)
	}
}

// TestIntegrationSurvivesEnsembleRestart is split out from the connect test
// above so a failure to reconnect doesn't mask whether the initial connect
// worked. It re-derives its own container so the two tests stay independent.
func TestIntegrationSurvivesEnsembleRestart(t *testing.T) {
	requireDocker(t)

	containerName := fmt.Sprintf("zklens-test-restart-%d", time.Now().UnixNano())

	// Use a fixed host port rather than letting Docker assign a dynamic one
	// (-p 127.0.0.1::2181): Docker reassigns a *different* ephemeral port
	// each time a container is started, and this test needs the address to
	// stay the same across the stop/start cycle below.
	hostPort := reserveLocalPort(t)

	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	// Deliberately no --rm here: this test stops and restarts the same
	// container, and --rm auto-removes a container as soon as it stops.
	if out, err := exec.CommandContext(runCtx, "docker", "run", "-d",
		"-p", fmt.Sprintf("127.0.0.1:%d:2181", hostPort), "--name", containerName, testImage).CombinedOutput(); err != nil {
		t.Skipf("could not start %s container (docker unavailable/offline?): %v: %s", testImage, err, out)
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer killCancel()
		_ = exec.CommandContext(killCtx, "docker", "rm", "-f", containerName).Run()
	}()

	cfg := Config{Hosts: []string{fmt.Sprintf("127.0.0.1:%d", hostPort)}, SessionTimeout: 6 * time.Second}
	client, err := ConnectAndWait(context.Background(), cfg, 60*time.Second)
	if err != nil {
		t.Fatalf("initial ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	events := client.Events()

	dockerStop(t, containerName)

	if !waitForState(t, events, 20*time.Second, func(s State) bool {
		return s == upstream.StateDisconnected || s == upstream.StateConnecting
	}) {
		t.Fatal("did not observe a disconnect event after stopping the ensemble")
	}

	dockerStart(t, containerName)

	if !waitForState(t, events, 60*time.Second, func(s State) bool {
		return s == upstream.StateHasSession
	}) {
		t.Fatal("did not observe upstream.StateHasSession after restarting the ensemble; client did not reconnect")
	}
}

// reserveLocalPort returns a TCP port on 127.0.0.1 that is very likely free:
// a listener is opened then immediately closed so the port number can be
// handed to `docker run -p` for a fixed mapping.
func reserveLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForState(t *testing.T, events <-chan Event, timeout time.Duration, match func(State) bool) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return false
			}
			t.Logf("zk event: %+v", ev)
			if match(ev.State) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// TestIntegrationChildrenListsCreatedNodesSorted seeds real znodes via the
// underlying go-zookeeper/zk connection (internal/zk.Client exposes no
// Create yet; that's out of scope for this task) and verifies Client.Children
// actually lists them, sorted, against a real server rather than trusting
// the implementation's own claim.
func TestIntegrationChildrenListsCreatedNodesSorted(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.conn.Create("/demo", nil, 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/demo) failed: %v", err)
	}
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if _, err := client.conn.Create("/demo/"+name, nil, 0, upstream.WorldACL(upstream.PermAll)); err != nil {
			t.Fatalf("seed Create(/demo/%s) failed: %v", name, err)
		}
	}

	children, err := client.Children("/demo")
	if err != nil {
		t.Fatalf("Children(/demo) failed: %v", err)
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(children) != len(want) {
		t.Fatalf("Children(/demo) = %v, want %v", children, want)
	}
	for i := range want {
		if children[i] != want[i] {
			t.Fatalf("Children(/demo) = %v, want %v (sorted)", children, want)
		}
	}
}

// TestIntegrationChildrenNonexistentNode verifies the "znode inexistente"
// edge case against a real server: Children on a path that was never
// created must fail wrapping the real zk.ErrNoNode, not hang or panic.
func TestIntegrationChildrenNonexistentNode(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	_, err = client.Children("/this-was-never-created")
	if err == nil {
		t.Fatal("Children() on a nonexistent znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoNode) {
		t.Fatalf("Children() error = %v, want it to wrap zk.ErrNoNode", err)
	}
}

// TestIntegrationChildrenACLPermissionDenied verifies the "error de
// permisos (ACL)" edge case against a real server: a znode created with a
// digest ACL that grants no permission to "world:anyone" must reject an
// unauthenticated client's Children call with zk.ErrNoAuth, not silently
// return an empty list or hang.
func TestIntegrationChildrenACLPermissionDenied(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	ownerCfg := Config{
		Hosts:          []string{hostPort},
		SessionTimeout: 6 * time.Second,
		Auth:           &Auth{Scheme: AuthSchemeDigest, Credential: "owner:ownerpass"},
	}
	owner, err := ConnectAndWait(context.Background(), ownerCfg, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(owner) failed: %v", err)
	}
	defer owner.Close()

	if _, err := owner.conn.Create("/restricted", nil, 0, upstream.DigestACL(upstream.PermAll, "owner", "ownerpass")); err != nil {
		t.Fatalf("seed Create(/restricted) failed: %v", err)
	}

	outsider, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(outsider) failed: %v", err)
	}
	defer outsider.Close()

	_, err = outsider.Children("/restricted")
	if err == nil {
		t.Fatal("unauthenticated Children() on an ACL-restricted znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoAuth) {
		t.Fatalf("Children() error = %v, want it to wrap zk.ErrNoAuth", err)
	}
}

// TestIntegrationChildrenSurvivesDisconnectMidOperation verifies the
// "desconexión a mitad de operación" edge case: Children calls made while
// the ensemble is stopped must fail promptly (never hang), and once the
// ensemble is back up the same *Client must serve Children successfully
// again without the caller having reconnected manually.
func TestIntegrationChildrenSurvivesDisconnectMidOperation(t *testing.T) {
	requireDocker(t)

	containerName := fmt.Sprintf("zklens-test-midop-%d", time.Now().UnixNano())
	hostPort := reserveLocalPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)

	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	// No --rm: this test stops and restarts the same container.
	if out, err := exec.CommandContext(runCtx, "docker", "run", "-d",
		"-p", fmt.Sprintf("127.0.0.1:%d:2181", hostPort), "--name", containerName, testImage).CombinedOutput(); err != nil {
		t.Skipf("could not start %s container (docker unavailable/offline?): %v: %s", testImage, err, out)
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer killCancel()
		_ = exec.CommandContext(killCtx, "docker", "rm", "-f", containerName).Run()
	}()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{addr}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.conn.Create("/demo", nil, 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/demo) failed: %v", err)
	}

	var (
		mu               sync.Mutex
		sawErrorInOutage bool
		maxCallDuration  time.Duration
	)
	stopPolling := make(chan struct{})
	pollingDone := make(chan struct{})
	go func() {
		defer close(pollingDone)
		for {
			select {
			case <-stopPolling:
				return
			default:
			}
			start := time.Now()
			_, callErr := client.Children("/demo")
			d := time.Since(start)

			mu.Lock()
			if d > maxCallDuration {
				maxCallDuration = d
			}
			if callErr != nil {
				sawErrorInOutage = true
			}
			mu.Unlock()

			if d > 10*time.Second {
				t.Errorf("Children(/demo) took %v mid-test, want every call bounded well under the ensemble outage window", d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	time.Sleep(300 * time.Millisecond) // let a few successful calls happen first
	dockerStop(t, containerName)
	time.Sleep(1500 * time.Millisecond) // keep the ensemble down while the loop keeps hammering it
	dockerStart(t, containerName)

	reconnected := waitForState(t, client.Events(), 60*time.Second, func(s State) bool {
		return s == upstream.StateHasSession
	})

	time.Sleep(500 * time.Millisecond) // let the polling loop observe a post-reconnect success
	close(stopPolling)
	<-pollingDone

	if !reconnected {
		t.Fatal("did not observe StateHasSession after restarting the ensemble; client did not reconnect")
	}

	mu.Lock()
	sawErr := sawErrorInOutage
	maxDur := maxCallDuration
	mu.Unlock()

	if !sawErr {
		t.Fatal("no Children() call failed while the ensemble was stopped; the outage window may not have overlapped a call — increase the stop duration")
	}
	t.Logf("max single Children() call duration observed: %v", maxDur)

	if _, err := client.Children("/demo"); err != nil {
		t.Fatalf("Children(/demo) after the ensemble recovered = %v, want success", err)
	}
}

// TestIntegrationGetReturnsDataAndStat verifies Client.Get against a real
// server: the returned bytes must match what was written, and Stat must be
// internally consistent (DataLength matching the payload, Version starting
// at 0, a persistent node's EphemeralOwner at 0).
func TestIntegrationGetReturnsDataAndStat(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	payload := []byte(`{"hello":"world"}`)
	if _, err := client.conn.Create("/demo", payload, 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/demo) failed: %v", err)
	}

	data, stat, err := client.Get("/demo")
	if err != nil {
		t.Fatalf("Get(/demo) failed: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("Get(/demo) data = %q, want %q", data, payload)
	}
	if stat == nil {
		t.Fatal("Get(/demo) returned a nil Stat")
	}
	if int(stat.DataLength) != len(payload) {
		t.Fatalf("stat.DataLength = %d, want %d", stat.DataLength, len(payload))
	}
	if stat.Version != 0 {
		t.Fatalf("stat.Version = %d, want 0 for a freshly created znode", stat.Version)
	}
	if stat.NumChildren != 0 {
		t.Fatalf("stat.NumChildren = %d, want 0", stat.NumChildren)
	}
	if stat.EphemeralOwner != 0 {
		t.Fatalf("stat.EphemeralOwner = %d, want 0 for a persistent znode", stat.EphemeralOwner)
	}
	if stat.Czxid == 0 || stat.Mzxid == 0 || stat.Ctime == 0 || stat.Mtime == 0 {
		t.Fatalf("stat has zero-valued zxid/time fields, want them populated by the server: %+v", stat)
	}
}

// TestIntegrationGetEphemeralOwnerMatchesCreatingSession verifies the
// EphemeralOwner Stat field the detail view surfaces actually reflects a
// real session id, not a placeholder.
func TestIntegrationGetEphemeralOwnerMatchesCreatingSession(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.conn.Create("/ephemeral-demo", nil, upstream.FlagEphemeral, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/ephemeral-demo) failed: %v", err)
	}

	_, stat, err := client.Get("/ephemeral-demo")
	if err != nil {
		t.Fatalf("Get(/ephemeral-demo) failed: %v", err)
	}
	if stat.EphemeralOwner != client.conn.SessionID() {
		t.Fatalf("stat.EphemeralOwner = %d, want it to match the creating session id %d", stat.EphemeralOwner, client.conn.SessionID())
	}
}

// TestIntegrationGetNonexistentNode verifies the "znode inexistente" edge
// case against a real server: Get on a path that was never created must
// fail wrapping the real zk.ErrNoNode, not hang or panic.
func TestIntegrationGetNonexistentNode(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	_, _, err = client.Get("/this-was-never-created")
	if err == nil {
		t.Fatal("Get() on a nonexistent znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoNode) {
		t.Fatalf("Get() error = %v, want it to wrap zk.ErrNoNode", err)
	}
}

// TestIntegrationGetACLPermissionDenied verifies the "error de permisos
// (ACL)" edge case against a real server for Get, mirroring the Children
// ACL test: a znode created with a digest ACL granting no permission to
// world:anyone must reject an unauthenticated client's Get with
// zk.ErrNoAuth.
func TestIntegrationGetACLPermissionDenied(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	ownerCfg := Config{
		Hosts:          []string{hostPort},
		SessionTimeout: 6 * time.Second,
		Auth:           &Auth{Scheme: AuthSchemeDigest, Credential: "owner:ownerpass"},
	}
	owner, err := ConnectAndWait(context.Background(), ownerCfg, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(owner) failed: %v", err)
	}
	defer owner.Close()

	if _, err := owner.conn.Create("/restricted-data", []byte("secret"), 0, upstream.DigestACL(upstream.PermAll, "owner", "ownerpass")); err != nil {
		t.Fatalf("seed Create(/restricted-data) failed: %v", err)
	}

	outsider, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(outsider) failed: %v", err)
	}
	defer outsider.Close()

	_, _, err = outsider.Get("/restricted-data")
	if err == nil {
		t.Fatal("unauthenticated Get() on an ACL-restricted znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoAuth) {
		t.Fatalf("Get() error = %v, want it to wrap zk.ErrNoAuth", err)
	}
}

// TestIntegrationGetSurvivesDisconnectMidOperation mirrors
// TestIntegrationChildrenSurvivesDisconnectMidOperation for Get: calls made
// while the ensemble is stopped must fail promptly (never hang), and once
// it is back up the same *Client must serve Get successfully again without
// the caller reconnecting manually.
func TestIntegrationGetSurvivesDisconnectMidOperation(t *testing.T) {
	requireDocker(t)

	containerName := fmt.Sprintf("zklens-test-getmidop-%d", time.Now().UnixNano())
	hostPort := reserveLocalPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)

	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	if out, err := exec.CommandContext(runCtx, "docker", "run", "-d",
		"-p", fmt.Sprintf("127.0.0.1:%d:2181", hostPort), "--name", containerName, testImage).CombinedOutput(); err != nil {
		t.Skipf("could not start %s container (docker unavailable/offline?): %v: %s", testImage, err, out)
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer killCancel()
		_ = exec.CommandContext(killCtx, "docker", "rm", "-f", containerName).Run()
	}()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{addr}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.conn.Create("/demo", []byte("v1"), 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/demo) failed: %v", err)
	}

	var (
		mu               sync.Mutex
		sawErrorInOutage bool
	)
	stopPolling := make(chan struct{})
	pollingDone := make(chan struct{})
	go func() {
		defer close(pollingDone)
		for {
			select {
			case <-stopPolling:
				return
			default:
			}
			start := time.Now()
			_, _, callErr := client.Get("/demo")
			d := time.Since(start)

			mu.Lock()
			if callErr != nil {
				sawErrorInOutage = true
			}
			mu.Unlock()

			if d > 10*time.Second {
				t.Errorf("Get(/demo) took %v mid-test, want every call bounded well under the ensemble outage window", d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	time.Sleep(300 * time.Millisecond)
	dockerStop(t, containerName)
	time.Sleep(1500 * time.Millisecond)
	dockerStart(t, containerName)

	reconnected := waitForState(t, client.Events(), 60*time.Second, func(s State) bool {
		return s == upstream.StateHasSession
	})

	time.Sleep(500 * time.Millisecond)
	close(stopPolling)
	<-pollingDone

	if !reconnected {
		t.Fatal("did not observe StateHasSession after restarting the ensemble; client did not reconnect")
	}

	mu.Lock()
	sawErr := sawErrorInOutage
	mu.Unlock()
	if !sawErr {
		t.Fatal("no Get() call failed while the ensemble was stopped; the outage window may not have overlapped a call — increase the stop duration")
	}

	if _, _, err := client.Get("/demo"); err != nil {
		t.Fatalf("Get(/demo) after the ensemble recovered = %v, want success", err)
	}
}

// --- Create / Set / Delete (mutation) ---------------------------------------

// TestIntegrationCreateAllModesAndWorldReadable verifies, against a real
// server, that Create actually produces the four requested combinations
// (persistent/ephemeral x plain/sequential), that a sequential mode gets a
// server-assigned numeric suffix, that an ephemeral node's owner is the
// creating session, and that Create's fixed open ACL really means any other
// (unauthenticated) client can read what was created — not just a comment
// claiming so.
func TestIntegrationCreateAllModesAndWorldReadable(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.conn.Create("/mut", nil, 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/mut) failed: %v", err)
	}

	persistentPath, err := client.Create("/mut/persistent", []byte("p"), CreateModePersistent)
	if err != nil {
		t.Fatalf("Create(persistent) failed: %v", err)
	}
	if persistentPath != "/mut/persistent" {
		t.Fatalf("Create(persistent) actual path = %q, want it unchanged (no sequence suffix)", persistentPath)
	}

	ephemeralPath, err := client.Create("/mut/ephemeral", []byte("e"), CreateModeEphemeral)
	if err != nil {
		t.Fatalf("Create(ephemeral) failed: %v", err)
	}
	_, stat, err := client.Get(ephemeralPath)
	if err != nil {
		t.Fatalf("Get(%s) failed: %v", ephemeralPath, err)
	}
	if stat.EphemeralOwner != client.conn.SessionID() {
		t.Fatalf("ephemeral node's EphemeralOwner = %d, want the creating session id %d", stat.EphemeralOwner, client.conn.SessionID())
	}

	seqRe := regexp.MustCompile(`^/mut/seq\d+$`)
	seqPath, err := client.Create("/mut/seq", []byte("s"), CreateModePersistentSequential)
	if err != nil {
		t.Fatalf("Create(persistent sequential) failed: %v", err)
	}
	if !seqRe.MatchString(seqPath) {
		t.Fatalf("Create(persistent sequential) actual path = %q, want it to match %s", seqPath, seqRe)
	}
	if _, _, err := client.Get(seqPath); err != nil {
		t.Fatalf("Get(%s) failed: %v", seqPath, err)
	}

	ephSeqRe := regexp.MustCompile(`^/mut/ephseq\d+$`)
	ephSeqPath, err := client.Create("/mut/ephseq", []byte("es"), CreateModeEphemeralSequential)
	if err != nil {
		t.Fatalf("Create(ephemeral sequential) failed: %v", err)
	}
	if !ephSeqRe.MatchString(ephSeqPath) {
		t.Fatalf("Create(ephemeral sequential) actual path = %q, want it to match %s", ephSeqPath, ephSeqRe)
	}
	_, stat, err = client.Get(ephSeqPath)
	if err != nil {
		t.Fatalf("Get(%s) failed: %v", ephSeqPath, err)
	}
	if stat.EphemeralOwner != client.conn.SessionID() {
		t.Fatalf("ephemeral-sequential node's EphemeralOwner = %d, want the creating session id %d", stat.EphemeralOwner, client.conn.SessionID())
	}

	children, err := client.Children("/mut")
	if err != nil {
		t.Fatalf("Children(/mut) failed: %v", err)
	}
	if len(children) != 4 {
		t.Fatalf("Children(/mut) = %v, want 4 entries", children)
	}

	// The fixed open ACL means an entirely separate, unauthenticated client
	// must be able to read what Create made.
	outsider, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(outsider) failed: %v", err)
	}
	defer outsider.Close()
	data, _, err := outsider.Get("/mut/persistent")
	if err != nil {
		t.Fatalf("outsider Get(/mut/persistent) failed: %v, want Create's open ACL to allow this", err)
	}
	if string(data) != "p" {
		t.Fatalf("outsider Get(/mut/persistent) data = %q, want %q", data, "p")
	}
}

// TestIntegrationCreateNodeAlreadyExists verifies the "nodo ya existente"
// mutation error: creating the same path twice must wrap zk.ErrNodeExists.
func TestIntegrationCreateNodeAlreadyExists(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/dup", nil, CreateModePersistent); err != nil {
		t.Fatalf("first Create(/dup) failed: %v", err)
	}
	_, err = client.Create("/dup", nil, CreateModePersistent)
	if err == nil {
		t.Fatal("second Create(/dup) = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNodeExists) {
		t.Fatalf("second Create(/dup) error = %v, want it to wrap zk.ErrNodeExists", err)
	}
}

// TestIntegrationSetWithCorrectVersionSucceeds verifies the golden path:
// Set with the version just read via Get succeeds, bumps Version by one,
// and the new data is actually persisted.
func TestIntegrationSetWithCorrectVersionSucceeds(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/versioned", []byte("v0"), CreateModePersistent); err != nil {
		t.Fatalf("Create(/versioned) failed: %v", err)
	}
	_, stat, err := client.Get("/versioned")
	if err != nil {
		t.Fatalf("Get(/versioned) failed: %v", err)
	}

	newStat, err := client.Set("/versioned", []byte("v1"), stat.Version)
	if err != nil {
		t.Fatalf("Set(/versioned, correct version) failed: %v", err)
	}
	if newStat.Version != stat.Version+1 {
		t.Fatalf("Set() returned Version = %d, want %d", newStat.Version, stat.Version+1)
	}

	data, _, err := client.Get("/versioned")
	if err != nil {
		t.Fatalf("Get(/versioned) after Set failed: %v", err)
	}
	if string(data) != "v1" {
		t.Fatalf("Get(/versioned) after Set = %q, want %q", data, "v1")
	}
}

// TestIntegrationSetVersionGuardPreventsConcurrentOverwrite is the central
// correctness test for this task: it simulates the exact scenario the
// acceptance criteria call out — Set must not blindly overwrite a
// concurrent change using -1. A "concurrent writer" changes the znode after
// our client last read its Stat; our client's Set, still holding the now-
// stale version, must be rejected with zk.ErrBadVersion, and the concurrent
// writer's data must survive untouched.
func TestIntegrationSetVersionGuardPreventsConcurrentOverwrite(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/contested", []byte("v0"), CreateModePersistent); err != nil {
		t.Fatalf("Create(/contested) failed: %v", err)
	}

	// Our "editor" reads the current Stat, as the UI's edit flow does.
	_, staleStat, err := client.Get("/contested")
	if err != nil {
		t.Fatalf("Get(/contested) failed: %v", err)
	}

	// A concurrent writer changes the znode in the meantime.
	if _, err := client.Set("/contested", []byte("external-writer"), staleStat.Version); err != nil {
		t.Fatalf("simulated concurrent Set(/contested) failed: %v", err)
	}

	// Our editor's Set now uses the version it read before the concurrent
	// write — it must be rejected, not silently applied.
	_, err = client.Set("/contested", []byte("stale-editor-overwrite"), staleStat.Version)
	if err == nil {
		t.Fatal("Set() with a stale version = nil error, want zk.ErrBadVersion")
	}
	if !errors.Is(err, upstream.ErrBadVersion) {
		t.Fatalf("Set() with a stale version error = %v, want it to wrap zk.ErrBadVersion", err)
	}

	data, _, err := client.Get("/contested")
	if err != nil {
		t.Fatalf("Get(/contested) failed: %v", err)
	}
	if string(data) != "external-writer" {
		t.Fatalf("Get(/contested) after the rejected stale Set = %q, want %q (the concurrent writer's data must not be overwritten)", data, "external-writer")
	}
}

// TestIntegrationSetForceVersionNegativeOneBypassesGuard confirms the
// escape hatch documented on Set still works at the Client API level (the
// UI must never use it by default, but Set itself must still support an
// explicit, deliberate force).
func TestIntegrationSetForceVersionNegativeOneBypassesGuard(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/forced", []byte("v0"), CreateModePersistent); err != nil {
		t.Fatalf("Create(/forced) failed: %v", err)
	}
	// Bump the version behind Set's back.
	if _, err := client.Set("/forced", []byte("v1"), 0); err != nil {
		t.Fatalf("bump Set(/forced) failed: %v", err)
	}

	if _, err := client.Set("/forced", []byte("v2-forced"), -1); err != nil {
		t.Fatalf("Set(/forced, version=-1) failed: %v, want the force write to succeed regardless of the current version", err)
	}
	data, _, err := client.Get("/forced")
	if err != nil {
		t.Fatalf("Get(/forced) failed: %v", err)
	}
	if string(data) != "v2-forced" {
		t.Fatalf("Get(/forced) after a forced Set = %q, want %q", data, "v2-forced")
	}
}

// TestIntegrationSetNonexistentNode verifies the "znode inexistente"
// mutation error for Set.
func TestIntegrationSetNonexistentNode(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	_, err = client.Set("/never-created", []byte("x"), 0)
	if err == nil {
		t.Fatal("Set() on a nonexistent znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoNode) {
		t.Fatalf("Set() error = %v, want it to wrap zk.ErrNoNode", err)
	}
}

// TestIntegrationSetACLPermissionDenied verifies the "ACL" mutation error
// for Set: an unauthenticated client must not be able to overwrite a
// digest-ACL-restricted znode.
func TestIntegrationSetACLPermissionDenied(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	ownerCfg := Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second, Auth: &Auth{Scheme: AuthSchemeDigest, Credential: "owner:ownerpass"}}
	owner, err := ConnectAndWait(context.Background(), ownerCfg, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(owner) failed: %v", err)
	}
	defer owner.Close()

	if _, err := owner.conn.Create("/restricted-set", []byte("orig"), 0, upstream.DigestACL(upstream.PermAll, "owner", "ownerpass")); err != nil {
		t.Fatalf("seed Create(/restricted-set) failed: %v", err)
	}

	outsider, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(outsider) failed: %v", err)
	}
	defer outsider.Close()

	_, err = outsider.Set("/restricted-set", []byte("attacker"), 0)
	if err == nil {
		t.Fatal("unauthenticated Set() on an ACL-restricted znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoAuth) {
		t.Fatalf("Set() error = %v, want it to wrap zk.ErrNoAuth", err)
	}
}

// TestIntegrationDeleteWithCorrectVersionSucceeds verifies the golden path:
// Delete with the version just read succeeds, and the znode is actually
// gone afterward.
func TestIntegrationDeleteWithCorrectVersionSucceeds(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/todelete", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create(/todelete) failed: %v", err)
	}
	_, stat, err := client.Get("/todelete")
	if err != nil {
		t.Fatalf("Get(/todelete) failed: %v", err)
	}

	if err := client.Delete("/todelete", stat.Version); err != nil {
		t.Fatalf("Delete(/todelete, correct version) failed: %v", err)
	}

	_, _, err = client.Get("/todelete")
	if !errors.Is(err, upstream.ErrNoNode) {
		t.Fatalf("Get(/todelete) after Delete error = %v, want zk.ErrNoNode (the znode must actually be gone)", err)
	}
}

// TestIntegrationDeleteVersionGuardRejectsChangedNode mirrors the Set
// version-guard test for Delete: if the znode changed after the caller
// last read its Stat, Delete with the stale version must be rejected
// rather than deleting whatever the node has become.
func TestIntegrationDeleteVersionGuardRejectsChangedNode(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/changed-before-delete", []byte("v0"), CreateModePersistent); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	_, staleStat, err := client.Get("/changed-before-delete")
	if err != nil {
		t.Fatalf("Get() failed: %v", err)
	}

	// Someone else updates the znode after we read it (e.g. between opening
	// the delete confirmation and pressing "s").
	if _, err := client.Set("/changed-before-delete", []byte("v1"), staleStat.Version); err != nil {
		t.Fatalf("simulated concurrent Set() failed: %v", err)
	}

	err = client.Delete("/changed-before-delete", staleStat.Version)
	if err == nil {
		t.Fatal("Delete() with a stale version = nil error, want zk.ErrBadVersion")
	}
	if !errors.Is(err, upstream.ErrBadVersion) {
		t.Fatalf("Delete() with a stale version error = %v, want it to wrap zk.ErrBadVersion", err)
	}

	if _, _, err := client.Get("/changed-before-delete"); err != nil {
		t.Fatalf("Get() after the rejected stale Delete failed: %v, want the znode to still exist", err)
	}
}

// TestIntegrationDeleteNodeWithChildrenFailsNotEmptyNoRecursion verifies the
// "znode con hijos" mutation error explicitly called out by the acceptance
// criteria: Delete on a non-empty znode must fail with zk.ErrNotEmpty, and
// must never recurse into deleting the children itself.
func TestIntegrationDeleteNodeWithChildrenFailsNotEmptyNoRecursion(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/parent-nonempty", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create(parent) failed: %v", err)
	}
	if _, err := client.Create("/parent-nonempty/child", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create(child) failed: %v", err)
	}
	_, stat, err := client.Get("/parent-nonempty")
	if err != nil {
		t.Fatalf("Get(parent) failed: %v", err)
	}

	err = client.Delete("/parent-nonempty", stat.Version)
	if err == nil {
		t.Fatal("Delete() on a znode with children = nil error, want zk.ErrNotEmpty")
	}
	if !errors.Is(err, upstream.ErrNotEmpty) {
		t.Fatalf("Delete() error = %v, want it to wrap zk.ErrNotEmpty", err)
	}

	if _, _, err := client.Get("/parent-nonempty"); err != nil {
		t.Fatalf("Get(parent) after the rejected Delete failed: %v, want the parent to still exist", err)
	}
	if _, _, err := client.Get("/parent-nonempty/child"); err != nil {
		t.Fatalf("Get(child) after the rejected Delete failed: %v, want the child to still exist (no recursive deletion)", err)
	}
}

// TestIntegrationDeleteNonexistentNode verifies the "znode inexistente"
// mutation error for Delete.
func TestIntegrationDeleteNonexistentNode(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	err = client.Delete("/never-created", 0)
	if err == nil {
		t.Fatal("Delete() on a nonexistent znode = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoNode) {
		t.Fatalf("Delete() error = %v, want it to wrap zk.ErrNoNode", err)
	}
}

// TestIntegrationDeleteACLPermissionDenied verifies the "ACL" mutation
// error for Delete.
//
// ZooKeeper checks DELETE permission against the *parent* znode's ACL, not
// the target's own ACL (an oft-missed protocol quirk) — so the restrictive
// ACL has to be on the parent, with the target created underneath it by the
// owner, for an unauthenticated Delete on the target to actually be
// rejected.
func TestIntegrationDeleteACLPermissionDenied(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	ownerCfg := Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second, Auth: &Auth{Scheme: AuthSchemeDigest, Credential: "owner:ownerpass"}}
	owner, err := ConnectAndWait(context.Background(), ownerCfg, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(owner) failed: %v", err)
	}
	defer owner.Close()

	if _, err := owner.conn.Create("/restricted-parent", nil, 0, upstream.DigestACL(upstream.PermAll, "owner", "ownerpass")); err != nil {
		t.Fatalf("seed Create(/restricted-parent) failed: %v", err)
	}
	if _, err := owner.conn.Create("/restricted-parent/child", nil, 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/restricted-parent/child) failed: %v", err)
	}

	outsider, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(outsider) failed: %v", err)
	}
	defer outsider.Close()

	err = outsider.Delete("/restricted-parent/child", 0)
	if err == nil {
		t.Fatal("unauthenticated Delete() of a child under an ACL-restricted parent = nil error, want error")
	}
	if !errors.Is(err, upstream.ErrNoAuth) {
		t.Fatalf("Delete() error = %v, want it to wrap zk.ErrNoAuth", err)
	}
}

// TestIntegrationSetSurvivesDisconnectMidOperation verifies Set specifically
// (distinct machinery from Get/Children: it's a write) keeps working
// correctly across a real ensemble outage: calls made while the ensemble is
// down must fail promptly, and the same *Client must be able to Set
// successfully again once it's back, without the caller reconnecting
// manually.
func TestIntegrationSetSurvivesDisconnectMidOperation(t *testing.T) {
	requireDocker(t)

	containerName := fmt.Sprintf("zklens-test-setmidop-%d", time.Now().UnixNano())
	hostPort := reserveLocalPort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)

	runCtx, runCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer runCancel()
	if out, err := exec.CommandContext(runCtx, "docker", "run", "-d",
		"-p", fmt.Sprintf("127.0.0.1:%d:2181", hostPort), "--name", containerName, testImage).CombinedOutput(); err != nil {
		t.Skipf("could not start %s container (docker unavailable/offline?): %v: %s", testImage, err, out)
	}
	defer func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer killCancel()
		_ = exec.CommandContext(killCtx, "docker", "rm", "-f", containerName).Run()
	}()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{addr}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.conn.Create("/demo", []byte("v0"), 0, upstream.WorldACL(upstream.PermAll)); err != nil {
		t.Fatalf("seed Create(/demo) failed: %v", err)
	}

	var (
		mu               sync.Mutex
		sawErrorInOutage bool
	)
	stopPolling := make(chan struct{})
	pollingDone := make(chan struct{})
	go func() {
		defer close(pollingDone)
		for {
			select {
			case <-stopPolling:
				return
			default:
			}
			start := time.Now()
			// version=-1 (force): this test is only about surviving the
			// disconnect, not about version bookkeeping across a loop of
			// writes (already covered by the dedicated version-guard tests
			// above).
			_, callErr := client.Set("/demo", []byte("polled"), -1)
			d := time.Since(start)

			mu.Lock()
			if callErr != nil {
				sawErrorInOutage = true
			}
			mu.Unlock()

			if d > 10*time.Second {
				t.Errorf("Set(/demo) took %v mid-test, want every call bounded well under the ensemble outage window", d)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	time.Sleep(300 * time.Millisecond)
	dockerStop(t, containerName)
	time.Sleep(1500 * time.Millisecond)
	dockerStart(t, containerName)

	reconnected := waitForState(t, client.Events(), 60*time.Second, func(s State) bool {
		return s == upstream.StateHasSession
	})

	time.Sleep(500 * time.Millisecond)
	close(stopPolling)
	<-pollingDone

	if !reconnected {
		t.Fatal("did not observe StateHasSession after restarting the ensemble; client did not reconnect")
	}

	mu.Lock()
	sawErr := sawErrorInOutage
	mu.Unlock()
	if !sawErr {
		t.Fatal("no Set() call failed while the ensemble was stopped; the outage window may not have overlapped a call — increase the stop duration")
	}

	if _, err := client.Set("/demo", []byte("final"), -1); err != nil {
		t.Fatalf("Set(/demo) after the ensemble recovered = %v, want success", err)
	}
}

// --- ChildrenW / GetW (live watches) -----------------------------------------

// TestIntegrationChildrenWMatchesChildrenAndFiresOnce verifies, against a
// real server: ChildrenW returns the same children Children would, its
// watch channel fires exactly once when a child is added by another
// connection, and — this is the core "one-shot" guarantee the acceptance
// criteria hinge on — a *second* change afterward does NOT produce a
// second event on that same channel, because nothing re-armed it.
func TestIntegrationChildrenWMatchesChildrenAndFiresOnce(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	other, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(other) failed: %v", err)
	}
	defer other.Close()

	if _, err := client.Create("/watched-parent", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create(/watched-parent) failed: %v", err)
	}
	plainChildren, err := client.Children("/watched-parent")
	if err != nil {
		t.Fatalf("Children(/watched-parent) failed: %v", err)
	}

	wChildren, events, err := client.ChildrenW("/watched-parent")
	if err != nil {
		t.Fatalf("ChildrenW(/watched-parent) failed: %v", err)
	}
	if len(wChildren) != len(plainChildren) {
		t.Fatalf("ChildrenW() children = %v, want the same as Children() = %v", wChildren, plainChildren)
	}

	if _, err := other.Create("/watched-parent/new-child", nil, CreateModePersistent); err != nil {
		t.Fatalf("other.Create(/watched-parent/new-child) failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Type != upstream.EventNodeChildrenChanged {
			t.Fatalf("watch event type = %v, want EventNodeChildrenChanged", ev.Type)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ChildrenW's watch channel did not fire within 10s after a child was added")
	}

	// The watch already fired once; go-zookeeper/zk closes the channel
	// right after delivering that single event (see notifyWatches), so
	// without re-arming (a fresh ChildrenW call, which the UI does but this
	// test deliberately does not), a second, unrelated change must produce
	// no further *real* event — reading the channel again must see it
	// already closed, not block waiting for a live second notification.
	if _, err := other.Create("/watched-parent/second-child", nil, CreateModePersistent); err != nil {
		t.Fatalf("other.Create(/watched-parent/second-child) failed: %v", err)
	}
	select {
	case ev, ok := <-events:
		if ok {
			t.Fatalf("watch channel produced a second live event %+v without being re-armed; ZK watches must be one-shot", ev)
		}
		// ok == false: the channel is closed, as one-shot semantics demand.
	case <-time.After(2 * time.Second):
		t.Fatal("reading the already-fired watch channel again blocked instead of seeing it closed")
	}
}

// TestIntegrationChildrenWFiresOnParentDeleted verifies that a children
// watch also fires when the watched znode itself is deleted (not just when
// a child is added/removed) — the scenario the UI's "node observed gets
// deleted" error handling depends on.
func TestIntegrationChildrenWFiresOnParentDeleted(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/to-be-deleted-watched", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	_, events, err := client.ChildrenW("/to-be-deleted-watched")
	if err != nil {
		t.Fatalf("ChildrenW() failed: %v", err)
	}

	if err := client.Delete("/to-be-deleted-watched", 0); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Type != upstream.EventNodeDeleted {
			t.Fatalf("watch event type = %v, want EventNodeDeleted", ev.Type)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ChildrenW's watch channel did not fire within 10s after the watched node was deleted")
	}
}

// TestIntegrationGetWMatchesGetAndFiresOnce mirrors
// TestIntegrationChildrenWMatchesChildrenAndFiresOnce for GetW: same
// current data as Get, fires exactly once on a data change, and does not
// fire again for a second change without being re-armed.
func TestIntegrationGetWMatchesGetAndFiresOnce(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	other, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(other) failed: %v", err)
	}
	defer other.Close()

	if _, err := client.Create("/watched-data", []byte("v0"), CreateModePersistent); err != nil {
		t.Fatalf("Create(/watched-data) failed: %v", err)
	}

	wData, wStat, events, err := client.GetW("/watched-data")
	if err != nil {
		t.Fatalf("GetW(/watched-data) failed: %v", err)
	}
	if string(wData) != "v0" {
		t.Fatalf("GetW() data = %q, want %q", wData, "v0")
	}
	if wStat.Version != 0 {
		t.Fatalf("GetW() stat.Version = %d, want 0", wStat.Version)
	}

	if _, err := other.Set("/watched-data", []byte("v1"), 0); err != nil {
		t.Fatalf("other.Set(/watched-data) failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Type != upstream.EventNodeDataChanged {
			t.Fatalf("watch event type = %v, want EventNodeDataChanged", ev.Type)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GetW's watch channel did not fire within 10s after the data changed")
	}

	if _, err := other.Set("/watched-data", []byte("v2"), 1); err != nil {
		t.Fatalf("other.Set(/watched-data) (second change) failed: %v", err)
	}
	// As above: go-zookeeper/zk closes the channel right after the one
	// event it delivers, so the second change must show up as a closed
	// channel, not a live second notification.
	select {
	case ev, ok := <-events:
		if ok {
			t.Fatalf("watch channel produced a second live event %+v without being re-armed; ZK watches must be one-shot", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading the already-fired watch channel again blocked instead of seeing it closed")
	}
}

// TestIntegrationGetWFiresOnNodeDeleted mirrors
// TestIntegrationChildrenWFiresOnParentDeleted for GetW.
func TestIntegrationGetWFiresOnNodeDeleted(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/data-to-be-deleted", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}
	_, _, events, err := client.GetW("/data-to-be-deleted")
	if err != nil {
		t.Fatalf("GetW() failed: %v", err)
	}

	if err := client.Delete("/data-to-be-deleted", 0); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	select {
	case ev := <-events:
		if ev.Type != upstream.EventNodeDeleted {
			t.Fatalf("watch event type = %v, want EventNodeDeleted", ev.Type)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("GetW's watch channel did not fire within 10s after the watched node was deleted")
	}
}

// TestIntegrationChildrenWReArmedByCallingItAgainObservesSecondChange
// proves the flip side of the one-shot guarantee: calling ChildrenW again
// after the first fire (exactly what the UI's re-arm loop does) *does*
// let the caller observe a second, independent change — the watch itself
// never re-arms, but calling the method again works every time, not just
// once.
func TestIntegrationChildrenWReArmedByCallingItAgainObservesSecondChange(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	if _, err := client.Create("/rearm-parent", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		_, events, err := client.ChildrenW("/rearm-parent")
		if err != nil {
			t.Fatalf("ChildrenW() call #%d failed: %v", i+1, err)
		}
		childName := fmt.Sprintf("/rearm-parent/child%d", i)
		if _, err := client.Create(childName, nil, CreateModePersistent); err != nil {
			t.Fatalf("Create(%s) failed: %v", childName, err)
		}
		select {
		case ev := <-events:
			if ev.Type != upstream.EventNodeChildrenChanged {
				t.Fatalf("cycle #%d: watch event type = %v, want EventNodeChildrenChanged", i+1, ev.Type)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("cycle #%d: watch channel did not fire within 10s after re-arming", i+1)
		}
	}
}

// TestIntegrationGetWDoesNotFireOnChildCreated pins down the ZooKeeper
// semantics behind zklens's "stale Stat panel" bug: a data-watch (GetW) only
// fires on a setData or a delete of the watched znode itself, never on a
// child being created underneath it — even though creating a child bumps
// the parent's own Stat (cversion, numChildren, pzxid). Children/ChildrenW
// see the new child; a GetW watch armed on the parent stays completely
// silent.
func TestIntegrationGetWDoesNotFireOnChildCreated(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	other, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait(other) failed: %v", err)
	}
	defer other.Close()

	if _, err := client.Create("/data-watch-parent", nil, CreateModePersistent); err != nil {
		t.Fatalf("Create(/data-watch-parent) failed: %v", err)
	}

	_, beforeStat, events, err := client.GetW("/data-watch-parent")
	if err != nil {
		t.Fatalf("GetW(/data-watch-parent) failed: %v", err)
	}

	if _, err := other.Create("/data-watch-parent/child", nil, CreateModePersistent); err != nil {
		t.Fatalf("other.Create(/data-watch-parent/child) failed: %v", err)
	}

	select {
	case ev, ok := <-events:
		if ok {
			t.Fatalf("GetW's watch channel fired %+v after a child was created; want it to stay silent — data-watches don't cover child changes", ev)
		}
	case <-time.After(2 * time.Second):
		// Expected: the data-watch neither fires nor closes within this
		// window, because creating a child isn't an event it watches for.
	}

	afterData, afterStat, err := client.Get("/data-watch-parent")
	if err != nil {
		t.Fatalf("Get(/data-watch-parent) failed: %v", err)
	}
	if len(afterData) != 0 {
		t.Fatalf("Get(/data-watch-parent) data = %q, want empty: creating a child must not touch the parent's data", afterData)
	}
	if afterStat.Cversion == beforeStat.Cversion {
		t.Fatalf("stat.Cversion unchanged after creating a child (before=%d, after=%d): want it bumped even though the data-watch didn't fire", beforeStat.Cversion, afterStat.Cversion)
	}
	if afterStat.NumChildren != 1 {
		t.Fatalf("stat.NumChildren = %d, want 1 after creating one child", afterStat.NumChildren)
	}
}

// TestIntegrationExistsReportsNumChildren verifies the field the tree
// explorer's markers hang on: NumChildren must reflect the node's real
// children, since it is what tells a leaf from an unexpanded subtree.
func TestIntegrationExistsReportsNumChildren(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	acl := upstream.WorldACL(upstream.PermAll)
	for _, path := range []string{"/rama", "/rama/uno", "/rama/dos", "/hoja"} {
		if _, err := client.conn.Create(path, nil, 0, acl); err != nil {
			t.Fatalf("seed Create(%s) failed: %v", path, err)
		}
	}

	ok, stat, err := client.Exists("/rama")
	if err != nil {
		t.Fatalf("Exists(/rama) failed: %v", err)
	}
	if !ok || stat == nil {
		t.Fatalf("Exists(/rama) = %v, stat=%v, want true and a Stat", ok, stat)
	}
	if stat.NumChildren != 2 {
		t.Fatalf("Exists(/rama) stat.NumChildren = %d, want 2", stat.NumChildren)
	}

	ok, stat, err = client.Exists("/hoja")
	if err != nil {
		t.Fatalf("Exists(/hoja) failed: %v", err)
	}
	if !ok || stat == nil {
		t.Fatalf("Exists(/hoja) = %v, stat=%v, want true and a Stat", ok, stat)
	}
	if stat.NumChildren != 0 {
		t.Fatalf("Exists(/hoja) stat.NumChildren = %d, want 0 for a leaf", stat.NumChildren)
	}
}

// TestIntegrationExistsNonexistentNode pins the contract the tree relies on:
// a missing znode is reported as absent, not as an error, so a child deleted
// between listing and the count lookup does not surface as a failure.
func TestIntegrationExistsNonexistentNode(t *testing.T) {
	hostPort, stop := startZKContainer(t)
	defer stop()

	client, err := ConnectAndWait(context.Background(), Config{Hosts: []string{hostPort}, SessionTimeout: 6 * time.Second}, 60*time.Second)
	if err != nil {
		t.Fatalf("ConnectAndWait() failed: %v", err)
	}
	defer client.Close()

	ok, stat, err := client.Exists("/no-existe")
	if err != nil {
		t.Fatalf("Exists(/no-existe) = error %v, want a clean false", err)
	}
	if ok {
		t.Fatal("Exists(/no-existe) = true, want false")
	}
	if stat != nil {
		t.Fatalf("Exists(/no-existe) stat = %+v, want nil", stat)
	}
}
