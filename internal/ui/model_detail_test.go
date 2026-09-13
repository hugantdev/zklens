package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hugantdev/zklens/internal/zk"
)

// --- opening / closing the detail panel ------------------------------------

func TestUpdateTabOpensDetailAndReturnsFetchCmdWithoutBlocking(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a}

	m := newTestModel(root, nodes)
	m.list.Select(1) // /a

	newModel, cmd := m.Update(keyMsg("tab"))
	nm := newModel.(Model)

	if nm.focus != focusData {
		t.Fatalf("focus after tab = %v, want focusData", nm.mode)
	}
	if nm.detail.path != "/a" {
		t.Fatalf("detail.path = %q, want /a", nm.detail.path)
	}
	if !nm.detail.loading {
		t.Fatal("detail.loading = false right after opening, want true")
	}
	if cmd == nil {
		t.Fatal("Update(tab) returned a nil cmd, want a fetch command (Get is only ever called from a tea.Cmd)")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
	// m.client is nil in this test model. If openDetail had called
	// client.Get synchronously instead of deferring to fetchDataCmd, this
	// test would already have panicked above with a nil pointer dereference.
}

func TestUpdateTabWithNoSelectionDoesNotPanic(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.SetItems(nil) // force "no selection"

	newModel, cmd := m.Update(keyMsg("tab"))
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(tab) with nothing selected returned a non-nil cmd, want nil")
	}
	if nm.mode != modeTree {
		t.Fatalf("mode after tab with no selection = %v, want modeTree (unchanged)", nm.mode)
	}
}

func TestUpdateTabThenEscReturnsToTreePreservingState(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true, expanded: true}
	a1 := &node{path: "/a/1", name: "1", parent: a, loaded: true}
	a.children = []*node{a1}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a, "/a/1": a1}

	m := newTestModel(root, nodes)
	m.list.Select(1) // /a

	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)
	if m2.focus != focusData {
		t.Fatalf("focus after tab = %v, want focusData", m2.mode)
	}

	newModel, cmd := m2.Update(keyMsg("esc"))
	m3 := newModel.(Model)
	if m3.mode != modeTree {
		t.Fatalf("mode after esc = %v, want modeTree", m3.mode)
	}
	if cmd != nil {
		t.Fatal("Update(esc) returned a non-nil cmd, want nil")
	}
	if got := selectedPath(m3); got != "/a" {
		t.Fatalf("selected path after returning from detail = %q, want /a (selection preserved)", got)
	}
	if !m3.nodes["/a"].expanded {
		t.Fatal("/a.expanded = false after returning from detail, want true (tree state untouched)")
	}
	got := namesOf(visibleNodes(m3.root))
	want := []string{"/", "/a", "/a/1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("visible nodes after returning from detail = %v, want %v (tree untouched)", got, want)
	}
}

func TestUpdateTabAgainFromDetailAlsoReturnsToTree(t *testing.T) {
	// keyDetail and keyBack are both bound to "tab"; from inside the detail
	// pane, tab must behave like back-to-tree, not "open detail again".
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)
	if m2.focus != focusData {
		t.Fatalf("focus after first tab = %v, want focusData", m2.mode)
	}

	newModel, cmd := m2.Update(keyMsg("tab"))
	m3 := newModel.(Model)
	if m3.mode != modeTree {
		t.Fatalf("mode after second tab = %v, want modeTree", m3.mode)
	}
	if cmd != nil {
		t.Fatal("Update(tab) leaving the detail pane returned a non-nil cmd, want nil")
	}
}

