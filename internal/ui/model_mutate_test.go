package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func typeRunes(m Model, s string) Model {
	for _, r := range s {
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = newModel.(Model)
	}
	return m
}

// --- create wizard -----------------------------------------------------------

func TestUpdateCreateKeyOpensWizardWithoutBlocking(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, cmd := m.Update(keyMsg("n"))
	nm := newModel.(Model)

	if nm.mode != modeCreate {
		t.Fatalf("mode after 'n' = %v, want modeCreate", nm.mode)
	}
	if nm.create.parent != root {
		t.Fatal("create.parent not set to the selected node")
	}
	if nm.create.step != createStepName {
		t.Fatalf("create.step = %v, want createStepName", nm.create.step)
	}
	if !nm.create.name.Focused() {
		t.Fatal("create.name input is not focused after opening the wizard")
	}
	_ = cmd // may be the textinput blink cmd; just must not panic (nil client untouched at this point)
}

func TestUpdateCreateEmptyNameDoesNotAdvance(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, _ := m.Update(keyMsg("n"))
	m2 := newModel.(Model)

	// Name is empty (only whitespace typed): enter must not advance.
	m2 = typeRunes(m2, "   ")
	newModel, _ = m2.Update(keyMsg("enter"))
	m3 := newModel.(Model)

	if m3.create.step != createStepName {
		t.Fatalf("create.step after enter with a blank name = %v, want it to stay at createStepName", m3.create.step)
	}
}

func TestUpdateCreateWizardFullFlowReachesModeStep(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, _ := m.Update(keyMsg("n"))
	m2 := newModel.(Model)
	m2 = typeRunes(m2, "child1")

	newModel, _ = m2.Update(keyMsg("enter"))
	m3 := newModel.(Model)
	if m3.create.step != createStepData {
		t.Fatalf("create.step after naming = %v, want createStepData", m3.create.step)
	}
	if !m3.create.data.Focused() {
		t.Fatal("create.data input is not focused after advancing to the data step")
	}
	if m3.create.name.Focused() {
		t.Fatal("create.name input is still focused after advancing past it")
	}

	m3 = typeRunes(m3, "hello")
	newModel, _ = m3.Update(keyMsg("enter"))
	m4 := newModel.(Model)
	if m4.create.step != createStepMode {
		t.Fatalf("create.step after entering data = %v, want createStepMode", m4.create.step)
	}
	if m4.create.name.Value() != "child1" || m4.create.data.Value() != "hello" {
		t.Fatalf("create name/data = %q/%q, want child1/hello", m4.create.name.Value(), m4.create.data.Value())
	}
}

func TestUpdateCreateModeStepCyclesWithWraparound(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.mode = modeCreate
	m.create = createState{parent: root, step: createStepMode, modeIndex: 0}

	newModel, _ := m.Update(keyMsg("left"))
	nm := newModel.(Model)
	if nm.create.modeIndex != len(createModeOptions)-1 {
		t.Fatalf("modeIndex after left from 0 = %d, want %d (wrap to last)", nm.create.modeIndex, len(createModeOptions)-1)
	}

	newModel, _ = nm.Update(keyMsg("right"))
	nm2 := newModel.(Model)
	if nm2.create.modeIndex != 0 {
		t.Fatalf("modeIndex after right from last = %d, want 0 (wrap to first)", nm2.create.modeIndex)
	}
}

func TestUpdateCreateEscCancelsAtAnyStepWithoutCreating(t *testing.T) {
	for _, step := range []createStep{createStepName, createStepData, createStepMode} {
		root := &node{path: "/", name: "/", expanded: true, loaded: true}
		nodes := map[string]*node{"/": root}
		m := newTestModel(root, nodes)
		m.mode = modeCreate
		m.create = createState{parent: root, step: step, name: newTextInput(""), data: newTextInput("")}

		newModel, cmd := m.Update(keyMsg("esc"))
		nm := newModel.(Model)

		if nm.mode != modeTree {
			t.Fatalf("step %v: mode after esc = %v, want modeTree", step, nm.mode)
		}
		if cmd != nil {
			t.Fatalf("step %v: Update(esc) returned a non-nil cmd, want nil (must not create anything)", step)
		}
	}
}

