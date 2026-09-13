package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// --- panel geometry ---------------------------------------------------------

// A panel must occupy exactly the cells it was given, border included:
// JoinHorizontal pads the shorter column, so one panel that renders a cell
// too wide silently pushes the whole layout out of alignment.
func TestPanelRendersExactlyTheRequestedSize(t *testing.T) {
	cases := []struct{ name, content string }{
		{"empty", ""},
		{"short", "hola"},
		{"multiline", "una\ndos\ntres"},
		{"line wider than the box", strings.Repeat("x", 200)},
		{"more lines than the box", strings.Repeat("linea\n", 50)},
		{"styled content", styles.NodeSelected.Render("seleccionado")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := panel("Título", tc.content, 40, 10, false)
			if w := lipgloss.Width(got); w != 40 {
				t.Errorf("lipgloss.Width(panel) = %d, want 40", w)
			}
			if h := lipgloss.Height(got); h != 10 {
				t.Errorf("lipgloss.Height(panel) = %d, want 10", h)
			}
		})
	}
}

func TestPanelShowsItsTitleAndDistinguishesFocus(t *testing.T) {
	unfocused := panel("Árbol", "contenido", 30, 8, false)
	focused := panel("Árbol", "contenido", 30, 8, true)

	if !strings.Contains(unfocused, "Árbol") {
		t.Fatalf("panel = %q, want the title rendered", unfocused)
	}
	if !strings.Contains(unfocused, "contenido") {
		t.Fatalf("panel = %q, want the content rendered", unfocused)
	}
	if lipgloss.Width(focused) != lipgloss.Width(unfocused) {
		t.Fatal("focus changed the panel width, want highlighting to be purely cosmetic")
	}
	if lipgloss.Height(focused) != lipgloss.Height(unfocused) {
		t.Fatal("focus changed the panel height, want highlighting to be purely cosmetic")
	}

	// The highlight itself is colour, which lipgloss strips when tests run
	// without a TTY, so assert on the styles rather than the rendered bytes.
	if styles.PanelBorderFocused.GetBorderTopForeground() == styles.PanelBorder.GetBorderTopForeground() {
		t.Error("PanelBorderFocused and PanelBorder share a border colour, want the focused panel to stand out")
	}
	if styles.PanelTitleFocused.GetForeground() == styles.PanelTitle.GetForeground() {
		t.Error("PanelTitleFocused and PanelTitle share a colour, want the focused title to stand out")
	}
}

// A title longer than the box must be cut, not wrapped onto a second row —
// wrapping would push the content down and break the height contract.
func TestPanelTruncatesAnOverlongTitle(t *testing.T) {
	got := panel(strings.Repeat("largo-", 30), "x", 20, 6, false)
	if w := lipgloss.Width(got); w != 20 {
		t.Fatalf("lipgloss.Width(panel) = %d, want 20", w)
	}
	if h := lipgloss.Height(got); h != 6 {
		t.Fatalf("lipgloss.Height(panel) = %d, want 6", h)
	}
}

func TestPanelTooSmallRendersNothing(t *testing.T) {
	if got := panel("t", "c", 2, 10, false); got != "" {
		t.Errorf("panel(width=2) = %q, want an empty string", got)
	}
	if got := panel("t", "c", 40, panelChrome-1, false); got != "" {
		t.Errorf("panel(height=%d) = %q, want an empty string", panelChrome-1, got)
	}
}

// --- fitBlock / truncCells --------------------------------------------------

func TestFitBlockPadsAndTruncatesToExactCells(t *testing.T) {
	got := fitBlock("ab\n"+strings.Repeat("z", 20), 5, 4)

	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("fitBlock produced %d lines, want 4", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 5 {
			t.Errorf("line %d width = %d, want 5 (%q)", i, w, line)
		}
	}
}

// Padding after a truncated styled line must not inherit that line's colors,
// or the background bleeds across the panel and onto its border.
func TestFitLineClosesStyleBeforePadding(t *testing.T) {
	// Written as a raw SGR run rather than via a lipgloss style: lipgloss
	// strips colour when tests run without a TTY, which would make this pass
	// vacuously.
	got := fitLine("\x1b[41mab", 10)

	if w := lipgloss.Width(got); w != 10 {
		t.Fatalf("fitLine width = %d, want 10", w)
	}
	if !strings.HasSuffix(got, strings.Repeat(" ", 8)) {
		t.Fatalf("fitLine = %q, want it to end in plain padding spaces", got)
	}
	if !strings.Contains(got, "\x1b[m") {
		t.Fatalf("fitLine = %q, want an explicit style reset before the padding", got)
	}
}

func TestTruncCellsMarksTheCut(t *testing.T) {
	if got := truncCells("corto", 10); got != "corto" {
		t.Errorf("truncCells(fits) = %q, want it returned unchanged", got)
	}
	got := truncCells("un-nombre-de-znode-muy-largo", 10)
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("truncCells width = %d, want at most 10", w)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncCells(too long) = %q, want an ellipsis marking the cut", got)
	}
}

// --- overlay ----------------------------------------------------------------

func TestOverlayKeepsBackgroundSizeAndSurroundings(t *testing.T) {
	bg := panel("Fondo", strings.Repeat("fondo fondo fondo\n", 10), 40, 12, false)
	fg := panel("Modal", "confirmar", 20, 5, true)

	got := overlay(bg, fg, 10, 3)

	if w := lipgloss.Width(got); w != 40 {
		t.Errorf("overlay width = %d, want the background's 40", w)
	}
	if h := lipgloss.Height(got); h != 12 {
		t.Errorf("overlay height = %d, want the background's 12", h)
	}
	if !strings.Contains(got, "confirmar") {
		t.Error("overlay dropped the foreground content")
	}
	// Rows the modal does not cover must survive untouched.
	first := strings.Split(got, "\n")[0]
	if first != strings.Split(bg, "\n")[0] {
		t.Error("overlay modified a row above the foreground")
	}
}

// The whole point of splicing rather than lipgloss.Place: what is left and
// right of the modal on its own rows stays visible.
func TestOverlayKeepsBackgroundBesideTheForeground(t *testing.T) {
	bg := strings.Repeat("IZQUIERDA-----------DERECHA\n", 5)
	bg = strings.TrimSuffix(bg, "\n")

	got := overlay(bg, "XXXX", 12, 2)

	row := strings.Split(got, "\n")[2]
	if !strings.Contains(row, "IZQUIERDA") {
		t.Errorf("row = %q, want the background left of the overlay kept", row)
	}
	if !strings.Contains(row, "DERECHA") {
		t.Errorf("row = %q, want the background right of the overlay kept", row)
	}
	if !strings.Contains(row, "XXXX") {
		t.Errorf("row = %q, want the overlay spliced in", row)
	}
	if w := lipgloss.Width(row); w != lipgloss.Width("IZQUIERDA-----------DERECHA") {
		t.Errorf("row width = %d, want the background's width unchanged", w)
	}
}

func TestOverlayIgnoresRowsOutsideTheBackground(t *testing.T) {
	bg := "uno\ndos"
	if got := overlay(bg, "X", 0, 99); got != bg {
		t.Errorf("overlay below the background = %q, want it unchanged", got)
	}
	if got := overlay(bg, "X", 0, -5); !strings.Contains(got, "uno") {
		t.Errorf("overlay above the background lost a row: %q", got)
	}
}

// --- clamp ------------------------------------------------------------------

func TestClamp(t *testing.T) {
	cases := []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{99, 0, 10, 10},
		{5, 10, 3, 10}, // no room for the minimum: lo wins
	}
	for _, c := range cases {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}
