package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hugantdev/zklens/internal/zk"
)

// --- tree children-watch: basic toggle -------------------------------------

func TestUpdateWatchKeyOnTreeNodeArmsWithoutBlocking(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, cmd := m.Update(keyMsg("w"))
	nm := newModel.(Model)

	if !root.watching {
		t.Fatal("node.watching = false after 'w', want true")
	}
	if cmd == nil {
		t.Fatal("Update(w) returned a nil cmd, want a watch-arm command (ChildrenW must only run from a tea.Cmd)")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
	// m.client is nil: a synchronous client.ChildrenW call inside
	// toggleChildWatch would already have panicked above.
}

func TestUpdateWatchKeyTogglesOffWithoutTouchingNetwork(t *testing.T) {
	root := &node{path: "/n", name: "n", watching: true}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, cmd := m.Update(keyMsg("w"))
	nm := newModel.(Model)

	if root.watching {
		t.Fatal("node.watching = true after toggling off, want false")
	}
	if cmd != nil {
		t.Fatal("Update(w) toggling off returned a non-nil cmd, want nil")
	}
	if nm.statusErr {
		t.Fatal("statusErr = true after a plain toggle-off, want false")
	}
	if !strings.Contains(nm.status, "/n") {
		t.Fatalf("status = %q, want it to mention the path", nm.status)
	}
}

func TestWatchListenerCommandsStopPromptlyWhenCancelled(t *testing.T) {
	tests := []struct {
		name string
		cmd  func(<-chan struct{}) tea.Cmd
	}{
		{
			name: "child",
			cmd: func(done <-chan struct{}) tea.Cmd {
				return listenChildWatchCmd("/n", 1, make(chan zk.Event), done)
			},
		},
		{
			name: "data",
			cmd: func(done <-chan struct{}) tea.Cmd {
				return listenDataWatchCmd("/n", 1, make(chan zk.Event), done)
			},
		},
		{
			name: "stat",
			cmd: func(done <-chan struct{}) tea.Cmd {
				return listenStatWatchCmd("/n", 1, make(chan zk.Event), done)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{})
			result := make(chan tea.Msg, 1)
			go func() { result <- tt.cmd(done)() }()
			close(done)

			select {
			case msg := <-result:
				if msg != nil {
					t.Fatalf("cancelled listener returned %T, want nil", msg)
				}
			case <-time.After(time.Second):
				t.Fatal("listener did not stop after local cancellation")
			}
		})
	}
}

func TestChildWatchGenerationRejectsOldToggleMessages(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	m := newTestModel(root, map[string]*node{"/n": root})
	m.list.Select(0)

	newModel, _ := m.Update(keyMsg("w"))
	first := newModel.(Model)
	firstGeneration := root.watchGeneration
	firstCancel := root.watchCancel
	newModel, _ = first.Update(keyMsg("w"))
	off := newModel.(Model)
	select {
	case <-firstCancel:
	default:
		t.Fatal("turning a child watch off did not cancel its listener")
	}
	newModel, _ = off.Update(keyMsg("w"))
	current := newModel.(Model)
	currentGeneration := root.watchGeneration
	if currentGeneration == firstGeneration {
		t.Fatal("re-enabled child watch reused the old generation")
	}

	newModel, cmd := current.Update(childWatchArmedMsg{path: "/n", generation: firstGeneration, children: []string{"stale"}})
	stale := newModel.(Model)
	if cmd != nil || len(root.children) != 0 {
		t.Fatal("old child arm result was applied after a later toggle")
	}
	newModel, cmd = stale.Update(childWatchFiredMsg{path: "/n", generation: firstGeneration})
	if cmd != nil {
		t.Fatal("old child fire re-armed the newer watch")
	}
}

func TestChildWatchGenerationDoesNotCollideAfterDeleteAndRecreate(t *testing.T) {
	parent := &node{path: "/", name: "/", expanded: true, loaded: true}
	original := &node{path: "/n", name: "n", parent: parent}
	parent.children = []*node{original}
	m := newTestModel(parent, map[string]*node{"/": parent, "/n": original})

	m, _ = m.startChildWatch(original)
	oldGeneration := original.watchGeneration
	m.removeNodeSubtree("/n") // local delete/reconciliation removal

	recreated := &node{path: "/n", name: "n", parent: parent}
	parent.children = []*node{recreated}
	m.nodes["/n"] = recreated
	m, _ = m.startChildWatch(recreated)
	if recreated.watchGeneration == oldGeneration {
		t.Fatal("recreated node reused the deleted node's watch generation")
	}

	newModel, cmd := m.Update(childWatchArmedMsg{path: "/n", generation: oldGeneration, children: []string{"stale"}})
	stale := newModel.(Model)
	if cmd != nil || len(recreated.children) != 0 {
		t.Fatal("old arm result updated or listened on the recreated node")
	}
	newModel, cmd = stale.Update(childWatchFiredMsg{path: "/n", generation: oldGeneration})
	if cmd != nil || newModel.(Model).loadingCount != 1 {
		t.Fatal("old fire re-armed the recreated node's watch")
	}
}

