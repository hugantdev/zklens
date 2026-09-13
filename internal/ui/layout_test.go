package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func sizedTestModel(t *testing.T, w, h int) Model {
	t.Helper()
	root := &node{path: "/", name: "/", expanded: true, loaded: true}
	child := &node{path: "/hijo", name: "hijo", parent: root}
	root.children = []*node{child}
	nodes := map[string]*node{"/": root, "/hijo": child}

	m := newTestModel(root, nodes)
	m.title = "localhost:2181"
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return newModel.(Model)
}

// --- focus cycling ----------------------------------------------------------

func TestShiftTabCyclesThroughThePanels(t *testing.T) {
	m := sizedTestModel(t, 100, 40)

	for _, want := range []focusArea{focusData, focusStat, focusTree} {
		newModel, cmd := m.Update(keyMsg("shift+tab"))
		m = newModel.(Model)
		if m.focus != want {
			t.Fatalf("focus after shift+tab = %v, want %v", m.focus, want)
		}
		if cmd != nil {
			t.Fatal("shift+tab returned a non-nil cmd, want nil (moving focus must not hit the ensemble)")
		}
	}
}

// Only the focused panel may consume a key, or the tree cursor would move
// while the user is scrolling the data pane.
func TestArrowKeysOnlyDriveTheFocusedPanel(t *testing.T) {
	m := sizedTestModel(t, 100, 40)
	m.detail = detailState{path: "/hijo", data: []byte(strings.Repeat("linea\n", 200))}
	m.syncDetailViews()
	m.list.Select(1)

	m.focus = focusData
	newModel, _ := m.Update(keyMsg("down"))
	nm := newModel.(Model)

	if nm.dataVP.YOffset == 0 {
		t.Error("dataVP did not scroll on 'down' while focused")
	}
	if selectedPath(nm) != "/hijo" {
		t.Errorf("tree selection moved to %q while the data panel had the focus, want it untouched", selectedPath(nm))
	}

	m.focus = focusTree
	before := m.dataVP.YOffset
	newModel, _ = m.Update(keyMsg("down"))
	nm2 := newModel.(Model)

	if nm2.dataVP.YOffset != before {
		t.Error("dataVP scrolled on 'down' while the tree had the focus, want it untouched")
	}
}

// The list's own Quit binding is ("q","esc"), which would kill the app instead
// of returning focus to the tree.
func TestEscInTheTreeDoesNotQuit(t *testing.T) {
	m := sizedTestModel(t, 100, 40)

	_, cmd := m.Update(keyMsg("esc"))
	if cmd != nil {
		t.Fatal("esc with the tree focused returned a non-nil cmd, want nil (it must not quit)")
	}
}

// --- geometry ---------------------------------------------------------------

// JoinHorizontal pads the shorter column, so any drift between the two
// right-hand panel heights shows up as a stair-step between the columns.
func TestGeometryPanelsAlwaysTileTheBodyExactly(t *testing.T) {
	sizes := []struct{ w, h int }{
		{100, 40}, {80, 24}, {200, 60}, {61, 20}, {45, 14}, {120, 12},
	}
	for _, s := range sizes {
		m := sizedTestModel(t, s.w, s.h)
		g := m.geometry()
		if g.singlePanel {
			continue
		}
		if g.treeW+g.rightW != s.w {
			t.Errorf("%dx%d: treeW+rightW = %d, want %d", s.w, s.h, g.treeW+g.rightW, s.w)
		}
		if g.dataH+g.statH != g.bodyH {
			t.Errorf("%dx%d: dataH+statH = %d, want bodyH %d", s.w, s.h, g.dataH+g.statH, g.bodyH)
		}
	}
}

// A short terminal cannot show two useful panes in the right column, so they
// merge into one rather than becoming two unreadable slivers.
func TestGeometryMergesTheRightColumnWhenTooShort(t *testing.T) {
	m := sizedTestModel(t, 100, 12)
	g := m.geometry()

	if g.statH != 0 {
		t.Fatalf("statH = %d on a 12-row terminal, want 0 (merged right column)", g.statH)
	}
	if g.dataH != g.bodyH {
		t.Fatalf("dataH = %d, want the whole body height %d", g.dataH, g.bodyH)
	}
	if got := m.nextFocus(); got != focusData {
		t.Fatalf("nextFocus from the tree = %v, want focusData", got)
	}
	m.focus = focusData
	if got := m.nextFocus(); got != focusTree {
		t.Fatalf("nextFocus from the data panel = %v, want focusTree (no Stat panel to visit)", got)
	}
}

