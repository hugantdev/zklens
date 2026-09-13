package zk

import (
	"fmt"
	"sort"

	"github.com/go-zookeeper/zk"
)

// Children returns the names (not full paths) of path's immediate
// children, sorted for stable display. It performs a blocking round trip
// to the ensemble, so callers driving a bubbletea UI must invoke it from a
// tea.Cmd, never from Update.
func (c *Client) Children(path string) ([]string, error) {
	children, _, err := c.conn.Children(path)
	if err != nil {
		return nil, fmt.Errorf("zk: children of %s: %w", path, err)
	}
	sort.Strings(children)
	return children, nil
}

// Get returns path's raw data and full Stat. It performs a blocking round
// trip to the ensemble, so callers driving a bubbletea UI must invoke it
// from a tea.Cmd, never from Update.
//
// Get does not set a watch or subscribe to live changes; that is a
// separate, not-yet-implemented operation.
func (c *Client) Get(path string) ([]byte, *Stat, error) {
	data, stat, err := c.conn.Get(path)
	if err != nil {
		return nil, nil, fmt.Errorf("zk: get %s: %w", path, err)
	}
	return data, stat, nil
}

// Exists reports whether path exists and, when it does, returns its Stat.
// A missing znode is not an error: it comes back as (false, nil, nil).
//
// The tree explorer uses it to learn a listed child's NumChildren without
// pulling its data, which Get would. It performs a blocking round trip to the
// ensemble, so callers driving a bubbletea UI must invoke it from a tea.Cmd,
// never from Update.
func (c *Client) Exists(path string) (bool, *Stat, error) {
	ok, stat, err := c.conn.Exists(path)
	if err != nil {
		return false, nil, fmt.Errorf("zk: exists %s: %w", path, err)
	}
	if !ok {
		// The driver hands back a zeroed Stat rather than nil for a missing
		// znode, which reads exactly like a real node with no children. Drop
		// it so callers cannot mistake absence for an empty node.
		return false, nil, nil
	}
	return true, stat, nil
}

// ChildrenW is Children plus a one-shot watch: the returned channel
// receives exactly one zk.Event the next time path's children change (a
// child created or deleted) or path itself is deleted. ZooKeeper watches
// never re-arm themselves — once the channel fires (or is closed, e.g.
// because the connection was lost), the watch is gone. Calling ChildrenW
// again is how you keep observing; re-arming is the caller's
// responsibility, not this client's. It performs a blocking round trip to
// the ensemble, so callers driving a bubbletea UI must invoke it from a
// tea.Cmd, never from Update.
func (c *Client) ChildrenW(path string) ([]string, <-chan Event, error) {
	children, _, events, err := c.conn.ChildrenW(path)
	if err != nil {
		return nil, nil, fmt.Errorf("zk: children of %s: %w", path, err)
	}
	sort.Strings(children)
	return children, events, nil
}

// GetW is Get plus a one-shot watch: the returned channel receives exactly
// one zk.Event the next time path's data changes or path itself is
// deleted. See ChildrenW's doc comment for why the watch is one-shot and
// must be re-armed by the caller (by calling GetW again), not by this
// client.
func (c *Client) GetW(path string) ([]byte, *Stat, <-chan Event, error) {
	data, stat, events, err := c.conn.GetW(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("zk: get %s: %w", path, err)
	}
	return data, stat, events, nil
}

// CreateMode selects the kind of znode Create makes.
type CreateMode int32

const (
	// CreateModePersistent creates a znode that survives the creating
	// session ending.
	CreateModePersistent CreateMode = CreateMode(zk.FlagPersistent)
	// CreateModeEphemeral creates a znode that is deleted automatically
	// when the session that created it ends.
	CreateModeEphemeral CreateMode = CreateMode(zk.FlagEphemeral)
	// CreateModePersistentSequential creates a persistent znode with a
	// monotonically increasing sequence number appended to path by the
	// server; the assigned path is Create's return value.
	CreateModePersistentSequential CreateMode = CreateMode(zk.FlagSequence)
	// CreateModeEphemeralSequential combines CreateModeEphemeral and
	// CreateModePersistentSequential.
	CreateModeEphemeralSequential CreateMode = CreateMode(zk.FlagEphemeralSequential)
)

// Create creates path as a new znode with data and mode, and returns the
// path the server actually assigned (which differs from the requested
// path for a sequential mode: the server appends a suffix). It performs a
// blocking round trip to the ensemble, so callers driving a bubbletea UI
// must invoke it from a tea.Cmd, never from Update.
//
// Create always uses an open ACL (world, all permissions): ACL management
// is out of scope for zklens's MVP.
func (c *Client) Create(path string, data []byte, mode CreateMode) (string, error) {
	actualPath, err := c.conn.Create(path, data, int32(mode), zk.WorldACL(zk.PermAll))
	if err != nil {
		return "", fmt.Errorf("zk: create %s: %w", path, err)
	}
	return actualPath, nil
}

// Set overwrites path's data and returns the resulting Stat. version is an
// optimistic-concurrency guard: the write is rejected with
// zk.ErrBadVersion unless the znode's current Stat.Version still matches
// it. Callers should read the current Stat (Client.Get) first and pass its
// Version; passing -1 forces the write regardless of version, which
// go-zookeeper/zk supports but callers should only do deliberately, not by
// default. It performs a blocking round trip to the ensemble, so callers
// driving a bubbletea UI must invoke it from a tea.Cmd, never from Update.
func (c *Client) Set(path string, data []byte, version int32) (*Stat, error) {
	stat, err := c.conn.Set(path, data, version)
	if err != nil {
		return nil, fmt.Errorf("zk: set %s: %w", path, err)
	}
	return stat, nil
}

// Delete removes path. version guards against a concurrent change the same
// way Set's does (see Set's doc comment), and -1 forces the delete
// regardless of version. ZK rejects deleting a znode that still has
// children with zk.ErrNotEmpty; Delete does not recurse into children
// itself. It performs a blocking round trip to the ensemble, so callers
// driving a bubbletea UI must invoke it from a tea.Cmd, never from Update.
func (c *Client) Delete(path string, version int32) error {
	if err := c.conn.Delete(path, version); err != nil {
		return fmt.Errorf("zk: delete %s: %w", path, err)
	}
	return nil
}