// --- tree children-watch: armed message -------------------------------------

func TestUpdateChildWatchArmedSuccessAppliesChildrenAndListensAgain(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, watching: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	events := make(chan zk.Event)
	newModel, cmd := m.Update(childWatchArmedMsg{path: "/", children: []string{"a", "b"}, events: events})
	nm := newModel.(Model)

	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
	if !root.watching {
		t.Fatal("node.watching = false after a successful arm, want true (still active)")
	}
	if len(root.children) != 2 || root.children[0].name != "a" || root.children[1].name != "b" {
		t.Fatalf("root.children = %+v, want [a b]", root.children)
	}
	if !strings.Contains(nm.status, "watch active") {
		t.Fatalf("status = %q, want it to mention the watch is active", nm.status)
	}
	if nm.statusErr {
		t.Fatal("statusErr = true after a successful arm, want false")
	}
	if cmd == nil {
		t.Fatal("Update(childWatchArmedMsg success) returned a nil cmd, want a listen command to keep observing")
	}
}

func TestUpdateChildWatchArmedErrorStopsWatchWithoutInfiniteRetry(t *testing.T) {
	root := &node{path: "/gone", name: "gone", watching: true}
	nodes := map[string]*node{"/gone": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	wantErr := errors.New("zk: children of /gone: zk: node does not exist")
	newModel, cmd := m.Update(childWatchArmedMsg{path: "/gone", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childWatchArmedMsg error) returned a non-nil cmd, want nil — must not retry in a loop once arming fails")
	}
	if root.watching {
		t.Fatal("node.watching = true after a failed arm, want false (watch stopped itself)")
	}
	if !errors.Is(root.err, wantErr) {
		t.Fatalf("node.err = %v, want it to carry the failure", root.err)
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a failed watch arm, want true")
	}
	if !strings.Contains(nm.status, "/gone") {
		t.Fatalf("status = %q, want it to mention the path", nm.status)
	}
}

// Attention point 2 (part A): the user disables the watch while an arm/
// re-arm request is already in flight — the eventual armed message must be
// discarded, not resurrect a watch the user already turned off.
func TestUpdateChildWatchArmedResultDiscardedIfTurnedOffMeanwhile(t *testing.T) {
	root := &node{path: "/n", name: "n", watching: false} // user already toggled off
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1
	m.status = "watch disabled: /n"

	events := make(chan zk.Event)
	newModel, cmd := m.Update(childWatchArmedMsg{path: "/n", children: []string{"late-child"}, events: events})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childWatchArmedMsg) for a node the user already turned watching off = non-nil cmd, want nil (must not start listening on an unwanted watch)")
	}
	if root.watching {
		t.Fatal("node.watching = true after a stale arm result, want it to stay false")
	}
	if len(root.children) != 0 {
		t.Fatalf("root.children = %+v, want untouched (the stale arm result must not apply)", root.children)
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0 (the in-flight arm's own loading bookkeeping must still settle)", nm.loadingCount)
	}
	if nm.status != "watch disabled: /n" {
		t.Fatalf("status = %q, want it left untouched by the discarded stale result", nm.status)
	}
}

func TestUpdateChildWatchArmedForUnknownNodeIsIgnored(t *testing.T) {
	root := &node{path: "/", name: "/"}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	newModel, cmd := m.Update(childWatchArmedMsg{path: "/already-deleted", children: []string{"x"}})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childWatchArmedMsg) for an unknown/deleted node = non-nil cmd, want nil")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
}

// --- tree children-watch: fired message -------------------------------------

func TestUpdateChildWatchFiredStillWatchingReArms(t *testing.T) {
	root := &node{path: "/n", name: "n", watching: true}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)

	newModel, cmd := m.Update(childWatchFiredMsg{path: "/n"})
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("Update(childWatchFiredMsg) while still watching returned a nil cmd, want a re-arm command")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1 (re-arming counts as loading again)", nm.loadingCount)
	}
}

// Attention point 2 (part B): the exact scenario called out in the task —
// the user turns the watch off while a fire is already in flight. The
// eventual fired message must be ignored, never re-arming a watch the user
// just disabled.
func TestUpdateChildWatchFiredIgnoredIfTurnedOffMeanwhile(t *testing.T) {
	root := &node{path: "/n", name: "n", watching: false}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.status = "watch disabled: /n"

	newModel, cmd := m.Update(childWatchFiredMsg{path: "/n"})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childWatchFiredMsg) after the user turned watching off = non-nil cmd, want nil (must not re-arm)")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0 (a discarded fire must not touch the loading counter)", nm.loadingCount)
	}
	if root.watching {
		t.Fatal("node.watching flipped back to true from a stale fire, want it to stay false")
	}
	if nm.status != "watch disabled: /n" {
		t.Fatalf("status = %q, want it left untouched", nm.status)
	}
}

