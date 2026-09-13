package ui

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hugantdev/zklens/internal/zk"
)

// newTestModel builds a Model without a real *zk.Client (left nil), the way
// New would but bypassing the connection. Update must never dereference
// m.client synchronously (only from inside a returned tea.Cmd closure), so
// a nil client here turns any accidental synchronous use into an immediate,
// loud test failure (a nil pointer panic) instead of silently passing.
func newTestModel(root *node, nodes map[string]*node) Model {
	l := newTreeList()
	l.SetSize(80, 20)

	m := Model{root: root, nodes: nodes}
	m.list = l
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot))
	m.help = newHelp()
	m.dataVP = viewport.New(80, 20)
	m.refreshItems()
	return m
}

func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func selectedPath(m Model) string {
	it, ok := m.list.SelectedItem().(treeItem)
	if !ok {
		return ""
	}
	return it.n.path
}

// --- expand / collapse / lazy loading ---------------------------------

func TestUpdateToggleExpandOnUnloadedNodeReturnsFetchCmdWithoutBlocking(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: false}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0) // root

	newModel, cmd := m.Update(keyMsg("enter"))
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("Update(enter) on an unloaded node returned a nil cmd, want a fetch command")
	}
	root2 := nm.nodes["/"]
	if !root2.expanded {
		t.Fatal("root.expanded = false after toggling, want true")
	}
	if !root2.loading {
		t.Fatal("root.loading = false after triggering a fetch, want true")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
	// The returned cmd captures a nil m.client; that's fine as long as
	// nothing calls it. If Update itself had synchronously called
	// client.Children (a violation of "only from tea.Cmd"), it would have
	// panicked already above with a nil pointer dereference.
}

func TestUpdateCollapseLoadedExpandedNodeDoesNotFetch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true, expanded: true}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a}

	m := newTestModel(root, nodes)
	m.list.Select(1) // /a

	newModel, cmd := m.Update(keyMsg("left"))
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(left) collapsing an already-loaded node returned a non-nil cmd, want nil (cached children, no re-fetch)")
	}
	if nm.nodes["/a"].expanded {
		t.Fatal("/a.expanded = true after collapsing, want false")
	}
}

func TestUpdateReExpandLoadedNodeUsesCacheNoFetch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true, expanded: false}
	a1 := &node{path: "/a/1", name: "1", parent: a}
	a.children = []*node{a1}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a, "/a/1": a1}

	m := newTestModel(root, nodes)
	m.list.Select(1) // /a, collapsed

	newModel, cmd := m.Update(keyMsg("right"))
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(right) re-expanding a previously-loaded node returned a non-nil cmd, want nil (must reuse cached children)")
	}
	if !nm.nodes["/a"].expanded {
		t.Fatal("/a.expanded = false after re-expanding, want true")
	}
	got := namesOf(visibleNodes(nm.root))
	want := []string{"/", "/a", "/a/1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visible nodes after re-expand = %v, want %v (cached child should reappear)", got, want)
	}
}

func TestUpdateCollapseOnAlreadyCollapsedNodeMovesToParent(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true, expanded: true}
	b := &node{path: "/a/b", name: "b", parent: a, loaded: true, expanded: false}
	a.children = []*node{b}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a, "/a/b": b}

	m := newTestModel(root, nodes)
	m.list.Select(2) // /a/b, already collapsed and has no visible children

	newModel, _ := m.Update(keyMsg("left"))
	nm := newModel.(Model)

	if got := selectedPath(nm); got != "/a" {
		t.Fatalf("selected path after collapsing an already-collapsed node = %q, want %q (jump to parent)", got, "/a")
	}
}

func TestUpdateEnterOnRootWithNoSelectionDoesNotPanic(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: false}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	// Toggling twice: first expands (fetch), second collapses (no fetch).
	newModel, cmd := m.Update(keyMsg("enter"))
	nm := newModel.(Model)
	if cmd == nil {
		t.Fatal("first Update(enter) = nil cmd, want fetch cmd")
	}
	nm.nodes["/"].loading = false // simulate fetch already completed
	newModel2, cmd2 := nm.Update(keyMsg("enter"))
	nm2 := newModel2.(Model)
	if cmd2 != nil {
		t.Fatal("second Update(enter) (collapse) = non-nil cmd, want nil")
	}
	if nm2.nodes["/"].expanded {
		t.Fatal("root.expanded = true after collapsing, want false")
	}
}