func TestUpdateCreateFinalEnterFiresCreateCmdAndReturnsToTreeImmediately(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.mode = modeCreate
	m.create = createState{parent: root, step: createStepMode, modeIndex: 1, name: newTextInput(""), data: newTextInput("")}
	m.create.name.SetValue("newchild")
	m.create.data.SetValue("data")

	newModel, cmd := m.Update(keyMsg("enter"))
	nm := newModel.(Model)

	if nm.mode != modeTree {
		t.Fatalf("mode right after firing Create = %v, want modeTree (does not wait for the result)", nm.mode)
	}
	if cmd == nil {
		t.Fatal("Update(enter) on the final create step returned a nil cmd, want a create command (Create is only ever called from a tea.Cmd)")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
	// m.client is nil: if advanceCreate had called client.Create synchronously
	// instead of deferring to createCmd, this test would already have
	// panicked with a nil pointer dereference.
}

func TestUpdateCreateResultSuccessInvalidatesExpandedParentCache(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	newModel, cmd := m.Update(createResultMsg{parentPath: "/", requestedPath: "/x", actualPath: "/x"})
	nm := newModel.(Model)

	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1 (the create's own loading finished, but the parent's immediate re-fetch started a new one)", nm.loadingCount)
	}
	if nm.statusErr {
		t.Fatal("statusErr = true after a successful create, want false")
	}
	if !strings.Contains(nm.status, "/x") {
		t.Fatalf("status = %q, want it to mention the created path", nm.status)
	}
	if root.loaded {
		t.Fatal("parent.loaded = true after a successful create under it, want the cache invalidated (false)")
	}
	if !root.loading {
		t.Fatal("parent.loading = false, want true: the parent is expanded, so it should be refetching immediately")
	}
	if cmd == nil {
		t.Fatal("Update(createResultMsg success) with an expanded parent returned a nil cmd, want a re-fetch command")
	}
}

func TestUpdateCreateResultSuccessOnCollapsedParentInvalidatesWithoutRefetch(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: false, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	newModel, cmd := m.Update(createResultMsg{parentPath: "/", requestedPath: "/x", actualPath: "/x"})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(createResultMsg success) with a collapsed parent returned a non-nil cmd, want nil (no need to refetch until expanded)")
	}
	if root.loaded {
		t.Fatal("parent.loaded = true, want the cache invalidated (false) so the next expand refetches")
	}
	_ = nm
}