func TestUpdateChildWatchFiredForUnknownNodeIsIgnored(t *testing.T) {
	root := &node{path: "/", name: "/"}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)

	newModel, cmd := m.Update(childWatchFiredMsg{path: "/already-deleted"})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(childWatchFiredMsg) for an unknown/deleted node = non-nil cmd, want nil")
	}
	_ = nm
}

// Attention point 1: re-arming must keep working for more than one cycle,
// not just recover from the very first fire.
func TestUpdateChildWatchReArmsAcrossMultipleCyclesInARow(t *testing.T) {
	root := &node{path: "/n", name: "n", watching: true}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	for cycle := 0; cycle < 3; cycle++ {
		newModel, armCmd := m.Update(childWatchArmedMsg{path: "/n", generation: root.watchGeneration, children: []string{"c"}})
		m = newModel.(Model)
		if armCmd == nil {
			t.Fatalf("cycle %d: armed message produced a nil cmd, want a listen command", cycle)
		}
		if !root.watching {
			t.Fatalf("cycle %d: node.watching = false after arming, want true", cycle)
		}
		if m.loadingCount != 0 {
			t.Fatalf("cycle %d: loadingCount = %d after arming settles, want 0", cycle, m.loadingCount)
		}

		newModel, fireCmd := m.Update(childWatchFiredMsg{path: "/n", generation: root.watchGeneration})
		m = newModel.(Model)
		if fireCmd == nil {
			t.Fatalf("cycle %d: fired message produced a nil cmd, want a re-arm command", cycle)
		}
		if m.loadingCount != 1 {
			t.Fatalf("cycle %d: loadingCount = %d after a fire re-arms, want 1", cycle, m.loadingCount)
		}
	}
	if !root.watching {
		t.Fatal("node.watching = false after 3 full arm/fire cycles, want it still true")
	}
}

// Collapsing (or otherwise navigating away from) a watched node must not
// silently turn its watch off — it's meant to keep observing in the
// background.
func TestCollapsingWatchedNodeDoesNotStopItsWatch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/n", name: "n", parent: root, loaded: true, expanded: true, watching: true}
	root.children = []*node{child}
	nodes := map[string]*node{"/": root, "/n": child}
	m := newTestModel(root, nodes)
	m.list.Select(1)

	newModel, _ := m.Update(keyMsg("left")) // collapse /n
	nm := newModel.(Model)

	if !child.watching {
		t.Fatal("node.watching = false after collapsing the node, want it to stay true (background watch)")
	}
	_ = nm
}

// A watch's armed/fired messages for a node that has since been deleted
// from the local tree (e.g. via the mutation flow) must be ignored safely,
// not crash.
func TestWatchMessagesForLocallyDeletedNodeDoNotCrash(t *testing.T) {
	parent := &node{path: "/", name: "/", expanded: true, loaded: true}
	target := &node{path: "/n", name: "n", parent: parent, watching: true}
	parent.children = []*node{target}
	nodes := map[string]*node{"/": parent, "/n": target}
	m := newTestModel(parent, nodes)
	m.loadingCount = 1

	// Simulate the node having just been deleted (as handleDeleteResult
	// does): removed from the nodes map.
	newModel, _ := m.Update(deleteResultMsg{path: "/n", parentPath: "/"})
	nm := newModel.(Model)

	newModel, cmd := nm.Update(childWatchArmedMsg{path: "/n", children: []string{"x"}})
	nm2 := newModel.(Model)
	if cmd != nil {
		t.Fatal("Update(childWatchArmedMsg) for a locally-deleted node = non-nil cmd, want nil")
	}

	newModel, cmd = nm2.Update(childWatchFiredMsg{path: "/n"})
	if cmd != nil {
		t.Fatal("Update(childWatchFiredMsg) for a locally-deleted node = non-nil cmd, want nil")
	}
	_ = newModel
}