// --- childrenMsg handling ------------------------------------------------

func TestUpdateChildrenMsgSuccessPopulatesTreeAndStatus(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loading: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	newModel, cmd := m.Update(childrenMsg{path: "/", children: []string{"a", "b"}})
	nm := newModel.(Model)

	// Children returns only names, so each new child's own child count is
	// looked up separately to tell a leaf from an unexpanded subtree.
	if cmd == nil {
		t.Fatal("Update(childrenMsg success) returned a nil cmd, want the child-count lookups")
	}
	// Those lookups are deliberately not counted: the tree is already usable,
	// and counting them would leave the spinner running after every expansion.
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
	if nm.root.loading {
		t.Fatal("root.loading = true after childrenMsg, want false")
	}
	if !nm.root.loaded {
		t.Fatal("root.loaded = false after successful childrenMsg, want true")
	}
	if nm.root.err != nil {
		t.Fatalf("root.err = %v, want nil", nm.root.err)
	}
	if nm.statusErr {
		t.Fatal("statusErr = true after a successful fetch, want false")
	}
	if len(nm.root.children) != 2 || nm.root.children[0].name != "a" || nm.root.children[1].name != "b" {
		t.Fatalf("root.children = %+v, want [a b] in the order the message provided", nm.root.children)
	}
	if _, ok := nm.nodes["/a"]; !ok {
		t.Fatal("child /a not registered in nodes map, lazy re-expansion would refetch or fail to find it")
	}
	got := namesOf(visibleNodes(nm.root))
	want := []string{"/", "/a", "/b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visible nodes after childrenMsg = %v, want %v", got, want)
	}
}

func TestUpdateChildrenMsgRefreshRetainsSurvivingExpandedSubtree(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", depth: 1, parent: root, expanded: true, loaded: true}
	a1 := &node{path: "/a/one", name: "one", depth: 2, parent: a, loaded: true}
	stale := &node{path: "/stale", name: "stale", depth: 1, parent: root}
	a.children = []*node{a1}
	root.children = []*node{a, stale}
	m := newTestModel(root, map[string]*node{
		"/": root, "/a": a, "/a/one": a1, "/stale": stale,
	})

	newModel, _ := m.Update(childrenMsg{path: "/", children: []string{"a", "new"}})
	nm := newModel.(Model)

	if got := nm.nodes["/a"]; got != a {
		t.Fatalf("refreshed /a = %p, want existing node pointer %p", got, a)
	}
	if !a.expanded || !a.loaded || len(a.children) != 1 || a.children[0] != a1 {
		t.Fatalf("/a after refresh = %+v with children %+v, want its expanded loaded subtree retained", a, a.children)
	}
	if got, want := strings.Join(namesOf(visibleNodes(nm.root)), ","), "/,/a,/a/one,/new"; got != want {
		t.Fatalf("visible nodes after refresh = %q, want %q", got, want)
	}
}

func TestUpdateChildrenMsgRefreshRetainsActiveChildWatch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	watched := &node{path: "/watched", name: "watched", depth: 1, parent: root, watching: true}
	root.children = []*node{watched}
	m := newTestModel(root, map[string]*node{"/": root, "/watched": watched})

	newModel, _ := m.Update(childrenMsg{path: "/", children: []string{"watched"}})
	nm := newModel.(Model)

	if got := nm.nodes["/watched"]; got != watched {
		t.Fatalf("refreshed /watched = %p, want existing node pointer %p", got, watched)
	}
	if !watched.watching {
		t.Fatal("/watched.watching = false after parent refresh, want its active child watch retained")
	}
}