func TestUpdateReopeningDetailAlwaysRefetchesNoCache(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, cmd1 := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)
	if cmd1 == nil {
		t.Fatal("first Update(tab) returned a nil cmd, want a fetch command")
	}

	newModel, _ = m2.Update(dataMsg{path: "/", requestID: m2.detailRequestID, data: []byte("v1"), stat: &zk.Stat{Version: 1}})
	m3 := newModel.(Model)
	if m3.detail.loading {
		t.Fatal("detail.loading = true after dataMsg settled, want false")
	}

	newModel, _ = m3.Update(keyMsg("esc"))
	m4 := newModel.(Model)

	newModel, cmd2 := m4.Update(keyMsg("tab"))
	m5 := newModel.(Model)
	if cmd2 == nil {
		t.Fatal("re-opening the same node's detail returned a nil cmd, want a fresh fetch (no caching, unlike Children)")
	}
	if !m5.detail.loading {
		t.Fatal("detail.loading = false right after re-opening, want true (must re-fetch, not reuse the previous snapshot)")
	}
}

// --- dataMsg handling -------------------------------------------------------

func TestUpdateDataMsgSuccessPopulatesDetailAndStatus(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)

	stat := &zk.Stat{Version: 3}
	newModel, cmd := m2.Update(dataMsg{path: "/", requestID: m2.detailRequestID, data: []byte("payload"), stat: stat})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(dataMsg success) returned a non-nil cmd, want nil")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0 after the fetch settles", nm.loadingCount)
	}
	if nm.detail.loading {
		t.Fatal("detail.loading = true after dataMsg, want false")
	}
	if string(nm.detail.data) != "payload" {
		t.Fatalf("detail.data = %q, want %q", nm.detail.data, "payload")
	}
	if nm.detail.stat != stat {
		t.Fatal("detail.stat not set from the message")
	}
	if nm.detail.err != nil {
		t.Fatalf("detail.err = %v, want nil", nm.detail.err)
	}
	if nm.statusErr {
		t.Fatal("statusErr = true after a successful read, want false")
	}
	if !strings.Contains(nm.status, "/") {
		t.Fatalf("status = %q, want it to mention the path", nm.status)
	}
}

func TestUpdateDataMsgErrorSurfacesInDetailAndStatusBarWithoutCrash(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)

	wantErr := errors.New("zk: get /: zk: not authenticated")
	newModel, cmd := m2.Update(dataMsg{path: "/", requestID: m2.detailRequestID, err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(dataMsg error) returned a non-nil cmd, want nil")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0 after the failed fetch settles", nm.loadingCount)
	}
	if nm.detail.loading {
		t.Fatal("detail.loading = true after an error, want false")
	}
	if !errors.Is(nm.detail.err, wantErr) {
		t.Fatalf("detail.err = %v, want it to carry the fetch error", nm.detail.err)
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a read error, want true (visible in the status bar)")
	}
	if !strings.Contains(nm.status, "/") {
		t.Fatalf("status = %q, want it to mention the failing path", nm.status)
	}
	// The detail pane itself must render the error without panicking.
	rendered := renderDetail(nm.detail)
	if !strings.Contains(rendered, "not authenticated") {
		t.Fatalf("renderDetail after error = %q, want the error message visible in the panel", rendered)
	}
}

func TestUpdateDataMsgForStalePathIsIgnored(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a}

	m := newTestModel(root, nodes)
	m.list.Select(0) // "/"

	// Open detail on "/", then go back and open detail on "/a" before the
	// first fetch (for "/") comes back.
	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)
	newModel, _ = m2.Update(keyMsg("esc"))
	m3 := newModel.(Model)
	m3.list.Select(1) // "/a"
	newModel, _ = m3.Update(keyMsg("tab"))
	m4 := newModel.(Model)

	if m4.detail.path != "/a" {
		t.Fatalf("detail.path = %q, want /a (the second, currently-open node)", m4.detail.path)
	}

	// The stale response for "/" arrives late.
	newModel, cmd := m4.Update(dataMsg{path: "/", requestID: m2.detailRequestID, data: []byte("stale"), stat: &zk.Stat{}})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(stale dataMsg) returned a non-nil cmd, want nil")
	}
	if nm.detail.path != "/a" {
		t.Fatalf("detail.path = %q after a stale dataMsg for /, want it to remain /a", nm.detail.path)
	}
	if string(nm.detail.data) == "stale" {
		t.Fatal("detail.data was overwritten by a stale response for a different path")
	}
}

