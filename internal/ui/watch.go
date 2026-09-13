package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hugantdev/zklens/internal/zk"
)

// This file wires up live ZooKeeper watches. ZooKeeper watches are
// one-shot (see Client.ChildrenW/GetW's doc comments), so each watch cycle
// is: arm (ChildrenW/GetW, which also returns the current data in the same
// round trip) -> listen for the single fire -> arm again. Three independent
// watch flows exist: a children-watch per tree node (toggled with 'w' in
// the tree), a data-watch on whichever node is open in the detail panel
// (toggled with 'w' there), and a stat-watch that piggybacks on the same
// toggle to keep that node's Stat fresh (see the comment on statWatchCmd
// for why the data-watch alone isn't enough). Their messages are kept
// distinct from each other and from connEventMsg/connClosedMsg
// (connection-level events) so a watch firing is never confused with a
// connection state change or with another watch flow.

// --- children watch (tree) --------------------------------------------------

// childWatchArmedMsg is the result of (re)arming a children-watch: it
// carries the current children (so the same round trip refreshes the tree,
// not just subscribes) alongside the channel to listen on for the next
// fire.
type childWatchArmedMsg struct {
	path       string
	generation uint64
	children   []string
	events     <-chan zk.Event
	err        error
}

// watchChildrenCmd arms (or re-arms) a one-shot watch on path's children.
// It is the only place internal/ui calls Client.ChildrenW, and only ever
// runs inside a tea.Cmd closure, never inside Update.
func watchChildrenCmd(client *zk.Client, path string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		children, events, err := client.ChildrenW(path)
		return childWatchArmedMsg{path: path, generation: generation, children: children, events: events, err: err}
	}
}

// childWatchFiredMsg reports that a previously-armed children-watch fired
// (or its channel closed, e.g. the connection dropped).
type childWatchFiredMsg struct {
	path       string
	generation uint64
	ev         zk.Event
}

// listenChildWatchCmd blocks until path's one-shot children watch fires,
// then returns. The watch must be re-armed (via watchChildrenCmd) to keep
// observing — ZooKeeper never re-arms a watch on its own behalf, and
// neither does this command.
func listenChildWatchCmd(path string, generation uint64, events <-chan zk.Event, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-events:
			return childWatchFiredMsg{path: path, generation: generation, ev: ev}
		case <-done:
			return nil
		}
	}
}

// --- data watch (detail panel) ----------------------------------------------

// dataWatchArmedMsg is the result of (re)arming a data-watch: like
// childWatchArmedMsg, it carries the current data/Stat alongside the
// channel to listen on next.
type dataWatchArmedMsg struct {
	path       string
	generation uint64
	data       []byte
	stat       *zk.Stat
	events     <-chan zk.Event
	err        error
}

// watchDataCmd arms (or re-arms) a one-shot watch on path's data. It is
// the only place internal/ui calls Client.GetW, and only ever runs inside
// a tea.Cmd closure.
func watchDataCmd(client *zk.Client, path string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		data, stat, events, err := client.GetW(path)
		return dataWatchArmedMsg{path: path, generation: generation, data: data, stat: stat, events: events, err: err}
	}
}

// dataWatchFiredMsg reports that a previously-armed data-watch fired (data
// changed, or the znode itself was deleted).
type dataWatchFiredMsg struct {
	path       string
	generation uint64
	ev         zk.Event
}

// listenDataWatchCmd blocks until path's one-shot data watch fires, then
// returns. Must be re-armed via watchDataCmd to keep observing.
func listenDataWatchCmd(path string, generation uint64, events <-chan zk.Event, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-events:
			return dataWatchFiredMsg{path: path, generation: generation, ev: ev}
		case <-done:
			return nil
		}
	}
}

// --- stat watch (detail panel's Stat freshness) -----------------------------
//
// A data-watch (GetW) only fires on a setData or a delete of the watched
// znode itself — never on a child being created or removed underneath it,
// even though that bumps the znode's own Stat (cversion, numChildren,
// pzxid; see TestIntegrationGetWDoesNotFireOnChildCreated). Left alone, the
// Stat panel would show a stale child count for as long as the watch stays
// armed and nothing else touches the node's data. So arming the detail
// watch also arms this second, independent children-watch on the same
// path, purely to learn *that* something changed; statRefreshCmd then
// re-reads just the Stat (not the data, which this watch never touches).

// statWatchArmedMsg is the result of (re)arming the stat-watch. It carries
// no children list: the Stat panel doesn't display children names, only
// counts and version fields already covered by refreshing Stat once the
// watch fires.
type statWatchArmedMsg struct {
	path       string
	generation uint64
	events     <-chan zk.Event
	err        error
}

// statWatchCmd arms (or re-arms) the stat-watch. Like watchChildrenCmd, it
// only ever runs inside a tea.Cmd closure.
func statWatchCmd(client *zk.Client, path string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		_, events, err := client.ChildrenW(path)
		return statWatchArmedMsg{path: path, generation: generation, events: events, err: err}
	}
}

// statWatchFiredMsg reports that the stat-watch fired: a child of path was
// created or removed (or path itself was deleted).
type statWatchFiredMsg struct {
	path       string
	generation uint64
	ev         zk.Event
}

// listenStatWatchCmd blocks until path's one-shot stat-watch fires, then
// returns. Must be re-armed via statWatchCmd to keep observing.
func listenStatWatchCmd(path string, generation uint64, events <-chan zk.Event, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case ev := <-events:
			return statWatchFiredMsg{path: path, generation: generation, ev: ev}
		case <-done:
			return nil
		}
	}
}

// statRefreshedMsg is the result of re-reading path's Stat after its
// stat-watch fired. Only Stat is re-read (via Exists, not Get): the fire
// means a child changed, not the node's own data, so re-fetching the data
// payload would be wasted work.
type statRefreshedMsg struct {
	path   string
	exists bool
	stat   *zk.Stat
	err    error
}

// statRefreshCmd re-reads path's Stat. Only ever runs inside a tea.Cmd
// closure.
func statRefreshCmd(client *zk.Client, path string) tea.Cmd {
	return func() tea.Msg {
		exists, stat, err := client.Exists(path)
		return statRefreshedMsg{path: path, exists: exists, stat: stat, err: err}
	}
}