func TestDeletingWatchedNodeCancelsAllLocalListeners(t *testing.T) {
	parent := &node{path: "/", name: "/", expanded: true, loaded: true}
	target := &node{path: "/n", name: "n", parent: parent}
	parent.children = []*node{target}
	m := newTestModel(parent, map[string]*node{"/": parent, "/n": target})
	m.list.Select(1)

	newModel, _ := m.Update(keyMsg("w"))
	childWatching := newModel.(Model)
	childCancel := target.watchCancel
	childWatching.loadingCount++ // the Delete request completing below
	newModel, _ = childWatching.Update(deleteResultMsg{path: "/n", parentPath: "/"})
	select {
	case <-childCancel:
	default:
		t.Fatal("deleting a watched tree node did not cancel its child listener")
	}

	// Rebuild the small tree for the detail watch case; deletion must also
	// stop both listeners that share the detail watch toggle.
	parent = &node{path: "/", name: "/", expanded: true, loaded: true}
	target = &node{path: "/n", name: "n", parent: parent}
	parent.children = []*node{target}
	m = newTestModel(parent, map[string]*node{"/": parent, "/n": target})
	m.focus = focusData
	m.detail = detailState{path: "/n"}
	newModel, _ = m.Update(keyMsg("w"))
	detailWatching := newModel.(Model)
	dataCancel := detailWatching.detail.dataWatchCancel
	statCancel := detailWatching.detail.statWatchCancel
	detailWatching.loadingCount++ // the Delete request completing below
	newModel, _ = detailWatching.Update(deleteResultMsg{path: "/n", parentPath: "/"})
	for name, done := range map[string]chan struct{}{"data": dataCancel, "stat": statCancel} {
		select {
		case <-done:
		default:
			t.Fatalf("deleting a watched detail node did not cancel its %s listener", name)
		}
	}
}

// --- detail data-watch: basic toggle ----------------------------------------

func TestUpdateWatchKeyInDetailArmsWithoutBlocking(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n"}

	newModel, cmd := m.Update(keyMsg("w"))
	nm := newModel.(Model)

	if !nm.detail.watching {
		t.Fatal("detail.watching = false after 'w' in detail mode, want true")
	}
	if cmd == nil {
		t.Fatal("Update(w) in detail mode returned a nil cmd, want a watch-arm command (GetW must only run from a tea.Cmd)")
	}
	if nm.loadingCount != 2 {
		t.Fatalf("loadingCount = %d, want 2 (both the data-watch and the stat-watch arm)", nm.loadingCount)
	}
}

func TestUpdateWatchKeyInDetailTogglesOff(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}

	newModel, cmd := m.Update(keyMsg("w"))
	nm := newModel.(Model)

	if nm.detail.watching {
		t.Fatal("detail.watching = true after toggling off, want false")
	}
	if cmd != nil {
		t.Fatal("Update(w) toggling detail watch off returned a non-nil cmd, want nil")
	}
}

func TestDetailWatchReplacementCancelsBothListenersAndRejectsOldGeneration(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	other := &node{path: "/other", name: "other", parent: root}
	root.children = []*node{other}
	m := newTestModel(root, map[string]*node{"/": root, "/other": other})
	m.focus = focusData
	m.detail = detailState{path: "/old"}

	newModel, _ := m.Update(keyMsg("w"))
	watching := newModel.(Model)
	dataGeneration := watching.detail.dataWatchGeneration
	statGeneration := watching.detail.statWatchGeneration
	dataCancel := watching.detail.dataWatchCancel
	statCancel := watching.detail.statWatchCancel
	// Switching focus alone intentionally keeps the detail watch alive; the
	// next tab replaces the viewed path and must cancel both listeners.
	watching.focus = focusTree
	watching.list.Select(1)
	newModel, _ = watching.Update(keyMsg("tab"))
	replaced := newModel.(Model)

	for name, done := range map[string]chan struct{}{"data": dataCancel, "stat": statCancel} {
		select {
		case <-done:
		default:
			t.Fatalf("replacing detail view did not cancel %s listener", name)
		}
	}
	if replaced.detail.path != "/other" || replaced.detail.watching {
		t.Fatalf("replacement detail state = path %q watching %t, want /other with watch off", replaced.detail.path, replaced.detail.watching)
	}

	newModel, cmd := replaced.Update(dataWatchFiredMsg{path: "/old", generation: dataGeneration})
	if cmd != nil || newModel.(Model).detail.path != "/other" {
		t.Fatal("old data fire revived a watch after replacing the detail view")
	}
	newModel, cmd = replaced.Update(statWatchArmedMsg{path: "/old", generation: statGeneration})
	if cmd != nil {
		t.Fatal("old stat arm started a listener after replacing the detail view")
	}
}