func TestGeometryFallsBackToASinglePanelWhenTooNarrow(t *testing.T) {
	m := sizedTestModel(t, 30, 24)
	if !m.geometry().singlePanel {
		t.Fatal("geometry on a 30-column terminal is not singlePanel, want the two columns collapsed")
	}
}

// --- View -------------------------------------------------------------------

// The rendered screen must fill the terminal exactly: a row that overflows
// scrolls the alt-screen and smears the layout.
func TestViewFitsTheTerminalExactly(t *testing.T) {
	sizes := []struct{ w, h int }{{100, 40}, {80, 24}, {61, 20}, {45, 14}, {30, 24}, {120, 12}, {100, 8}}
	for _, s := range sizes {
		for _, fullHelp := range []bool{false, true} {
			m := sizedTestModel(t, s.w, s.h)
			if fullHelp {
				// The expanded help is several rows tall; the panels have to
				// give up exactly those rows, not push them off screen.
				newModel, _ := m.Update(keyMsg("?"))
				m = newModel.(Model)
			}
			for _, mode := range []viewMode{modeTree, modeConfirmDelete} {
				m.mode = mode
				m.del = deleteState{target: &node{path: "/hijo"}}

				got := m.View()
				if w := lipgloss.Width(got); w > s.w {
					t.Errorf("%dx%d fullHelp=%v mode=%v: View width = %d, want at most %d", s.w, s.h, fullHelp, mode, w, s.w)
				}
				if h := lipgloss.Height(got); h > s.h {
					t.Errorf("%dx%d fullHelp=%v mode=%v: View height = %d, want at most %d", s.w, s.h, fullHelp, mode, h, s.h)
				}
			}
		}
	}
}

func TestViewShowsAllThreePanelsAndTheHelpBar(t *testing.T) {
	m := sizedTestModel(t, 100, 40)
	got := m.View()

	for _, want := range []string{"Tree", "Data", "Stat", "localhost:2181"} {
		if !strings.Contains(got, want) {
			t.Errorf("View is missing %q", want)
		}
	}
	// The panels are populated only on demand, so before tab they must say so
	// rather than sit blank.
	if !strings.Contains(got, "tab") {
		t.Error("View does not show the placeholder hinting that tab loads a node")
	}
}

// The breadcrumb carries the full path, which the narrow tree column truncates.
func TestDataPanelTitleCarriesThePathAsABreadcrumb(t *testing.T) {
	m := sizedTestModel(t, 100, 40)
	if got := m.dataTitle(); got != "Data" {
		t.Errorf("dataTitle with nothing loaded = %q, want a bare \"Data\"", got)
	}

	m.detail = detailState{path: "/a/b/c/configuracion"}
	if got := m.dataTitle(); !strings.Contains(got, "/a/b/c/configuracion") {
		t.Errorf("dataTitle = %q, want the full path as a breadcrumb", got)
	}
}

// --- help bar ---------------------------------------------------------------

func TestHelpToggleShrinksThePanels(t *testing.T) {
	m := sizedTestModel(t, 100, 40)
	shortBody := m.geometry().bodyH

	newModel, cmd := m.Update(keyMsg("?"))
	nm := newModel.(Model)
	if cmd != nil {
		t.Fatal("'?' returned a non-nil cmd, want nil")
	}
	if !nm.help.ShowAll {
		t.Fatal("help.ShowAll = false after '?', want the expanded help")
	}

	fullBody := nm.geometry().bodyH
	if fullBody >= shortBody {
		t.Fatalf("bodyH = %d with the expanded help, want less than %d — the panels must give up the rows it takes", fullBody, shortBody)
	}
	if nm.dataVP.Height != fullBody-nm.geometry().statH-panelChrome {
		t.Error("dataVP was not resized when the help expanded")
	}

	newModel, _ = nm.Update(keyMsg("?"))
	if got := newModel.(Model).geometry().bodyH; got != shortBody {
		t.Fatalf("bodyH after collapsing the help = %d, want the original %d", got, shortBody)
	}
}

func TestHelpListsKeysForTheFocusedPanel(t *testing.T) {
	m := sizedTestModel(t, 100, 40)

	tree := m.keyMap().ShortHelp()
	m.focus = focusData
	data := m.keyMap().ShortHelp()

	if len(tree) == len(data) && &tree[0] == &data[0] {
		t.Fatal("the help bar lists the same keys regardless of focus")
	}

	has := func(bs []key.Binding, k string) bool {
		for _, b := range bs {
			if b.Help().Key == k {
				return true
			}
		}
		return false
	}
	if !has(tree, "n") {
		t.Error("tree help does not list 'n' (new child)")
	}
	if has(data, "n") {
		t.Error("data-panel help lists 'n', which only works on the tree")
	}

	m.mode = modeConfirmDelete
	if !has(m.keyMap().ShortHelp(), "y") {
		t.Error("delete-confirmation help does not list the confirm key")
	}
}