func TestUpdateChildrenMsgRefreshRemovesDeletedDescendantsFromNodesMap(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	deleted := &node{path: "/deleted", name: "deleted", depth: 1, parent: root, expanded: true, loaded: true}
	descendant := &node{path: "/deleted/child", name: "child", depth: 2, parent: deleted, expanded: true, loaded: true}
	grandchild := &node{path: "/deleted/child/grandchild", name: "grandchild", depth: 3, parent: descendant}
	descendant.children = []*node{grandchild}
	deleted.children = []*node{descendant}
	root.children = []*node{deleted}
	m := newTestModel(root, map[string]*node{
		"/": root, "/deleted": deleted, "/deleted/child": descendant, "/deleted/child/grandchild": grandchild,
	})

	newModel, _ := m.Update(childrenMsg{path: "/", children: nil})
	nm := newModel.(Model)

	if len(nm.root.children) != 0 {
		t.Fatalf("root.children = %+v, want no children after refresh", nm.root.children)
	}
	for _, path := range []string{"/deleted", "/deleted/child", "/deleted/child/grandchild"} {
		if _, ok := nm.nodes[path]; ok {
			t.Fatalf("deleted subtree path %q still present in nodes map", path)
		}
	}
}

func TestInvalidatedChildrenRefreshRemovesDeletedDescendantsFromNodesMap(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	deleted := &node{path: "/deleted", name: "deleted", depth: 1, parent: root, expanded: true, loaded: true}
	descendant := &node{path: "/deleted/child", name: "child", depth: 2, parent: deleted}
	deleted.children = []*node{descendant}
	root.children = []*node{deleted}
	m := newTestModel(root, map[string]*node{
		"/": root, "/deleted": deleted, "/deleted/child": descendant,
	})

	if cmd := m.invalidateAndMaybeRefetch("/"); cmd == nil {
		t.Fatal("invalidateAndMaybeRefetch on an expanded node returned nil, want a refresh command")
	}
	if len(m.root.children) != 0 {
		t.Fatalf("root.children = %+v immediately after invalidation, want stale rows hidden while loading", m.root.children)
	}

	newModel, _ := m.Update(childrenMsg{path: "/", children: nil})
	nm := newModel.(Model)

	for _, path := range []string{"/deleted", "/deleted/child"} {
		if _, ok := nm.nodes[path]; ok {
			t.Fatalf("deleted subtree path %q still present in nodes map after invalidation refresh", path)
		}
	}
	if _, ok := nm.invalidatedChildren["/"]; ok {
		t.Fatal("saved invalidation children remained after the refresh result, want reconciliation history discarded")
	}
}

func TestOverlappingChildrenRefreshesIgnoreStaleResultAndSettleBoth(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	removed := &node{path: "/removed", name: "removed", parent: root}
	root.children = []*node{removed}
	m := newTestModel(root, map[string]*node{"/": root, "/removed": removed})

	m.invalidateAndMaybeRefetch("/")
	firstID := m.childrenRequestIDs["/"]
	m.invalidateAndMaybeRefetch("/")
	secondID := m.childrenRequestIDs["/"]
	if firstID == secondID {
		t.Fatal("overlapping child refreshes received the same request ID")
	}
	if m.loadingCount != 2 {
		t.Fatalf("loadingCount after two refreshes = %d, want 2", m.loadingCount)
	}

	newModel, _ := m.Update(childrenMsg{path: "/", requestID: secondID, children: nil})
	afterCurrent := newModel.(Model)
	if afterCurrent.loadingCount != 1 {
		t.Fatalf("loadingCount after current refresh = %d, want 1 for the stale request still in flight", afterCurrent.loadingCount)
	}
	if _, ok := afterCurrent.nodes["/removed"]; ok {
		t.Fatal("current refresh did not remove /removed")
	}

	newModel, cmd := afterCurrent.Update(childrenMsg{path: "/", requestID: firstID, children: []string{"removed"}})
	settled := newModel.(Model)
	if cmd != nil {
		t.Fatal("stale children result returned a command, want it ignored")
	}
	if settled.loadingCount != 0 {
		t.Fatalf("loadingCount after both refreshes = %d, want 0", settled.loadingCount)
	}
	if _, ok := settled.nodes["/removed"]; ok {
		t.Fatal("stale children result resurrected /removed")
	}
	if len(settled.root.children) != 0 {
		t.Fatalf("root.children = %+v, want the current empty list retained", settled.root.children)
	}
}

