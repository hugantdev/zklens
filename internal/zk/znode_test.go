package zk

import (
	"context"
	"testing"
	"time"
)

// TestChildrenOnUnreachableEnsembleReturnsBoundedError guards against
// Children hanging forever: even without a session ever being established,
// a call must come back with an error in bounded time rather than blocking
// the caller (which, per the UI's design, would block a tea.Cmd goroutine
// indefinitely).
func TestChildrenOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, err := client.Children("/whatever")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Children() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Children() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestGetOnUnreachableEnsembleReturnsBoundedError mirrors
// TestChildrenOnUnreachableEnsembleReturnsBoundedError for Get: it must
// never hang, since the UI's detail panel invokes it from a tea.Cmd
// goroutine that a caller may be waiting on.
func TestGetOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, _, err := client.Get("/whatever")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Get() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Get() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestCreateOnUnreachableEnsembleReturnsBoundedError mirrors the Children/
// Get bounded-error guards for Create: the create wizard invokes it from a
// tea.Cmd goroutine, so it must never hang.
func TestCreateOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, err := client.Create("/whatever", nil, CreateModePersistent)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Create() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Create() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestSetOnUnreachableEnsembleReturnsBoundedError mirrors the same guard
// for Set.
func TestSetOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, err := client.Set("/whatever", nil, -1)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Set() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Set() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestDeleteOnUnreachableEnsembleReturnsBoundedError mirrors the same guard
// for Delete.
func TestDeleteOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		err := client.Delete("/whatever", -1)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Delete() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Delete() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestChildrenWOnUnreachableEnsembleReturnsBoundedError mirrors the
// bounded-error guard for ChildrenW: the tree's watch toggle invokes it
// from a tea.Cmd goroutine, so it must never hang.
func TestChildrenWOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, _, err := client.ChildrenW("/whatever")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ChildrenW() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ChildrenW() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestGetWOnUnreachableEnsembleReturnsBoundedError mirrors the same guard
// for GetW.
func TestGetWOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, _, _, err := client.GetW("/whatever")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("GetW() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetW() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}

// TestExistsOnUnreachableEnsembleReturnsBoundedError mirrors
// TestChildrenOnUnreachableEnsembleReturnsBoundedError for Exists: the tree
// explorer fires one per listed child from a tea.Cmd goroutine, so a hang
// would leak a goroutine per child of every node the user expands.
func TestExistsOnUnreachableEnsembleReturnsBoundedError(t *testing.T) {
	cfg := Config{Hosts: []string{closedPortHost(t)}}
	client, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Connect() setup error = %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		_, _, err := client.Exists("/whatever")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Exists() against an unreachable ensemble = nil error, want error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exists() against an unreachable ensemble did not return within 5s, want a bounded failure")
	}
}