func TestDetailWatchGenerationDoesNotCollideAfterSamePathReopen(t *testing.T) {
	n := &node{path: "/n", name: "n"}
	m := newTestModel(n, map[string]*node{"/n": n})
	m.focus = focusData
	m.detail = detailState{path: "/n"}

	newModel, _ := m.Update(keyMsg("w"))
	first := newModel.(Model)
	oldDataGeneration := first.detail.dataWatchGeneration
	oldStatGeneration := first.detail.statWatchGeneration

	// openDetail resets detailState for the same path, exactly the case that
	// used to restart per-detail counters at generation 1.
	reopened, _ := first.openDetail(n)
	reopened.focus = focusData
	newModel, _ = reopened.Update(keyMsg("w"))
	current := newModel.(Model)
	if current.detail.dataWatchGeneration == oldDataGeneration || current.detail.statWatchGeneration == oldStatGeneration {
		t.Fatal("same-path detail reopen reused a previous watch generation")
	}

	newModel, cmd := current.Update(dataWatchArmedMsg{path: "/n", generation: oldDataGeneration, data: []byte("stale")})
	stale := newModel.(Model)
	if cmd != nil || string(stale.detail.data) == "stale" {
		t.Fatal("old data arm updated the reopened detail view")
	}
	newModel, cmd = stale.Update(statWatchFiredMsg{path: "/n", generation: oldStatGeneration})
	if cmd != nil || !newModel.(Model).detail.watching {
		t.Fatal("old stat fire re-armed or stopped the reopened detail watch")
	}
}

// --- detail data-watch: armed / fired ---------------------------------------

func TestUpdateDataWatchArmedSuccessAppliesDataAndListensAgain(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}
	m.loadingCount = 1

	stat := &zk.Stat{Version: 2}
	newModel, cmd := m.Update(dataWatchArmedMsg{path: "/n", data: []byte("fresh"), stat: stat})
	nm := newModel.(Model)

	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
	if string(nm.detail.data) != "fresh" {
		t.Fatalf("detail.data = %q, want %q", nm.detail.data, "fresh")
	}
	if !nm.detail.watching {
		t.Fatal("detail.watching = false after a successful arm, want true")
	}
	if !strings.Contains(nm.status, "watch active") {
		t.Fatalf("status = %q, want it to mention the watch is active", nm.status)
	}
	if cmd == nil {
		t.Fatal("Update(dataWatchArmedMsg success) returned a nil cmd, want a listen command")
	}
}

func TestUpdateDataWatchArmedErrorStopsWatchWithoutInfiniteRetry(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}
	m.loadingCount = 1

	wantErr := errors.New("zk: get /n: zk: node does not exist")
	newModel, cmd := m.Update(dataWatchArmedMsg{path: "/n", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(dataWatchArmedMsg error) returned a non-nil cmd, want nil — must not retry forever")
	}
	if nm.detail.watching {
		t.Fatal("detail.watching = true after a failed arm, want false")
	}
	if !errors.Is(nm.detail.err, wantErr) {
		t.Fatalf("detail.err = %v, want it to carry the failure", nm.detail.err)
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a failed data-watch arm, want true")
	}
	rendered := renderDetail(nm.detail)
	if !strings.Contains(rendered, "node does not exist") {
		t.Fatalf("renderDetail after watch error = %q, want the error visible inline", rendered)
	}
}

func TestUpdateDataWatchArmedDiscardedIfTurnedOffOrNavigatedAway(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: false} // already turned off
	m.loadingCount = 1

	newModel, cmd := m.Update(dataWatchArmedMsg{path: "/n", data: []byte("late")})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(dataWatchArmedMsg) after turning watch off = non-nil cmd, want nil")
	}
	if len(nm.detail.data) != 0 {
		t.Fatalf("detail.data = %q, want untouched by the stale arm result", nm.detail.data)
	}
}

func TestUpdateDataWatchFiredStillWatchingReArms(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}

	newModel, cmd := m.Update(dataWatchFiredMsg{path: "/n"})
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("Update(dataWatchFiredMsg) while still watching returned a nil cmd, want a re-arm command")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
}

// The panels stay on screen when the focus leaves them, so a data-watch is no
// longer tied to "the panel is open": it keeps updating the still-visible panel
// and is deliberately left armed.
func TestUnfocusingTheDataPanelKeepsItsWatchArmed(t *testing.T) {
	for _, k := range []string{"tab", "esc"} {
		t.Run(k, func(t *testing.T) {
			root := &node{path: "/n", name: "n"}
			nodes := map[string]*node{"/n": root}
			m := newTestModel(root, nodes)
			m.focus = focusData
			m.detail = detailState{path: "/n", watching: true, data: []byte("x"), stat: &zk.Stat{}}

			newModel, cmd := m.Update(keyMsg(k))
			nm := newModel.(Model)

			if nm.focus != focusTree {
				t.Fatalf("focus after %q = %v, want focusTree", k, nm.focus)
			}
			if !nm.detail.watching {
				t.Fatal("detail.watching = false after moving the focus away, want the watch left armed")
			}
			if nm.detail.path != "/n" {
				t.Fatalf("detail.path = %q, want the panel to keep its content", nm.detail.path)
			}
			if cmd != nil {
				t.Fatalf("Update(%q) moving the focus returned a non-nil cmd, want nil", k)
			}

			// A fire that was in flight still re-arms, because the panel it
			// feeds is still on screen.
			_, cmd2 := nm.Update(dataWatchFiredMsg{path: "/n"})
			if cmd2 == nil {
				t.Fatal("Update(dataWatchFiredMsg) after unfocusing = nil cmd, want the watch re-armed")
			}
		})
	}
}

