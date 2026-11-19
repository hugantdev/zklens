package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"

	"github.com/hugantdev/zklens/internal/zk"
)

// refreshFixture builds "/" → "/a" → {"/a/x", "/a/y"} with /a expanded and
// loaded, and "/" selected.
func refreshFixture() (Model, *node, *node) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", depth: 1, parent: root, expanded: true, loaded: true}
	x := &node{path: "/a/x", name: "x", depth: 2, parent: a}
	y := &node{path: "/a/y", name: "y", depth: 2, parent: a}
	a.children = []*node{x, y}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a, "/a/x": x, "/a/y": y}
	m := newTestModel(root, nodes)
	return m, root, a
}

func childPaths(n *node) []string {
	var out []string
	for _, c := range n.children {
		out = append(out, c.path)
	}
	return out
}

func TestRefreshKeyOnTreeRefetchesChildrenAndKeepsThemUntilReply(t *testing.T) {
	m, _, a := refreshFixture()
	m.selectNode(a)

	newModel, cmd := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("Update(r) returned nil cmd, want a children fetch (must run from a tea.Cmd, client is nil)")
	}
	if !a.loading || nm.loadingCount != 1 {
		t.Fatalf("loading=%v loadingCount=%d, want true and 1", a.loading, nm.loadingCount)
	}
	if got := childPaths(a); len(got) != 2 {
		t.Fatalf("children = %v, want the old children kept while refreshing", got)
	}
	if !strings.Contains(nm.status, "/a") || nm.statusErr {
		t.Fatalf("status = %q (err=%v), want a refreshing message for /a", nm.status, nm.statusErr)
	}
}

func TestRefreshShowsExternalChildChanges(t *testing.T) {
	m, _, a := refreshFixture()
	m.selectNode(a)
	newModel, _ := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	// The ensemble now has /a/y and /a/z, but no /a/x.
	reply := childrenMsg{path: "/a", requestID: nm.childrenRequestIDs["/a"], children: []string{"y", "z"}}
	updated, _ := nm.Update(reply)
	um := updated.(Model)

	got := childPaths(a)
	if len(got) != 2 || got[0] != "/a/y" || got[1] != "/a/z" {
		t.Fatalf("children = %v, want [/a/y /a/z]", got)
	}
	if _, ok := um.nodes["/a/x"]; ok {
		t.Fatal("/a/x still registered after it disappeared")
	}
	if a.loading || um.loadingCount != 0 {
		t.Fatalf("loading=%v loadingCount=%d, want settled", a.loading, um.loadingCount)
	}
}

func TestRefreshKeyOnTreeAlsoRefreshesLoadedDetailOfSameNode(t *testing.T) {
	m, _, a := refreshFixture()
	m.detail = detailState{path: "/a", data: []byte("old"), stat: &zk.Stat{}}
	m.selectNode(a)

	newModel, cmd := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("cmd = nil, want children + data fetches")
	}
	if nm.loadingCount != 2 {
		t.Fatalf("loadingCount = %d, want 2 (children + data)", nm.loadingCount)
	}

	updated, _ := nm.Update(dataMsg{path: "/a", requestID: nm.detailRequestID, data: []byte("new"), stat: &zk.Stat{}})
	if got := string(updated.(Model).detail.data); got != "new" {
		t.Fatalf("detail data = %q, want %q", got, "new")
	}
}

func TestRefreshKeyOnTreeLeavesOtherNodesDetailAlone(t *testing.T) {
	m, root, _ := refreshFixture()
	m.detail = detailState{path: "/a", data: []byte("keep")}
	m.selectNode(root)

	newModel, _ := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1 (children only)", nm.loadingCount)
	}
	if nm.detail.loading || string(nm.detail.data) != "keep" {
		t.Fatalf("detail touched: %+v", nm.detail)
	}
}