func TestUpdateCreateResultErrorSurfacesWithoutInvalidatingCache(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	wantErr := errors.New("zk: create /x: zk: node already exists")
	newModel, cmd := m.Update(createResultMsg{parentPath: "/", requestedPath: "/x", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(createResultMsg error) returned a non-nil cmd, want nil")
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a create error, want true")
	}
	if !strings.Contains(nm.status, "/x") || !strings.Contains(nm.status, "already exists") {
		t.Fatalf("status = %q, want it to mention the path and the error", nm.status)
	}
	if !root.loaded {
		t.Fatal("parent.loaded = false after a failed create, want the cache left untouched (still true)")
	}
}

// --- edit -----------------------------------------------------------------

func TestUpdateEditKeyFromTreeOpensFormWithoutBlocking(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, cmd := m.Update(keyMsg("e"))
	nm := newModel.(Model)

	if nm.mode != modeEdit {
		t.Fatalf("mode after 'e' = %v, want modeEdit", nm.mode)
	}
	if nm.edit.path != "/" {
		t.Fatalf("edit.path = %q, want /", nm.edit.path)
	}
	if !nm.edit.loading {
		t.Fatal("edit.loading = false right after opening, want true")
	}
	if nm.focus != focusTree {
		t.Fatalf("focus after opening the edit dialog = %v, want focusTree (a dialog must not move the focus)", nm.focus)
	}
	if cmd == nil {
		t.Fatal("Update(e) returned a nil cmd, want a fetch-for-edit command (Get must only run from a tea.Cmd)")
	}
}

func TestUpdateEditKeyFromDetailPanelLeavesTheFocusThere(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/"}

	newModel, cmd := m.Update(keyMsg("e"))
	nm := newModel.(Model)

	if nm.mode != modeEdit {
		t.Fatalf("mode after 'e' from detail = %v, want modeEdit", nm.mode)
	}
	// The focus survives the dialog untouched, which is what makes closing it
	// return to the panel the user opened it from.
	if nm.focus != focusData {
		t.Fatalf("focus after opening the edit dialog from the data panel = %v, want focusData", nm.focus)
	}
	if cmd == nil {
		t.Fatal("Update(e) from detail returned a nil cmd, want a fetch-for-edit command")
	}
}

func TestUpdateEditStatSuccessSeedsFormWithCurrentDataAndVersion(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeEdit
	m.edit = editState{path: "/n", loading: true}
	m.loadingCount = 1

	newModel, cmd := m.Update(editStatMsg{path: "/n", data: []byte("current"), version: 7})
	nm := newModel.(Model)

	if cmd == nil {
		t.Fatal("Update(editStatMsg success) returned a nil cmd, want the textinput's focus/blink cmd")
	}
	if nm.edit.loading {
		t.Fatal("edit.loading = true after editStatMsg, want false")
	}
	if nm.edit.version != 7 {
		t.Fatalf("edit.version = %d, want 7 (the version read via Get)", nm.edit.version)
	}
	if nm.edit.input.Value() != "current" {
		t.Fatalf("edit.input.Value() = %q, want %q (seeded with the current data)", nm.edit.input.Value(), "current")
	}
	if !nm.edit.input.Focused() {
		t.Fatal("edit.input is not focused after being seeded")
	}
	if nm.loadingCount != 0 {
		t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
	}
}

func TestUpdateEditStatRejectsPayloadsTheSingleLineEditorCannotPreserve(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "multiline", data: []byte("first\nsecond"), want: "multiline"},
		{name: "over limit", data: []byte(strings.Repeat("x", editCharLimit+1)), want: "exceeds"},
		{name: "invalid UTF-8", data: []byte{0xff, 0xfe}, want: "valid UTF-8"},
		{name: "tab", data: []byte("left\tright"), want: "round-trip"},
		{name: "C0 control rune", data: []byte("bell\a"), want: "round-trip"},
		{name: "C1 control rune", data: []byte("control\u009b"), want: "round-trip"},
		{name: "literal replacement character", data: []byte("replacement � character"), want: "round-trip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &node{path: "/n", name: "n"}
			m := newTestModel(root, map[string]*node{"/n": root})
			m.mode = modeEdit
			m.edit = editState{path: "/n", loading: true}
			m.loadingCount = 1

			newModel, cmd := m.Update(editStatMsg{path: "/n", data: tt.data, version: 7})
			nm := newModel.(Model)

			if cmd != nil {
				t.Fatal("unsupported edit payload returned a focus command, want nil")
			}
			if nm.edit.err == nil || !strings.Contains(nm.edit.err.Error(), tt.want) {
				t.Fatalf("edit.err = %v, want an error containing %q", nm.edit.err, tt.want)
			}
			if nm.edit.input.Focused() {
				t.Fatal("input is focused for an unsupported payload; Save must remain unavailable")
			}
			if nm.loadingCount != 0 {
				t.Fatalf("loadingCount = %d, want 0", nm.loadingCount)
			}
			newModel, saveCmd := nm.Update(keyMsg("enter"))
			blocked := newModel.(Model)
			if saveCmd != nil || blocked.mode != modeEdit {
				t.Fatal("enter submitted an unsupported payload; Save must remain unavailable")
			}
		})
	}
}

func TestEditPayloadPreservesOriginalBytesWhenUnchanged(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "plain text", data: []byte("current")},
		{name: "unicode", data: []byte("héllo 世界")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := editableData(tt.data)
			if err != nil {
				t.Fatalf("editableData(%q): %v", tt.data, err)
			}
			input := newTextInput("")
			input.SetValue(value)
			es := editState{
				input:         input,
				originalData:  append([]byte(nil), tt.data...),
				originalValue: value,
			}

			// payload is passed directly to Client.Set by submitEdit. A no-op
			// save must use the exact bytes read from ZooKeeper.
			if got := es.payload(); !bytes.Equal(got, tt.data) {
				t.Fatalf("payload bytes = %v, want original bytes %v", got, tt.data)
			}
		})
	}
}

