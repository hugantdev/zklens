package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

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

func TestRefreshKeyOnUnloadedCollapsedNodeFetchesChildrenWithoutExpanding(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	b := &node{path: "/b", name: "b", depth: 1, parent: root}
	root.children = []*node{b}
	m := newTestModel(root, map[string]*node{"/": root, "/b": b})
	m.selectNode(b)

	newModel, cmd := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	if cmd == nil || nm.loadingCount != 1 || !b.loading {
		t.Fatalf("cmd=%v loadingCount=%d loading=%v, want a children fetch", cmd, nm.loadingCount, b.loading)
	}
	if b.expanded {
		t.Fatal("refresh must not expand the node")
	}

	updated, _ := nm.Update(childrenMsg{path: "/b", requestID: nm.childrenRequestIDs["/b"], children: []string{"c"}})
	um := updated.(Model)
	if !b.loaded || b.expanded || len(b.children) != 1 || um.loadingCount != 0 {
		t.Fatalf("loaded=%v expanded=%v children=%v loadingCount=%d", b.loaded, b.expanded, childPaths(b), um.loadingCount)
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

// mixedRefresh starts a refresh of /a with its detail loaded, then delivers
// the children and data results in the requested order.
func mixedRefresh(t *testing.T, childrenErr, dataErr error, childrenFirst bool) Model {
	t.Helper()
	m, _, a := refreshFixture()
	m.detail = detailState{path: "/a", data: []byte("old"), stat: &zk.Stat{}}
	m.selectNode(a)
	newModel, _ := m.Update(keyMsg("r"))
	nm := newModel.(Model)

	children := childrenMsg{path: "/a", requestID: nm.childrenRequestIDs["/a"], children: []string{"x", "y"}, err: childrenErr}
	data := dataMsg{path: "/a", requestID: nm.detailRequestID, data: []byte("new"), stat: &zk.Stat{}, err: dataErr}
	order := []interface{}{children, data}
	if !childrenFirst {
		order = []interface{}{data, children}
	}
	var cur tea.Model = nm
	for _, msg := range order {
		cur, _ = cur.Update(msg)
	}
	return cur.(Model)
}

func TestRefreshKeepsErrorAcrossBothCompletions(t *testing.T) {
	cases := []struct {
		name          string
		childrenErr   error
		dataErr       error
		wantInStatus  string
		childrenFirst bool
	}{
		{"children ok, data err, children first", nil, errors.New("data boom"), "data boom", true},
		{"children ok, data err, data first", nil, errors.New("data boom"), "data boom", false},
		{"children err, data ok, children first", errors.New("list boom"), nil, "list boom", true},
		{"children err, data ok, data first", errors.New("list boom"), nil, "list boom", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			um := mixedRefresh(t, tc.childrenErr, tc.dataErr, tc.childrenFirst)
			if !um.statusErr || !strings.Contains(um.status, tc.wantInStatus) {
				t.Fatalf("status = %q (err=%v), want the %q error to stay", um.status, um.statusErr, tc.wantInStatus)
			}
			if um.loadingCount != 0 || um.refreshErr != "" {
				t.Fatalf("loadingCount=%d refreshErr=%q, want settled and cleared", um.loadingCount, um.refreshErr)
			}
		})
	}
}

func TestRefreshBothSucceedClearsError(t *testing.T) {
	for _, first := range []bool{true, false} {
		um := mixedRefresh(t, nil, nil, first)
		if um.statusErr {
			t.Fatalf("childrenFirst=%v: status = %q flagged as error", first, um.status)
		}
	}
}

func TestRefreshErrorDoesNotLeakIntoNextRefresh(t *testing.T) {
	um := mixedRefresh(t, errors.New("list boom"), nil, true)
	newModel, _ := um.Update(keyMsg("r"))
	nm := newModel.(Model)
	var cur tea.Model = nm
	cur, _ = cur.Update(childrenMsg{path: "/a", requestID: nm.childrenRequestIDs["/a"], children: []string{"x"}})
	cur, _ = cur.Update(dataMsg{path: "/a", requestID: nm.detailRequestID, data: []byte("n"), stat: &zk.Stat{}})
	if cur.(Model).statusErr {
		t.Fatalf("status = %q, stale error leaked into a clean refresh", cur.(Model).status)
	}
}

// interleave refreshes /a (detail loaded) after starting an independent read
// of /b (expanding it), then delivers the results in the given order. Each
// step is "bOK", "bErr", "aChildrenOK", "aChildrenErr", "aDataOK", "aDataErr".
func interleave(t *testing.T, steps ...string) Model {
	t.Helper()
	m, root, a := refreshFixture()
	b := &node{path: "/b", name: "b", depth: 1, parent: root}
	root.children = append(root.children, b)
	m.nodes["/b"] = b
	m.refreshItems()
	m.detail = detailState{path: "/a", data: []byte("old"), stat: &zk.Stat{}}

	m.selectNode(b)
	var cur tea.Model = m
	nm, _ := cur.Update(keyMsg("right")) // expand B: independent children read
	cur = nm
	bID := cur.(Model).childrenRequestIDs["/b"]
	if bID == 0 {
		t.Fatal("expanding /b did not start an independent children read")
	}
	cm := cur.(Model)
	cm.selectNode(a)
	nm, _ = cm.Update(keyMsg("r"))
	cur = nm
	aChildrenID := cur.(Model).childrenRequestIDs["/a"]
	dataID := cur.(Model).detailRequestID

	for _, step := range steps {
		var msg tea.Msg
		switch step {
		case "bOK":
			msg = childrenMsg{path: "/b", requestID: bID, children: []string{"k"}}
		case "bErr":
			msg = childrenMsg{path: "/b", requestID: bID, err: errors.New("b boom")}
		case "aChildrenOK":
			msg = childrenMsg{path: "/a", requestID: aChildrenID, children: []string{"x", "y"}}
		case "aChildrenErr":
			msg = childrenMsg{path: "/a", requestID: aChildrenID, err: errors.New("a list boom")}
		case "aDataOK":
			msg = dataMsg{path: "/a", requestID: dataID, data: []byte("new"), stat: &zk.Stat{}}
		case "aDataErr":
			msg = dataMsg{path: "/a", requestID: dataID, err: errors.New("a data boom")}
		default:
			t.Fatalf("unknown step %q", step)
		}
		cur, _ = cur.Update(msg)
	}
	return cur.(Model)
}

func TestRefreshErrorSurvivesIndependentSuccessBetweenTrackedReads(t *testing.T) {
	um := interleave(t, "aDataErr", "bOK", "aChildrenOK")
	if !um.statusErr || !strings.Contains(um.status, "a data boom") {
		t.Fatalf("status = %q (err=%v), want the refresh's data error restored", um.status, um.statusErr)
	}
	if um.loadingCount != 0 || um.refreshErr != "" {
		t.Fatalf("loadingCount=%d refreshErr=%q, want settled", um.loadingCount, um.refreshErr)
	}
}

func TestRefreshErrorSurvivesIndependentSuccessInEveryPosition(t *testing.T) {
	orders := [][]string{
		{"bOK", "aDataErr", "aChildrenOK"},
		{"aDataErr", "bOK", "aChildrenOK"},
		{"aDataErr", "aChildrenOK", "bOK"}, // after settling: last message wins, see below
		{"bOK", "aChildrenErr", "aDataOK"},
		{"aChildrenErr", "bOK", "aDataOK"},
		{"bErr", "aDataErr", "aChildrenOK"},
		{"aDataErr", "bErr", "aChildrenOK"},
		{"aChildrenErr", "bErr", "aDataOK"},
	}
	for _, steps := range orders {
		um := interleave(t, steps...)
		last := steps[len(steps)-1]
		if last == "bOK" {
			// The refresh had fully settled before the unrelated read ended;
			// ordinary last-message-wins applies.
			continue
		}
		if !um.statusErr || !(strings.Contains(um.status, "a data boom") || strings.Contains(um.status, "a list boom")) {
			t.Errorf("%v: status = %q (err=%v), want the refresh error to be shown", steps, um.status, um.statusErr)
		}
		if um.loadingCount != 0 || um.refreshErr != "" {
			t.Errorf("%v: loadingCount=%d refreshErr=%q, want settled", steps, um.loadingCount, um.refreshErr)
		}
	}
}

func TestRefreshCleanWithIndependentReadsShowsNoError(t *testing.T) {
	for _, steps := range [][]string{
		{"bOK", "aChildrenOK", "aDataOK"},
		{"aChildrenOK", "bOK", "aDataOK"},
		{"aDataOK", "bErr", "aChildrenOK"}, // independent error is overwritten by later tracked success: last wins
	} {
		um := interleave(t, steps...)
		if um.statusErr {
			t.Errorf("%v: status = %q flagged as error, want clean", steps, um.status)
		}
	}
}