func TestRefreshKeyOnUnloadedCollapsedNodeDoesNothing(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	b := &node{path: "/b", name: "b", depth: 1, parent: root}
	root.children = []*node{b}
	m := newTestModel(root, map[string]*node{"/": root, "/b": b})
	m.selectNode(b)

	newModel, cmd := m.Update(keyMsg("r"))
	if cmd != nil || newModel.(Model).loadingCount != 0 {
		t.Fatalf("cmd=%v loadingCount=%d, want a no-op", cmd, newModel.(Model).loadingCount)
	}
}

func TestRefreshKeyInDataAndStatFocusRefreshesDetailNode(t *testing.T) {
	for _, focus := range []focusArea{focusData, focusStat} {
		m, root, _ := refreshFixture()
		m.detail = detailState{path: "/a", data: []byte("old"), stat: &zk.Stat{}}
		m.focus = focus
		m.selectNode(root) // the tree cursor is elsewhere; the panel's node wins

		newModel, cmd := m.Update(keyMsg("r"))
		nm := newModel.(Model)

		if cmd == nil {
			t.Fatalf("focus %v: cmd = nil, want fetches", focus)
		}
		if nm.loadingCount != 2 || !nm.nodes["/a"].loading || !nm.detail.loading {
			t.Fatalf("focus %v: loadingCount=%d children.loading=%v detail.loading=%v, want 2/true/true",
				focus, nm.loadingCount, nm.nodes["/a"].loading, nm.detail.loading)
		}
		if root.loading {
			t.Fatalf("focus %v: the tree-selected node was refreshed instead of the panel's node", focus)
		}
	}
}

func TestRefreshKeyInPanelWithNoDetailIsNoop(t *testing.T) {
	m, _, _ := refreshFixture()
	m.focus = focusData

	newModel, cmd := m.Update(keyMsg("r"))
	if cmd != nil || newModel.(Model).loadingCount != 0 {
		t.Fatal("r with nothing loaded in the panel must be a no-op")
	}
}

func TestRefreshErrorIsShownAndTreeStaysIntact(t *testing.T) {
	m, _, a := refreshFixture()
	m.selectNode(a)
	newModel, _ := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	updated, _ := nm.Update(childrenMsg{
		path: "/a", requestID: nm.childrenRequestIDs["/a"], err: errors.New("connection loss"),
	})
	um := updated.(Model)

	if !um.statusErr || !strings.Contains(um.status, "connection loss") {
		t.Fatalf("status = %q (err=%v), want the ZooKeeper error in the status bar", um.status, um.statusErr)
	}
	if got := childPaths(a); len(got) != 2 {
		t.Fatalf("children = %v, want the old children preserved after a failed refresh", got)
	}
	if a.loading || um.loadingCount != 0 {
		t.Fatalf("loading=%v loadingCount=%d, want settled", a.loading, um.loadingCount)
	}
	if n := len(um.list.Items()); n != 4 {
		t.Fatalf("visible items = %d, want 4 (tree intact)", n)
	}
}

func TestRefreshDataErrorIsShownInStatusBar(t *testing.T) {
	m, _, a := refreshFixture()
	m.detail = detailState{path: "/a", data: []byte("old")}
	m.selectNode(a)
	newModel, _ := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	updated, _ := nm.Update(dataMsg{path: "/a", requestID: nm.detailRequestID, err: errors.New("no node")})
	um := updated.(Model)

	if !um.statusErr || !strings.Contains(um.status, "no node") {
		t.Fatalf("status = %q (err=%v), want the error", um.status, um.statusErr)
	}
}

func TestRefreshBindingIsInHelp(t *testing.T) {
	has := func(bs []key.Binding) bool {
		for _, b := range bs {
			if b.Help().Key == "r" && b.Help().Desc == "refresh" {
				return true
			}
		}
		return false
	}
	for _, focus := range []focusArea{focusTree, focusData, focusStat} {
		if !has(appKeyMap{mode: modeTree, focus: focus}.ShortHelp()) {
			t.Errorf("ShortHelp for focus %v lacks r/refresh", focus)
		}
	}
	found := false
	for _, row := range (appKeyMap{mode: modeTree}).FullHelp() {
		found = found || has(row)
	}
	if !found {
		t.Error("FullHelp lacks r/refresh")
	}
}