func TestRemoveNodeSubtreeClearsInvalidatedRefreshState(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	parent := &node{path: "/gone", name: "gone", parent: root, expanded: true, loaded: true}
	child := &node{path: "/gone/child", name: "child", parent: parent}
	parent.children = []*node{child}
	root.children = []*node{parent}
	m := newTestModel(root, map[string]*node{"/": root, "/gone": parent, "/gone/child": child})

	m.invalidateAndMaybeRefetch("/gone")
	requestID := m.childrenRequestIDs["/gone"]
	if _, ok := m.invalidatedChildren["/gone"]; !ok {
		t.Fatal("invalidation did not save /gone's previous children")
	}

	m.removeNodeSubtree("/gone")
	if _, ok := m.invalidatedChildren["/gone"]; ok {
		t.Fatal("removed parent retained saved invalidation children")
	}
	if _, ok := m.childrenRequestIDs["/gone"]; ok {
		t.Fatal("removed parent retained its pending refresh identity")
	}

	newModel, _ := m.Update(childrenMsg{path: "/gone", requestID: requestID, children: []string{"child"}})
	nm := newModel.(Model)
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount after removed parent's completion = %d, want 0", nm.loadingCount)
	}
	if _, ok := nm.nodes["/gone"]; ok {
		t.Fatal("stale result recreated a removed parent")
	}
}

func TestUpdateChildrenMsgErrorSurfacesInNodeAndStatusBarWithoutCrash(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loading: true}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	wantErr := errors.New("zk: children of /a: zk: node does not exist")
	newModel, cmd := m.Update(childrenMsg{path: "/a", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childrenMsg error) returned a non-nil cmd, want nil")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0 after the failed fetch settles", nm.loadingCount)
	}
	if nm.nodes["/a"].loading {
		t.Fatal("/a.loading = true after an error, want false")
	}
	if !errors.Is(nm.nodes["/a"].err, wantErr) && nm.nodes["/a"].err.Error() != wantErr.Error() {
		t.Fatalf("/a.err = %v, want it to carry the fetch error", nm.nodes["/a"].err)
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a fetch error, want true (visible in the status bar)")
	}
	if !strings.Contains(nm.status, "/a") {
		t.Fatalf("status = %q, want it to mention the failing path", nm.status)
	}
	// The node must still be present and visible (not dropped from the
	// tree) so its inline error marker can be shown.
	got := namesOf(visibleNodes(nm.root))
	want := []string{"/", "/a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visible nodes after a fetch error = %v, want %v (node kept, not removed)", got, want)
	}
}

func TestUpdateChildrenMsgForUnknownPathIsIgnored(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.status = "connected"

	newModel, cmd := m.Update(childrenMsg{path: "/stale/path", children: []string{"x"}})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childrenMsg for unknown path) returned a non-nil cmd, want nil")
	}
	if nm.status != "connected" {
		t.Fatalf("status changed to %q for a stale/unknown childrenMsg, want it left untouched", nm.status)
	}
	if _, ok := nm.nodes["/stale/path"]; ok {
		t.Fatal("an unknown path must not be inserted into nodes as a side effect")
	}
}

// --- connection events reflected in the status bar -----------------------

func TestUpdateConnEventMsgUpdatesStatusBar(t *testing.T) {
	cases := []struct {
		name      string
		state     zk.State
		wantErr   bool
		wantConn  connIndicator
		wantWords []string
	}{
		{"has session", zk.StateHasSession, false, connOK, []string{"connected"}},
		{"disconnected", zk.StateDisconnected, true, connWarn, []string{"disconnected"}},
		{"connecting", zk.StateConnecting, true, connWarn, []string{"reconnecting"}},
		{"expired", zk.StateExpired, true, connBad, []string{"session", "expired"}},
		{"auth failed", zk.StateAuthFailed, true, connBad, []string{"authentication"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := &node{path: "/", name: "/", expanded: true}
			nodes := map[string]*node{"/": root}
			m := newTestModel(root, nodes)

			newModel, cmd := m.Update(connEventMsg{ev: zk.Event{State: tc.state}})
			nm := newModel.(Model)

			if cmd == nil {
				t.Fatal("Update(connEventMsg) returned a nil cmd, want it to re-arm listenEventsCmd so future events keep being observed")
			}
			if nm.statusErr != tc.wantErr {
				t.Fatalf("statusErr = %v, want %v for state %v", nm.statusErr, tc.wantErr, tc.state)
			}
			if nm.conn != tc.wantConn {
				t.Fatalf("conn = %v, want %v for state %v", nm.conn, tc.wantConn, tc.state)
			}
			for _, w := range tc.wantWords {
				if !strings.Contains(nm.status, w) {
					t.Fatalf("status = %q, want it to mention %q for state %v", nm.status, w, tc.state)
				}
			}
		})
	}
}