// Loading another node into the panel is what stops the previous node's
// watch — a fire or arm for the old path that was already in flight must be
// discarded rather than re-arming anything.
func TestLoadingAnotherNodeStopsThePreviousDataWatch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	other := &node{path: "/otro", name: "otro", parent: root}
	root.children = []*node{other}
	nodes := map[string]*node{"/": root, "/otro": other}

	m := newTestModel(root, nodes)
	m.detail = detailState{path: "/n", watching: true, data: []byte("x"), stat: &zk.Stat{}}
	m.list.Select(1) // /otro

	newModel, cmd := m.Update(keyMsg("tab"))
	nm := newModel.(Model)

	if nm.detail.path != "/otro" {
		t.Fatalf("detail.path = %q, want /otro", nm.detail.path)
	}
	if nm.detail.watching {
		t.Fatal("detail.watching = true after loading another node, want the previous watch dropped")
	}
	if cmd == nil {
		t.Fatal("Update(tab) on another node returned a nil cmd, want a fetch command")
	}

	// Late messages for the node that is no longer loaded.
	_, cmd2 := nm.Update(dataWatchFiredMsg{path: "/n"})
	if cmd2 != nil {
		t.Fatal("Update(dataWatchFiredMsg) for the previous path = non-nil cmd, want nil (must not re-arm)")
	}

	newModel, cmd3 := nm.Update(dataWatchArmedMsg{path: "/n", data: []byte("late-arm"), stat: &zk.Stat{}})
	nm3 := newModel.(Model)
	if cmd3 != nil {
		t.Fatal("Update(dataWatchArmedMsg) for the previous path = non-nil cmd, want nil (must not start listening)")
	}
	if string(nm3.detail.data) == "late-arm" {
		t.Fatal("a late arm for the previous path overwrote the current panel's data")
	}
}

// Deleting the loaded node clears the panels, which also drops any watch on
// it: every watch handler discards messages whose path is not the current one.
func TestDeletingTheLoadedNodeClearsThePanelsAndItsWatch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	target := &node{path: "/n", name: "n", parent: root}
	root.children = []*node{target}
	nodes := map[string]*node{"/": root, "/n": target}

	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true, data: []byte("x"), stat: &zk.Stat{}}
	m.loadingCount = 1

	newModel, _ := m.Update(deleteResultMsg{path: "/n", parentPath: "/"})
	nm := newModel.(Model)

	if nm.detail.path != "" {
		t.Fatalf("detail.path = %q after deleting the loaded node, want it cleared", nm.detail.path)
	}
	if nm.detail.watching {
		t.Fatal("detail.watching = true after the loaded node was deleted, want false")
	}
	if nm.focus != focusTree {
		t.Fatalf("focus = %v after deleting the loaded node, want focusTree", nm.focus)
	}

	_, cmd := nm.Update(dataWatchFiredMsg{path: "/n"})
	if cmd != nil {
		t.Fatal("Update(dataWatchFiredMsg) for the deleted node = non-nil cmd, want nil")
	}
}

func TestReopeningDetailPanelStartsWithWatchOff(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, _ := m.Update(keyMsg("tab"))
	nm := newModel.(Model)

	if nm.detail.watching {
		t.Fatal("detail.watching = true right after opening the panel, want false (must be re-enabled explicitly with 'w')")
	}
}

// Attention point 1 for the data watch too: re-arming must survive more
// than one cycle in a row.
func TestUpdateDataWatchReArmsAcrossMultipleCyclesInARow(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}

	for cycle := 0; cycle < 3; cycle++ {
		newModel, armCmd := m.Update(dataWatchArmedMsg{path: "/n", generation: m.detail.dataWatchGeneration, data: []byte("v"), stat: &zk.Stat{}})
		m = newModel.(Model)
		if armCmd == nil {
			t.Fatalf("cycle %d: armed message produced a nil cmd, want a listen command", cycle)
		}
		if !m.detail.watching {
			t.Fatalf("cycle %d: detail.watching = false after arming, want true", cycle)
		}

		newModel, fireCmd := m.Update(dataWatchFiredMsg{path: "/n", generation: m.detail.dataWatchGeneration})
		m = newModel.(Model)
		if fireCmd == nil {
			t.Fatalf("cycle %d: fired message produced a nil cmd, want a re-arm command", cycle)
		}
	}
	if !m.detail.watching {
		t.Fatal("detail.watching = false after 3 full arm/fire cycles, want it still true")
	}
}

// --- detail stat-watch: keeps the Stat panel fresh across child changes ----
//
// A GetW data-watch only fires on a setData or a delete of the watched node
// itself, never on a child being created/removed underneath it (confirmed
// against a real server by TestIntegrationGetWDoesNotFireOnChildCreated in
// internal/zk). So the detail watch also arms an independent stat-watch
// (ChildrenW) on the same path; these tests exercise that second flow.