func TestDetailFetchesSettleAndIgnoreReorderedDifferentPathResponses(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true}
	root.children = []*node{a}
	m := newTestModel(root, map[string]*node{"/": root, "/a": a})

	newModel, _ := m.Update(keyMsg("tab"))
	first := newModel.(Model)
	firstID := first.detailRequestID
	newModel, _ = first.Update(keyMsg("esc"))
	second := newModel.(Model)
	second.list.Select(1)
	newModel, _ = second.Update(keyMsg("tab"))
	current := newModel.(Model)
	currentID := current.detailRequestID

	if current.loadingCount != 2 {
		t.Fatalf("loadingCount after two outstanding detail fetches = %d, want 2", current.loadingCount)
	}
	if firstID == currentID {
		t.Fatal("different detail fetches received the same request ID")
	}

	newModel, _ = current.Update(dataMsg{path: "/", requestID: firstID, data: []byte("stale")})
	afterStale := newModel.(Model)
	if afterStale.loadingCount != 1 {
		t.Fatalf("loadingCount after stale completion = %d, want 1", afterStale.loadingCount)
	}
	if afterStale.detail.path != "/a" || !afterStale.detail.loading {
		t.Fatalf("stale / response changed current detail to path=%q loading=%t, want /a still loading", afterStale.detail.path, afterStale.detail.loading)
	}

	newModel, _ = afterStale.Update(dataMsg{path: "/a", requestID: currentID, data: []byte("current"), stat: &zk.Stat{Version: 2}})
	settled := newModel.(Model)
	if settled.loadingCount != 0 {
		t.Fatalf("loadingCount after both completions = %d, want 0", settled.loadingCount)
	}
	if got := string(settled.detail.data); got != "current" {
		t.Fatalf("detail.data = %q, want current response", got)
	}
}

func TestPostSaveDetailReloadWinsOverOlderSamePathFetch(t *testing.T) {
	root := &node{path: "/n", name: "n", expanded: true, loaded: true}
	m := newTestModel(root, map[string]*node{"/n": root})

	newModel, _ := m.Update(keyMsg("tab"))
	first := newModel.(Model)
	firstID := first.detailRequestID

	// Model the Set request that is completing now. handleSetResult settles
	// that operation and starts a second detail Get for the same path.
	first.loadingCount++
	newModel, _ = first.Update(setResultMsg{path: "/n"})
	reloading := newModel.(Model)
	reloadID := reloading.detailRequestID
	if reloadID == firstID {
		t.Fatal("post-save reload reused the initial detail request ID")
	}
	if reloading.loadingCount != 2 {
		t.Fatalf("loadingCount with two outstanding Gets = %d, want 2", reloading.loadingCount)
	}

	newModel, _ = reloading.Update(dataMsg{path: "/n", requestID: reloadID, data: []byte("new"), stat: &zk.Stat{Version: 2}})
	afterReload := newModel.(Model)
	newModel, _ = afterReload.Update(dataMsg{path: "/n", requestID: firstID, data: []byte("old"), stat: &zk.Stat{Version: 1}})
	settled := newModel.(Model)

	if settled.loadingCount != 0 {
		t.Fatalf("loadingCount after reordered same-path responses = %d, want 0", settled.loadingCount)
	}
	if got := string(settled.detail.data); got != "new" {
		t.Fatalf("detail.data = %q, want post-save response to win", got)
	}
}