// The connection dot is the one piece of status-bar state that must outlive
// whatever transient message is currently shown: it is what lets the user
// notice a reconnect is in progress even after some unrelated operation
// (e.g. a successful children fetch) has overwritten the message text.
func TestConnIndicatorSurvivesALaterStatusMessage(t *testing.T) {
	root := &node{path: "/", name: "/", loading: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	newModel, _ := m.Update(connEventMsg{ev: zk.Event{State: zk.StateDisconnected}})
	m = newModel.(Model)
	if m.conn != connWarn {
		t.Fatalf("conn = %v after a disconnect event, want connWarn", m.conn)
	}

	// An unrelated, successful operation overwrites m.status/m.statusErr, as
	// handleChildren always does — but must not touch m.conn.
	newModel, _ = m.Update(childrenMsg{path: "/", children: []string{"a", "b"}})
	nm := newModel.(Model)

	if !strings.Contains(nm.status, "children") {
		t.Fatalf("status = %q, want the children-fetch message to have replaced it", nm.status)
	}
	if nm.statusErr {
		t.Fatal("statusErr = true after a successful fetch, want false (only the message changed)")
	}
	if nm.conn != connWarn {
		t.Fatalf("conn = %v after an unrelated status message, want it left at connWarn from the earlier disconnect", nm.conn)
	}
}

func TestUpdateConnClosedMsgSetsErrorStatusWithoutCrash(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, cmd := m.Update(connClosedMsg{})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(connClosedMsg) returned a non-nil cmd, want nil (nothing left to listen to)")
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after connClosedMsg, want true")
	}
	if nm.status == "" {
		t.Fatal("status is empty after connClosedMsg, want a visible message")
	}
	if nm.conn != connBad {
		t.Fatalf("conn = %v after connClosedMsg, want connBad", nm.conn)
	}
}

// --- navigation via the reused bubbles/list component ---------------------

func TestUpdateArrowKeysNavigateOnlyVisibleNodes(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true, expanded: false}
	a1 := &node{path: "/a/1", name: "1", parent: a} // hidden: a is collapsed
	a.children = []*node{a1}
	b := &node{path: "/b", name: "b", parent: root, loaded: true}
	root.children = []*node{a, b}
	nodes := map[string]*node{"/": root, "/a": a, "/a/1": a1, "/b": b}

	m := newTestModel(root, nodes)
	m.list.Select(0)
	if got := selectedPath(m); got != "/" {
		t.Fatalf("initial selection = %q, want /", got)
	}

	newModel, _ := m.Update(keyMsg("down"))
	m2 := newModel.(Model)
	if got := selectedPath(m2); got != "/a" {
		t.Fatalf("after one down: selected = %q, want /a", got)
	}

	newModel, _ = m2.Update(keyMsg("down"))
	m3 := newModel.(Model)
	if got := selectedPath(m3); got != "/b" {
		t.Fatalf("after two downs: selected = %q, want /b (/a/1 must stay hidden while /a is collapsed)", got)
	}

	newModel, _ = m3.Update(keyMsg("up"))
	m4 := newModel.(Model)
	if got := selectedPath(m4); got != "/a" {
		t.Fatalf("after moving back up: selected = %q, want /a", got)
	}
}

func TestUpdateGoToRootJumpsBackToRoot(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true}
	b := &node{path: "/b", name: "b", parent: root, loaded: true}
	root.children = []*node{a, b}
	nodes := map[string]*node{"/": root, "/a": a, "/b": b}

	m := newTestModel(root, nodes)
	m.list.Select(2) // /b

	newModel, _ := m.Update(keyMsg("g"))
	nm := newModel.(Model)
	if got := selectedPath(nm); got != "/" {
		t.Fatalf("selected path after pressing 'g' (go to start) = %q, want / (root)", got)
	}
}

