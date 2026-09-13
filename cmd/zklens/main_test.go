package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hugantdev/zklens/internal/ui"
)

// neverPick fails the test if the startup picker is invoked; the tests
// using it have no contexts configured, so there is nothing to pick.
func neverPick(t *testing.T) func([]ui.ContextItem) (int, error) {
	t.Helper()
	return func([]ui.ContextItem) (int, error) {
		t.Fatal("pickContext called, but no contexts are configured")
		return 0, errContextCancelled
	}
}

func TestRunHelpFlagExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, func(string) string { return "" }, &stdout, &stderr, neverPick(t))
	if code != 0 {
		t.Fatalf("run(--help) exit code = %d, want 0", code)
	}
}

func TestRunUnknownFlagExitsWithUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--does-not-exist"}, func(string) string { return "" }, &stdout, &stderr, neverPick(t))
	if code != 2 {
		t.Fatalf("run(--does-not-exist) exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "zklens:") {
		t.Fatalf("run(--does-not-exist) stderr = %q, want it to report the error", stderr.String())
	}
}

func TestRunConnectFailureExitsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the real zk.DefaultConnectTimeout (10s); skipped in -short")
	}

	// Reserve a port and close it immediately so nothing is listening.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	host := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}

	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := run([]string{"--hosts=" + host}, func(string) string { return "" }, &stdout, &stderr, neverPick(t))
	elapsed := time.Since(start)

	if code != 1 {
		t.Fatalf("run() against an unreachable host exit code = %d, want 1 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "connection failed") {
		t.Fatalf("run() stderr = %q, want it to mention the connection failure", stderr.String())
	}
	if elapsed > 15*time.Second {
		t.Fatalf("run() took %v to report a connection failure, want it bounded by zk.DefaultConnectTimeout", elapsed)
	}
}

func TestRunContextSelectionAppliesChosenContext(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the real zk.DefaultConnectTimeout (10s); skipped in -short")
	}

	// Nothing listens on this port: the run must fail fast (bounded by the
	// connect timeout) after picking the context, which is what lets us
	// observe which context the picker fed into the connection attempt.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	host := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("failed to close listener: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	content := `{"contexts": [
		{"name": "dev", "hosts": ["dev.invalid:2181"]},
		{"name": "prod", "hosts": ["` + host + `"]}
	]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	picked := false
	pick := func(items []ui.ContextItem) (int, error) {
		picked = true
		if len(items) != 2 || items[1].Name != "prod" {
			t.Fatalf("pickContext items = %+v, want the two contexts with prod second", items)
		}
		return 1, nil // the user picks prod
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--config=" + path}, func(string) string { return "" }, &stdout, &stderr, pick)
	if !picked {
		t.Fatal("pickContext was never called, want the startup picker for two unselected contexts")
	}
	if code != 1 {
		t.Fatalf("run() exit code = %d, want 1 (connection to the picked context must fail)", code)
	}
	if !strings.Contains(stdout.String(), "context prod") {
		t.Fatalf("run() stdout = %q, want it to identify the picked context", stdout.String())
	}
	if !strings.Contains(stdout.String(), host) {
		t.Fatalf("run() stdout = %q, want it to connect to the picked context's hosts", stdout.String())
	}
}

func TestRunContextPickerCancelExitsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	content := `{"contexts": [
		{"name": "dev", "hosts": ["dev.invalid:2181"]},
		{"name": "prod", "hosts": ["prod.invalid:2181"]}
	]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--config=" + path}, func(string) string { return "" }, &stdout, &stderr,
		func([]ui.ContextItem) (int, error) { return 0, errContextCancelled })
	if code != 0 {
		t.Fatalf("run() with a cancelled picker exit code = %d, want 0 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
}

func TestRunContextPickerFailureExitsOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zklens.json")
	content := `{"contexts": [
		{"name": "dev", "hosts": ["dev.invalid:2181"]},
		{"name": "prod", "hosts": ["prod.invalid:2181"]}
	]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"--config=" + path}, func(string) string { return "" }, &stdout, &stderr,
		func([]ui.ContextItem) (int, error) {
			return 0, errors.New("context selection failed: no usable terminal")
		})
	if code != 1 {
		t.Fatalf("run() with a failed picker exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "context selection failed") {
		t.Fatalf("run() stderr = %q, want it to report the picker failure", stderr.String())
	}
}