func TestUpdateStatWatchArmedSuccessListensAgain(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}
	m.loadingCount = 1

	events := make(chan zk.Event)
	newModel, cmd := m.Update(statWatchArmedMsg{path: "/n", events: events})
	nm := newModel.(Model)

	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
	if !nm.detail.watching {
		t.Fatal("detail.watching = false after a successful stat-watch arm, want true")
	}
	if cmd == nil {
		t.Fatal("Update(statWatchArmedMsg success) returned a nil cmd, want a listen command")
	}
}

func TestUpdateStatWatchArmedErrorStopsWatchWithoutInfiniteRetry(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", watching: true}
	m.loadingCount = 1

	wantErr := errors.New("zk: children of /n: zk: node does not exist")
	newModel, cmd := m.Update(statWatchArmedMsg{path: "/n", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(statWatchArmedMsg error) returned a non-nil cmd, want nil — must not retry forever")
	}
	if nm.detail.watching {
		t.Fatal("detail.watching = true after a failed stat-watch arm, want false")
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a failed stat-watch arm, want true")
	}
}

func TestUpdateStatWatchArmedDiscardedIfTurnedOffMeanwhile(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.detail = detailState{path: "/n", watching: false} // already turned off
	m.loadingCount = 1

	events := make(chan zk.Event)
	newModel, cmd := m.Update(statWatchArmedMsg{path: "/n", events: events})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(statWatchArmedMsg) after turning watch off = non-nil cmd, want nil (must not start listening on an unwanted watch)")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
}

// TestUpdateStatWatchFiredRefreshesStatAndReArms is the regression test for
// the reported bug: a child created elsewhere fires the stat-watch (not the
// data-watch), and that alone must be enough to bring cversion/numChildren
// back in sync, without touching the data already shown.
func TestUpdateStatWatchFiredRefreshesStatAndReArms(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	oldStat := &zk.Stat{NumChildren: 0, Cversion: 0}
	m.detail = detailState{path: "/n", watching: true, data: []byte("payload"), stat: oldStat}

	newModel, cmd := m.Update(statWatchFiredMsg{path: "/n"})
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("Update(statWatchFiredMsg) while still watching returned a nil cmd, want commands to refresh Stat and re-arm")
	}
	if nm.loadingCount != 2 {
		t.Fatalf("loadingCount = %d, want 2 (a Stat refresh plus a re-arm)", nm.loadingCount)
	}

	newStat := &zk.Stat{NumChildren: 1, Cversion: 1}
	newModel, cmd = nm.Update(statRefreshedMsg{path: "/n", exists: true, stat: newStat})
	nm2 := newModel.(Model)

	if nm2.detail.stat.NumChildren != 1 {
		t.Fatalf("detail.stat.NumChildren = %d after the stat-watch's refresh, want 1 — a child created elsewhere must update the Stat panel even though the data-watch never fires for it", nm2.detail.stat.NumChildren)
	}
	if string(nm2.detail.data) != "payload" {
		t.Fatalf("detail.data = %q, want it untouched by a Stat-only refresh", nm2.detail.data)
	}
	if nm2.loadingCount != 1 {
		t.Fatalf("loadingCount = %d after the refresh settles, want 1 (the re-arm is still in flight)", nm2.loadingCount)
	}
	_ = cmd
}

func TestUpdateStatWatchFiredIgnoredIfTurnedOffMeanwhile(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.detail = detailState{path: "/n", watching: false}

	newModel, cmd := m.Update(statWatchFiredMsg{path: "/n"})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(statWatchFiredMsg) after the user turned watching off = non-nil cmd, want nil (must not re-arm)")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0 (a discarded fire must not touch the loading counter)", nm.loadingCount)
	}
}

func TestUpdateStatRefreshedDiscardedForDifferentPath(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	stat := &zk.Stat{}
	m.detail = detailState{path: "/n", watching: true, stat: stat}
	m.loadingCount = 1

	newModel, cmd := m.Update(statRefreshedMsg{path: "/other", exists: true, stat: &zk.Stat{NumChildren: 9}})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(statRefreshedMsg) for a different path = non-nil cmd, want nil")
	}
	if nm.detail.stat.NumChildren != 0 {
		t.Fatal("a stale statRefreshedMsg for a different path overwrote the current Stat")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
}