func TestUpdateQuitKeyReturnsQuitCmd(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("Update(q) returned a nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Update(q) cmd produced %T, want tea.QuitMsg", cmd())
	}
}

// --- window sizing ---------------------------------------------------------

func TestUpdateWindowSizeSplitsTheTerminalIntoPanels(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	nm := newModel.(Model)

	if nm.width != 80 || nm.height != 24 {
		t.Fatalf("m.width, m.height = %d, %d, want 80, 24", nm.width, nm.height)
	}

	g := nm.geometry()
	// 24 rows less the status bar and the one-line help bar.
	if g.bodyH != 22 {
		t.Fatalf("bodyH = %d, want 22 (status bar and help bar reserved)", g.bodyH)
	}
	// JoinHorizontal pads the shorter column, so any drift here misaligns the
	// whole layout.
	if g.treeW+g.rightW != 80 {
		t.Fatalf("treeW+rightW = %d+%d, want them to add up to the full 80", g.treeW, g.rightW)
	}
	if g.dataH+g.statH != g.bodyH {
		t.Fatalf("dataH+statH = %d+%d, want them to add up to bodyH %d", g.dataH, g.statH, g.bodyH)
	}
	if g.treeW != 28 {
		t.Fatalf("treeW = %d, want 28 (35%% of 80)", g.treeW)
	}
	if nm.list.Width() != g.treeW-2 {
		t.Fatalf("list width = %d, want the tree panel's inner width %d", nm.list.Width(), g.treeW-2)
	}
	if nm.list.Height() != g.bodyH-panelChrome {
		t.Fatalf("list height = %d, want the tree panel's inner height %d", nm.list.Height(), g.bodyH-panelChrome)
	}
}

func TestUpdateWindowSizeNeverNegativeListHeight(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 10, Height: 0})
	nm := newModel.(Model)

	if nm.list.Height() < 0 {
		t.Fatalf("list height = %d, want >= 0 even when the window reports 0 height", nm.list.Height())
	}
}

// --- misc: spinner ticks don't crash and propagate a cmd -------------------

func TestUpdateSpinnerTickDoesNotCrash(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	_, cmd := m.Update(m.spinner.Tick())
	_ = cmd // may legitimately be nil or non-nil depending on the spinner impl; just must not panic
}

// --- child counts -----------------------------------------------------------

// ZooKeeper's Children returns only names, so a listed child's own child count
// arrives separately and is what lets the tree draw it as a leaf.
func TestUpdateChildStatMarksALeaf(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/a", name: "a", parent: root}
	root.children = []*node{child}
	nodes := map[string]*node{"/": root, "/a": child}
	m := newTestModel(root, nodes)

	if got := child.marker(); got != markerCollapsed {
		t.Fatalf("marker before the count arrives = %q, want %q", got, markerCollapsed)
	}

	newModel, cmd := m.Update(childStatMsg{path: "/a", found: true, numChildren: 0})
	nm := newModel.(Model)
	if cmd != nil {
		t.Fatal("Update(childStatMsg) returned a non-nil cmd, want nil")
	}
	if !nm.nodes["/a"].countKnown {
		t.Fatal("countKnown = false after the count arrived, want true")
	}
	if got := nm.nodes["/a"].marker(); got != markerLeaf {
		t.Fatalf("marker after a zero count = %q, want %q", got, markerLeaf)
	}

	newModel, _ = nm.Update(childStatMsg{path: "/a", found: true, numChildren: 4})
	if got := newModel.(Model).nodes["/a"].marker(); got != markerCollapsed {
		t.Fatalf("marker after a non-zero count = %q, want %q", got, markerCollapsed)
	}
}

// The count only picks a marker, so a node that cannot be stat'd is left with
// the neutral marker rather than shouting an error the user cannot act on.
func TestUpdateChildStatErrorIsSwallowed(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/a", name: "a", parent: root}
	root.children = []*node{child}
	nodes := map[string]*node{"/": root, "/a": child}
	m := newTestModel(root, nodes)
	m.status = "sin cambios"

	newModel, cmd := m.Update(childStatMsg{path: "/a", err: errors.New("zk: exists /a: no auth")})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childStatMsg error) returned a non-nil cmd, want nil")
	}
	if nm.nodes["/a"].countKnown {
		t.Fatal("countKnown = true after a failed lookup, want it left unknown")
	}
	if nm.statusErr || nm.status != "sin cambios" {
		t.Fatalf("status = %q (err=%v), want it untouched", nm.status, nm.statusErr)
	}
}