func TestUpdateEditStatErrorShownInFormWithoutCrashAndBlocksTyping(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeEdit
	m.edit = editState{path: "/n", loading: true}
	m.loadingCount = 1

	wantErr := errors.New("zk: get /n: zk: node does not exist")
	newModel, _ := m.Update(editStatMsg{path: "/n", err: wantErr})
	nm := newModel.(Model)

	if !errors.Is(nm.edit.err, wantErr) {
		t.Fatalf("edit.err = %v, want it to carry the fetch error", nm.edit.err)
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a failed pre-read, want true")
	}
	rendered := renderEdit(nm.edit)
	if !strings.Contains(rendered, "node does not exist") {
		t.Fatalf("renderEdit after error = %q, want the error visible in the form", rendered)
	}

	// Typing must be a no-op once the form is in an error state.
	newModel2, _ := nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	nm2 := newModel2.(Model)
	if nm2.edit.input.Value() != "" {
		t.Fatalf("edit.input.Value() = %q after typing on an errored form, want it to stay empty/untouched", nm2.edit.input.Value())
	}
}

func TestUpdateEditCancelClosesTheDialogWithoutSubmitting(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeEdit
	m.edit = editState{path: "/n", version: 3, input: newTextInput("")}

	newModel, cmd := m.Update(keyMsg("esc"))
	nm := newModel.(Model)

	if nm.mode != modeTree {
		t.Fatalf("mode after esc = %v, want modeTree (the dialog closed)", nm.mode)
	}
	if cmd != nil {
		t.Fatal("Update(esc) in edit mode returned a non-nil cmd, want nil (must not Set anything)")
	}
}

func TestUpdateEditConfirmFiresSetCmdWithoutBlocking(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeEdit
	m.edit = editState{path: "/n", version: 5, input: newTextInput("")}
	m.edit.input.SetValue("new data")

	newModel, cmd := m.Update(keyMsg("enter"))
	nm := newModel.(Model)

	if nm.mode != modeTree {
		t.Fatalf("mode after confirming edit = %v, want modeTree (the dialog closed)", nm.mode)
	}
	if cmd == nil {
		t.Fatal("Update(enter) confirming edit returned a nil cmd, want a set command (Set must only run from a tea.Cmd)")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
	// m.client is nil: a synchronous client.Set call inside submitEdit would
	// already have panicked above.
}

func TestUpdateSetResultSuccessRefreshesOpenDetailPane(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n", data: []byte("old")}
	m.loadingCount = 1

	newModel, cmd := m.Update(setResultMsg{path: "/n"})
	nm := newModel.(Model)

	if nm.statusErr {
		t.Fatal("statusErr = true after a successful set, want false")
	}
	if !nm.detail.loading {
		t.Fatal("detail.loading = false after a successful set on the currently-open detail path, want true (must refetch)")
	}
	if cmd == nil {
		t.Fatal("Update(setResultMsg success) with the detail panel open on the same path returned a nil cmd, want a refetch command")
	}
}

func TestUpdateSetResultSuccessOnUnrelatedPathDoesNotTouchDetail(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/other", data: []byte("old")}
	m.loadingCount = 1

	newModel, cmd := m.Update(setResultMsg{path: "/n"})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(setResultMsg) for a path other than the open detail pane returned a non-nil cmd, want nil")
	}
	if nm.detail.loading {
		t.Fatal("detail.loading = true, want the unrelated detail pane left untouched")
	}
}