// A statRefreshedMsg reporting the node gone (deleted concurrently, between
// the stat-watch firing and this Exists call landing) must not clobber the
// panel: the data-watch's own re-arm cycle already reports deletion.
func TestUpdateStatRefreshedIgnoresDeletedNode(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	stat := &zk.Stat{NumChildren: 3}
	m.detail = detailState{path: "/n", watching: true, stat: stat}
	m.loadingCount = 1

	newModel, _ := m.Update(statRefreshedMsg{path: "/n", exists: false})
	nm := newModel.(Model)

	if nm.detail.stat.NumChildren != 3 {
		t.Fatal("a statRefreshedMsg reporting the node gone must leave the last-known Stat alone")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
}

// --- tree watch keeping the detail Stat fresh -------------------------------

// A node's children changing moves its own cversion/numChildren/pzxid. When
// the watch that noticed is the tree's children-watch rather than the detail
// panel's own, the Stat panel used to sit stale until the user reloaded it
// with tab.
func TestChildWatchArmedRefreshesTheStatOfTheLoadedNode(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	n := &node{path: "/n", name: "n", parent: root, expanded: true, loaded: true, watching: true}
	root.children = []*node{n}
	m := newTestModel(root, map[string]*node{"/": root, "/n": n})
	m.detail = detailState{path: "/n", stat: &zk.Stat{}}
	m.syncDetailViews()

	before := m.loadingCount
	_, cmd := m.Update(childWatchArmedMsg{path: "/n", children: []string{"hijo"}})
	if cmd == nil {
		t.Fatal("Update(childWatchArmedMsg) returned a nil cmd, want the re-listen plus a Stat refresh")
	}

	// The refresh is an extra in-flight command and must be accounted for.
	newModel, _ := m.Update(childWatchArmedMsg{path: "/n", children: []string{"hijo"}})
	if got := newModel.(Model).loadingCount; got != before {
		t.Fatalf("loadingCount = %d, want %d+1 for the queued Stat refresh minus this handler's own decrement", got, before)
	}
}

// A node that is watched in the tree but not loaded in the panels must not
// trigger a detail refresh.
func TestChildWatchArmedDoesNotRefreshStatForAnotherNode(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	n := &node{path: "/n", name: "n", parent: root, expanded: true, loaded: true, watching: true}
	root.children = []*node{n}
	m := newTestModel(root, map[string]*node{"/": root, "/n": n})
	m.detail = detailState{path: "/otro", stat: &zk.Stat{}}
	m.loadingCount = 1

	newModel, _ := m.Update(childWatchArmedMsg{path: "/n", children: []string{"hijo"}})
	if got := newModel.(Model).loadingCount; got != 0 {
		t.Fatalf("loadingCount = %d, want 0 — no Stat refresh should have been queued", got)
	}
}

// The refresh result has to be applied even though the detail panel's own
// watch is off: the tree's watch is what triggered it.
func TestStatRefreshedAppliesWithoutTheDetailWatch(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	m := newTestModel(root, map[string]*node{"/n": root})
	// Sized so the right column really splits in two and statVP renders.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = sized.(Model)
	m.detail = detailState{path: "/n", stat: &zk.Stat{NumChildren: 0}, watching: false}
	m.syncDetailViews()
	m.loadingCount = 1

	newModel, _ := m.Update(statRefreshedMsg{path: "/n", exists: true, stat: &zk.Stat{NumChildren: 3, Cversion: 7}})
	nm := newModel.(Model)

	if nm.detail.stat.NumChildren != 3 || nm.detail.stat.Cversion != 7 {
		t.Fatalf("detail.stat = %+v, want the refreshed values applied with watching=false", nm.detail.stat)
	}
	if !strings.Contains(nm.statVP.View(), "3") {
		t.Fatalf("statVP = %q, want the refreshed numChildren rendered, not just stored", nm.statVP.View())
	}
}

// 'w' means the same thing on both detail panels: the watch belongs to the
// znode, not to the panel the cursor happens to be in.
func TestWatchKeyWorksFromTheStatPanel(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	m := newTestModel(root, map[string]*node{"/n": root})
	m.focus = focusStat
	m.detail = detailState{path: "/n"}

	newModel, cmd := m.Update(keyMsg("w"))
	nm := newModel.(Model)

	if !nm.detail.watching {
		t.Fatal("detail.watching = false after 'w' on the Stat panel, want the watch armed")
	}
	if cmd == nil {
		t.Fatal("Update(w) on the Stat panel returned a nil cmd, want the watch-arm commands")
	}
	if nm.focus != focusStat {
		t.Fatalf("focus = %v, want it left on the Stat panel", nm.focus)
	}
}

func TestWatchKeyOnStatPanelWithNothingLoadedIsANoop(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	m := newTestModel(root, map[string]*node{"/n": root})
	m.focus = focusStat

	newModel, cmd := m.Update(keyMsg("w"))
	if cmd != nil {
		t.Fatal("Update(w) with nothing loaded returned a non-nil cmd, want nil")
	}
	if newModel.(Model).detail.watching {
		t.Fatal("detail.watching = true with no node loaded, want false")
	}
}