// A count that was in flight while the node got expanded must not override
// what listing its children established.
func TestUpdateChildStatDoesNotOverrideAListedNode(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/a", name: "a", parent: root, loaded: true}
	root.children = []*node{child}
	nodes := map[string]*node{"/": root, "/a": child}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(childStatMsg{path: "/a", found: true, numChildren: 7})
	nm := newModel.(Model)

	if nm.nodes["/a"].countKnown {
		t.Fatal("a stale count was applied to an already-listed node, want it ignored")
	}
	if got := nm.nodes["/a"].marker(); got != markerLeaf {
		t.Fatalf("marker = %q, want %q — the listing said it has no children", got, markerLeaf)
	}
}

func TestUpdateChildStatForUnknownNodeIsIgnored(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, cmd := m.Update(childStatMsg{path: "/se-fue", found: true, numChildren: 2})
	if cmd != nil {
		t.Fatal("Update(childStatMsg) for an unknown node returned a non-nil cmd, want nil")
	}
	if _, ok := newModel.(Model).nodes["/se-fue"]; ok {
		t.Fatal("Update(childStatMsg) registered a node that was not in the tree")
	}
}

// A child can be deleted between its parent being listed and the count lookup
// landing. That is not an error, and must not leave it marked as a leaf.
func TestUpdateChildStatNotFoundLeavesTheMarkerNeutral(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/a", name: "a", parent: root}
	root.children = []*node{child}
	nodes := map[string]*node{"/": root, "/a": child}
	m := newTestModel(root, nodes)

	newModel, cmd := m.Update(childStatMsg{path: "/a", found: false})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childStatMsg not found) returned a non-nil cmd, want nil")
	}
	if nm.nodes["/a"].countKnown {
		t.Fatal("countKnown = true for a node that was not found, want it left unknown")
	}
	if got := nm.nodes["/a"].marker(); got != markerCollapsed {
		t.Fatalf("marker = %q, want the neutral %q — absence is not an empty node", got, markerCollapsed)
	}
	if nm.statusErr {
		t.Error("statusErr = true after a not-found count lookup, want it swallowed")
	}
}