func TestStatRefreshDoesNotOverwriteNewerDetailStat(t *testing.T) {
	newer := &zk.Stat{Mzxid: 20, Pzxid: 21, Version: 4, Cversion: 5, Aversion: 2}
	m := newTestModel(&node{path: "/n", name: "n"}, map[string]*node{})
	m.detail = detailState{path: "/n", stat: newer}
	m.loadingCount = 1

	newModel, _ := m.Update(statRefreshedMsg{
		path:   "/n",
		exists: true,
		stat:   &zk.Stat{Mzxid: 19, Pzxid: 20, Version: 3, Cversion: 4, Aversion: 1},
	})
	nm := newModel.(Model)

	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount after stat refresh = %d, want 0", nm.loadingCount)
	}
	if nm.detail.stat != newer {
		t.Fatal("older stat refresh overwrote the newer detail Stat")
	}
}

func TestStatRefreshAppliesRecreatedZnodeStatWithLowerVersions(t *testing.T) {
	old := &zk.Stat{Czxid: 10, Mzxid: 20, Pzxid: 20, Version: 5, Cversion: 3, Aversion: 1}
	recreated := &zk.Stat{Czxid: 30, Mzxid: 30, Pzxid: 30, Version: 0, Cversion: 0, Aversion: 0}
	m := newTestModel(&node{path: "/n", name: "n"}, map[string]*node{})
	m.detail = detailState{path: "/n", stat: old}
	m.loadingCount = 1

	newModel, _ := m.Update(statRefreshedMsg{
		path:   "/n",
		exists: true,
		stat:   recreated,
	})
	nm := newModel.(Model)

	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount after stat refresh = %d, want 0", nm.loadingCount)
	}
	if nm.detail.stat != recreated {
		t.Fatal("stat refresh of a recreated znode was discarded as stale")
	}
}

// --- detail-mode key routing -------------------------------------------------

func TestUpdateDetailModeQuitStillQuits(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)

	_, cmd := m2.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("Update(q) in detail mode returned a nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Update(q) in detail mode produced %T, want tea.QuitMsg", cmd())
	}
}

func TestUpdateDetailModeOtherKeysScrollViewportNotTree(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	a := &node{path: "/a", name: "a", parent: root, loaded: true}
	root.children = []*node{a}
	nodes := map[string]*node{"/": root, "/a": a}

	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, _ := m.Update(keyMsg("tab"))
	m2 := newModel.(Model)

	// Feed enough content that the viewport can actually scroll.
	var long strings.Builder
	for i := 0; i < 100; i++ {
		long.WriteString("line\n")
	}
	m2.dataVP.SetContent(long.String())
	m2.dataVP.GotoTop()

	newModel, _ = m2.Update(keyMsg("down"))
	m3 := newModel.(Model)

	if m3.focus != focusData {
		t.Fatalf("focus after pressing down in the data panel = %v, want focusData (unchanged)", m3.mode)
	}
	if selected := selectedPath(m3); selected != "/" {
		t.Fatalf("tree selection changed to %q while in detail mode, want it untouched (/)", selected)
	}
	if m3.dataVP.YOffset == 0 {
		t.Fatal("viewport.YOffset did not change after pressing down in detail mode, want it to scroll")
	}
}

// --- window sizing also resizes both detail viewports -----------------------

func TestUpdateWindowSizeResizesBothDetailViewports(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	nm := newModel.(Model)

	g := nm.geometry()
	if g.treeW != 35 || g.rightW != 65 {
		t.Fatalf("treeW, rightW = %d, %d, want 35, 65", g.treeW, g.rightW)
	}
	// Both viewports sit in the right column, so they share its inner width.
	if nm.dataVP.Width != g.rightW-2 || nm.statVP.Width != g.rightW-2 {
		t.Fatalf("dataVP/statVP widths = %d, %d, want both %d", nm.dataVP.Width, nm.statVP.Width, g.rightW-2)
	}
	if nm.dataVP.Height != g.dataH-panelChrome {
		t.Fatalf("dataVP.Height = %d, want %d", nm.dataVP.Height, g.dataH-panelChrome)
	}
	// The Stat panel is sized to fit all 11 Stat rows without scrolling.
	if nm.statVP.Height != 11 {
		t.Fatalf("statVP.Height = %d, want 11 (one row per Stat field)", nm.statVP.Height)
	}
}