// --- status bar layout ------------------------------------------------------

// dotWidth is how many display cells the connection dot itself takes, derived
// from the real style rather than hard-coded, so the test still holds if the
// glyph ever changes.
func dotWidth() int {
	return lipgloss.Width(connIndicatorStyle(connOK).Render("●"))
}

// The connection dot is meant to be readable at a glance in a fixed spot, so
// it must always sit at the right edge of the line, however short the
// message on the left is.
func TestRenderStatusPinsTheConnDotToTheRightEdge(t *testing.T) {
	m := sizedTestModel(t, 60, 20)
	m.status = "ok"
	m.statusErr = false
	m.conn = connOK
	m.loadingCount = 0

	line := m.renderStatus()
	plain := ansi.Strip(line)

	if got := lipgloss.Width(line); got != m.width {
		t.Fatalf("renderStatus width = %d, want exactly m.width = %d", got, m.width)
	}
	if !strings.HasSuffix(plain, "●") {
		t.Fatalf("renderStatus = %q, want it to end with the connection dot", plain)
	}
	if !strings.HasPrefix(plain, "ok") {
		t.Fatalf("renderStatus = %q, want it to start with the status message", plain)
	}
	if !strings.Contains(plain, "  ") {
		t.Fatalf("renderStatus = %q, want at least a gap of spaces between the short message and the dot", plain)
	}
}

// A message longer than the available width must be truncated, but the dot
// must still land exactly at the right edge — the truncation must never
// "push" the dot off screen or eat into its column.
func TestRenderStatusTruncatesALongMessageWithoutMovingTheDot(t *testing.T) {
	m := sizedTestModel(t, 30, 20)
	m.status = strings.Repeat("x", 100)
	m.statusErr = false
	m.conn = connWarn
	m.loadingCount = 0

	line := m.renderStatus()
	plain := ansi.Strip(line)

	if got := lipgloss.Width(line); got != m.width {
		t.Fatalf("renderStatus width = %d, want exactly m.width = %d", got, m.width)
	}
	if !strings.HasSuffix(plain, "●") {
		t.Fatalf("renderStatus = %q, want it to still end with the connection dot", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("renderStatus = %q, want the overlong message truncated with an ellipsis", plain)
	}
	if strings.Contains(plain, strings.Repeat("x", 100)) {
		t.Fatalf("renderStatus = %q, the full 100-char message was not truncated", plain)
	}
}

// Degenerate widths (no terminal at all, or barely enough for the dot) must
// not panic and must not overflow the available width.
func TestRenderStatusHandlesDegenerateWidths(t *testing.T) {
	dotW := dotWidth()

	t.Run("zero width", func(t *testing.T) {
		m := sizedTestModel(t, 60, 20)
		m.width = 0
		m.status = "algo"
		m.conn = connOK

		plain := ansi.Strip(m.renderStatus())
		if strings.Contains(plain, "●") {
			t.Fatalf("renderStatus at width 0 = %q, want no room wasted on the dot", plain)
		}
		if !strings.Contains(plain, "algo") {
			t.Fatalf("renderStatus at width 0 = %q, want the message rendered anyway (nothing else to show)", plain)
		}
	})

	t.Run("width equal to the dot's own width", func(t *testing.T) {
		m := sizedTestModel(t, 60, 20)
		m.width = dotW
		m.status = "un mensaje que no cabe"
		m.conn = connBad

		line := m.renderStatus()
		plain := ansi.Strip(line)

		if got := lipgloss.Width(line); got > m.width {
			t.Fatalf("renderStatus width = %d, want at most m.width = %d", got, m.width)
		}
		if plain != "●" {
			t.Fatalf("renderStatus at width %d = %q, want just the dot (no room for any message)", m.width, plain)
		}
	})
}

// The four connection-health styles must remain visually distinct from one
// another; comparing rendered bytes is unreliable since lipgloss drops color
// codes when there's no TTY (as in these tests), so the styles themselves are
// compared instead.
func TestStatusConnStylesAreVisuallyDistinct(t *testing.T) {
	all := map[string]lipgloss.Style{
		"OK":      styles.StatusConnOK,
		"Warn":    styles.StatusConnWarn,
		"Bad":     styles.StatusConnBad,
		"Unknown": styles.StatusConnUnknown,
	}
	for aName, a := range all {
		for bName, b := range all {
			if aName == bName {
				continue
			}
			if a.GetForeground() == b.GetForeground() {
				t.Errorf("StatusConn%s and StatusConn%s share the same foreground color, want distinct colors so the dot's meaning is visible at a glance", aName, bName)
			}
		}
	}
}