func TestChildStatsCmdBoundsConcurrentLookups(t *testing.T) {
	const limit = 3
	children := make([]*node, limit+5)
	for i := range children {
		children[i] = &node{path: "/child-" + string(rune('a'+i))}
	}

	started := make(chan struct{}, len(children))
	release := make(chan struct{})
	var active, maxActive atomic.Int32
	lookup := func(string) (bool, *zk.Stat, error) {
		current := active.Add(1)
		for {
			seen := maxActive.Load()
			if current <= seen || maxActive.CompareAndSwap(seen, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return true, &zk.Stat{}, nil
	}

	cmd := childStatsCmdWithLookup(children, nil, limit, lookup)
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("childStatsCmdWithLookup() = %T, want tea.BatchMsg", cmd())
	}
	if len(batch) != limit {
		t.Fatalf("initial worker count = %d, want concurrency limit %d", len(batch), limit)
	}

	finished := make(chan tea.Msg, limit)
	for _, worker := range batch {
		go func(worker tea.Cmd) { finished <- worker() }(worker)
	}
	for range limit {
		<-started
	}
	if got := maxActive.Load(); got > limit {
		t.Fatalf("maximum concurrent lookups = %d, want at most %d", got, limit)
	}
	close(release)
	for range limit {
		<-finished
	}
}

func TestModelChildStatSchedulerBoundsOverlappingRefreshPipelinesGlobally(t *testing.T) {
	const limit = 3
	makeChildren := func(prefix string, count int) []*node {
		children := make([]*node, count)
		for i := range children {
			children[i] = &node{path: "/" + prefix + "-" + string(rune('a'+i))}
		}
		return children
	}

	first := makeChildren("first", limit+2)
	second := makeChildren("second", limit+2)
	started := make(chan struct{}, len(first)+len(second))
	release := make(chan struct{})
	var active, maxActive atomic.Int32
	lookup := func(string) (bool, *zk.Stat, error) {
		current := active.Add(1)
		for {
			seen := maxActive.Load()
			if current <= seen || maxActive.CompareAndSwap(seen, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return true, &zk.Stat{}, nil
	}

	m := newTestModel(&node{path: "/", name: "/"}, map[string]*node{})
	m.childStats = newChildStatScheduler(limit, lookup)
	firstGenerations := make(map[string]uint64, len(first))
	secondGenerations := make(map[string]uint64, len(second))
	for _, child := range first {
		firstGenerations[child.path] = 1
	}
	for _, child := range second {
		secondGenerations[child.path] = 2
	}

	firstCmd := m.queueChildStats(first, firstGenerations)
	secondCmd := m.queueChildStats(second, secondGenerations)
	if secondCmd != nil {
		t.Fatal("second overlapping refresh started workers despite the global cap already being full")
	}
	batch, ok := firstCmd().(tea.BatchMsg)
	if !ok || len(batch) != limit {
		t.Fatalf("initial scheduler dispatch = %T with %d workers, want BatchMsg with %d", firstCmd(), len(batch), limit)
	}

	finished := make(chan tea.Msg, limit)
	for _, worker := range batch {
		go func(worker tea.Cmd) { finished <- worker() }(worker)
	}
	for range limit {
		<-started
	}
	if got := maxActive.Load(); got > limit {
		t.Fatalf("maximum active Exists calls across refreshes = %d, want at most %d", got, limit)
	}
	if m.childStats.running != limit || len(m.childStats.pending) != len(first)+len(second)-limit {
		t.Fatalf("scheduler state running=%d pending=%d, want %d running and %d queued", m.childStats.running, len(m.childStats.pending), limit, len(first)+len(second)-limit)
	}

	close(release)
	for range limit {
		<-finished
	}
}

func TestUpdateChildStatStaleGenerationIsIgnored(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/a", name: "a", parent: root}
	root.children = []*node{child}
	m := newTestModel(root, map[string]*node{"/": root, "/a": child})

	m.applyChildren(root, []string{"a"})
	staleGeneration := m.childStatGenerations["/a"]
	m.applyChildren(root, []string{"a"})
	currentGeneration := m.childStatGenerations["/a"]
	if staleGeneration == currentGeneration {
		t.Fatal("successive child listings used the same generation, want stale results distinguishable")
	}

	newModel, _ := m.Update(childStatMsg{path: "/a", generation: staleGeneration, found: true, numChildren: 0})
	nm := newModel.(Model)
	if nm.nodes["/a"].countKnown {
		t.Fatal("a child-stat result from an earlier listing marked the refreshed node as a leaf")
	}

	newModel, _ = nm.Update(childStatMsg{path: "/a", generation: currentGeneration, found: true, numChildren: 0})
	if got := newModel.(Model).nodes["/a"].marker(); got != markerLeaf {
		t.Fatalf("marker after the current generation's zero count = %q, want %q", got, markerLeaf)
	}
}

func TestRemoveNodeSubtreeClearsChildStatGenerations(t *testing.T) {
	root := &node{path: "/", name: "/"}
	deleted := &node{path: "/deleted", name: "deleted", parent: root}
	descendant := &node{path: "/deleted/child", name: "child", parent: deleted}
	kept := &node{path: "/kept", name: "kept", parent: root}
	m := newTestModel(root, map[string]*node{
		"/": root, "/deleted": deleted, "/deleted/child": descendant, "/kept": kept,
	})
	m.childStatGenerations = map[string]uint64{
		"/deleted":       1,
		"/deleted/child": 1,
		"/kept":          2,
	}

	m.removeNodeSubtree("/deleted")

	for _, path := range []string{"/deleted", "/deleted/child"} {
		if _, ok := m.childStatGenerations[path]; ok {
			t.Fatalf("removed subtree generation %q remained cached", path)
		}
	}
	if got := m.childStatGenerations["/kept"]; got != 2 {
		t.Fatalf("unrelated generation = %d, want 2", got)
	}
}