func TestUpdateSetResultErrorSurfacesWithoutCrash(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.loadingCount = 1

	wantErr := errors.New("zk: set /n: zk: version conflict")
	newModel, cmd := m.Update(setResultMsg{path: "/n", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(setResultMsg error) returned a non-nil cmd, want nil")
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a set error, want true")
	}
	if !strings.Contains(nm.status, "version conflict") {
		t.Fatalf("status = %q, want it to mention the error", nm.status)
	}
}

// --- delete confirmation ----------------------------------------------------

func TestUpdateDeleteKeyOpensConfirmationWithoutBlocking(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	nodes := map[string]*node{"/": root}
	m := newTestModel(root, nodes)
	m.list.Select(0)

	newModel, cmd := m.Update(keyMsg("x"))
	nm := newModel.(Model)

	if nm.mode != modeConfirmDelete {
		t.Fatalf("mode after 'x' = %v, want modeConfirmDelete", nm.mode)
	}
	if nm.del.target != root {
		t.Fatal("del.target not set to the selected node")
	}
	if !nm.del.loading {
		t.Fatal("del.loading = false right after opening, want true")
	}
	if cmd == nil {
		t.Fatal("Update(x) returned a nil cmd, want a fetch-stat-for-delete command (Get must only run from a tea.Cmd)")
	}
}

func TestUpdateDeleteConfirmationAnyNonConfirmKeyCancelsWithoutDeleting(t *testing.T) {
	cases := []string{"esc", "x", "n", "q", "s", "enter", "up", "down"}
	for _, k := range cases {
		if k == "q" {
			continue // q is the global quit binding, tested separately
		}
		t.Run(k, func(t *testing.T) {
			root := &node{path: "/n", name: "n"}
			nodes := map[string]*node{"/n": root}
			m := newTestModel(root, nodes)
			m.mode = modeConfirmDelete
			m.del = deleteState{target: root, version: 2}

			newModel, cmd := m.Update(keyMsg(k))
			nm := newModel.(Model)

			if nm.mode != modeTree {
				t.Fatalf("key %q: mode after cancel = %v, want modeTree", k, nm.mode)
			}
			if cmd != nil {
				t.Fatalf("key %q: Update() returned a non-nil cmd, want nil (must never delete on a non-confirm key)", k)
			}
		})
	}
}

func TestUpdateDeleteConfirmationYesFiresDeleteCmdWithoutBlocking(t *testing.T) {
	parent := &node{path: "/", name: "/"}
	root := &node{path: "/n", name: "n", parent: parent}
	nodes := map[string]*node{"/": parent, "/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeConfirmDelete
	m.del = deleteState{target: root, version: 2}

	newModel, cmd := m.Update(keyMsg("y"))
	nm := newModel.(Model)

	if nm.mode != modeTree {
		t.Fatalf("mode after confirming delete = %v, want modeTree (the dialog closed)", nm.mode)
	}
	if cmd == nil {
		t.Fatal("Update() returned a nil cmd, want a delete command (Delete must only run from a tea.Cmd)")
	}
	if nm.loadingCount != 1 {
		t.Fatalf("loadingCount = %d, want 1", nm.loadingCount)
	}
}

func TestUpdateDeleteConfirmationYesWhileLoadingOrErrorDoesNotDelete(t *testing.T) {
	t.Run("still loading", func(t *testing.T) {
		root := &node{path: "/n", name: "n"}
		nodes := map[string]*node{"/n": root}
		m := newTestModel(root, nodes)
		m.mode = modeConfirmDelete
		m.del = deleteState{target: root, loading: true}

		_, cmd := m.Update(keyMsg("y"))
		if cmd != nil {
			t.Fatal("Update(y) while del.loading = non-nil cmd, want nil (Stat not read yet, no version to guard the delete)")
		}
	})
	t.Run("stat read failed", func(t *testing.T) {
		root := &node{path: "/n", name: "n"}
		nodes := map[string]*node{"/n": root}
		m := newTestModel(root, nodes)
		m.mode = modeConfirmDelete
		m.del = deleteState{target: root, err: errors.New("boom")}

		_, cmd := m.Update(keyMsg("y"))
		if cmd != nil {
			t.Fatal("Update(y) after a failed stat read = non-nil cmd, want nil")
		}
	})
}

func TestUpdateDeleteStatSuccessSetsVersion(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeConfirmDelete
	m.del = deleteState{target: root, loading: true}

	newModel, cmd := m.Update(deleteStatMsg{path: "/n", version: 9})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(deleteStatMsg success) returned a non-nil cmd, want nil")
	}
	if nm.del.loading {
		t.Fatal("del.loading = true after deleteStatMsg, want false")
	}
	if nm.del.version != 9 {
		t.Fatalf("del.version = %d, want 9", nm.del.version)
	}
}

func TestUpdateDeleteStatErrorShownWithoutCrash(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.mode = modeConfirmDelete
	m.del = deleteState{target: root, loading: true}

	wantErr := errors.New("zk: get /n: zk: node does not exist")
	newModel, cmd := m.Update(deleteStatMsg{path: "/n", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(deleteStatMsg error) returned a non-nil cmd, want nil")
	}
	if !errors.Is(nm.del.err, wantErr) {
		t.Fatalf("del.err = %v, want it to carry the fetch error", nm.del.err)
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a failed pre-read, want true")
	}
	rendered := renderConfirmDelete(nm.del)
	if !strings.Contains(rendered, "node does not exist") {
		t.Fatalf("renderConfirmDelete after error = %q, want the error visible", rendered)
	}
}

func TestUpdateDeleteResultSuccessRemovesNodeAndInvalidatesParentCache(t *testing.T) {
	parent := &node{path: "/", name: "/", expanded: true, loaded: true}
	target := &node{path: "/n", name: "n", parent: parent}
	parent.children = []*node{target}
	nodes := map[string]*node{"/": parent, "/n": target}
	m := newTestModel(parent, nodes)
	m.loadingCount = 1

	newModel, cmd := m.Update(deleteResultMsg{path: "/n", parentPath: "/"})
	nm := newModel.(Model)

	if nm.statusErr {
		t.Fatal("statusErr = true after a successful delete, want false")
	}
	if !strings.Contains(nm.status, "/n") {
		t.Fatalf("status = %q, want it to mention the deleted path", nm.status)
	}
	if _, ok := nm.nodes["/n"]; ok {
		t.Fatal("deleted node still present in nodes map")
	}
	if parent.loaded {
		t.Fatal("parent.loaded = true after a successful delete under it, want the cache invalidated (false)")
	}
	if cmd == nil {
		t.Fatal("Update(deleteResultMsg success) with an expanded parent returned a nil cmd, want a re-fetch command")
	}
}

func TestUpdateDeleteResultSuccessOnOpenDetailReturnsToTree(t *testing.T) {
	root := &node{path: "/n", name: "n"}
	nodes := map[string]*node{"/n": root}
	m := newTestModel(root, nodes)
	m.focus = focusData
	m.detail = detailState{path: "/n"}
	m.loadingCount = 1

	newModel, _ := m.Update(deleteResultMsg{path: "/n", parentPath: ""})
	nm := newModel.(Model)

	if nm.mode != modeTree {
		t.Fatalf("mode after deleting the node whose detail was open = %v, want modeTree", nm.mode)
	}
}

func TestUpdateDeleteResultErrorNotEmptyDoesNotRemoveNodeNorCrash(t *testing.T) {
	parent := &node{path: "/", name: "/", expanded: true, loaded: true}
	target := &node{path: "/n", name: "n", parent: parent}
	child := &node{path: "/n/c", name: "c", parent: target}
	target.children = []*node{child}
	parent.children = []*node{target}
	nodes := map[string]*node{"/": parent, "/n": target, "/n/c": child}
	m := newTestModel(parent, nodes)
	m.loadingCount = 1

	wantErr := errors.New("zk: delete /n: zk: node has children")
	newModel, cmd := m.Update(deleteResultMsg{path: "/n", parentPath: "/", err: wantErr})
	nm := newModel.(Model)

	if cmd != nil {
		t.Fatal("Update(deleteResultMsg zk.ErrNotEmpty) returned a non-nil cmd, want nil")
	}
	if !nm.statusErr {
		t.Fatal("statusErr = false after a zk.ErrNotEmpty delete failure, want true")
	}
	if !strings.Contains(nm.status, "node has children") {
		t.Fatalf("status = %q, want the real server error surfaced verbatim", nm.status)
	}
	if _, ok := nm.nodes["/n"]; !ok {
		t.Fatal("/n removed from nodes after a FAILED delete, want it left in place")
	}
	if _, ok := nm.nodes["/n/c"]; !ok {
		t.Fatal("/n/c (child of the non-empty node) disappeared after a failed delete — no recursive deletion must ever happen, not even client-side bookkeeping")
	}
	if parent.loaded != true {
		t.Fatal("parent cache was invalidated after a FAILED delete, want it left untouched")
	}
}
