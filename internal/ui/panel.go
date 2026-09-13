package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// panelChrome is how many rows a panel spends on itself rather than on
// content: the top and bottom border rows. The title rides in the top border,
// so it costs no extra row.
const panelChrome = 2

// panel renders content inside a titled box of exactly width×height cells,
// border included.
//
// The content is normalized to the inner size by fitBlock before it ever
// reaches the border style. That is not belt-and-braces: lipgloss's
// Style.Width only *pads* short lines, and applyBorder then re-measures the
// box from the longest line, so a single over-long line would silently widen
// the whole panel and break the column split. MaxWidth is no help either —
// it is applied after the border and would eat the border itself.
func panel(title, content string, width, height int, focused bool) string {
	if width < 5 || height <= panelChrome {
		return ""
	}

	borderStyle := styles.PanelBorder
	if focused {
		borderStyle = styles.PanelBorderFocused
	}

	innerW := width - 2
	// The top border is drawn by hand so the title can carry its own colour
	// without being nested inside the border style's Render — see delegate.go's
	// Render for why that nesting would break.
	body := borderStyle.BorderTop(false).Render(fitBlock(content, innerW, height-panelChrome))

	return topBorder(title, width, focused) + "\n" + body
}

// topBorder draws a panel's top edge with the title set into it, as
// "╭─ Título ─────╮".
func topBorder(title string, width int, focused bool) string {
	b := lipgloss.RoundedBorder()

	titleStyle, borderStyle := styles.PanelTitle, styles.PanelBorder
	if focused {
		titleStyle, borderStyle = styles.PanelTitleFocused, styles.PanelBorderFocused
	}
	// Reuse the border's colour without its box: Render here draws a plain run
	// of border runes, not a frame.
	edge := lipgloss.NewStyle().Foreground(borderStyle.GetBorderTopForeground())

	// The corners, the one leading rune, and the spaces around the title.
	label := truncCells(title, max(0, width-5))
	fill := width - 5 - lipgloss.Width(label)
	if fill < 0 {
		fill = 0
	}

	// Each segment is rendered on its own and concatenated: styles are never
	// nested, so no inner reset can clear an outer one.
	return edge.Render(b.TopLeft+b.Top) +
		" " + titleStyle.Render(label) + " " +
		edge.Render(strings.Repeat(b.Top, fill)+b.TopRight)
}

// fitBlock normalizes s to exactly w columns by h lines, counting display
// cells rather than bytes so ANSI attributes (several bytes wide, zero cells
// wide) neither inflate nor truncate a line.
func fitBlock(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}

	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}

	out := make([]string, 0, h)
	for _, line := range lines {
		out = append(out, fitLine(line, w))
	}
	blank := strings.Repeat(" ", w)
	for len(out) < h {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}

// fitLine truncates or pads one line to exactly w display cells.
func fitLine(line string, w int) string {
	line = ansi.Truncate(line, w, "")
	pad := w - ansi.StringWidth(line)
	if pad <= 0 {
		return line
	}
	// A truncated line can end in the middle of an SGR run, which would bleed
	// its foreground/background into the padding and onto the border. Close it
	// explicitly before padding.
	if strings.ContainsRune(line, ansiEscape) {
		line += ansi.ResetStyle
	}
	return line + strings.Repeat(" ", pad)
}

const ansiEscape = '\x1b'

// truncCells shortens s to w display cells, marking the cut with an ellipsis.
func truncCells(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// overlay splices fg's lines into bg starting at cell (x, y), leaving the
// rest of each spliced row of bg visible around it — which is what makes a
// modal read as floating over the panels rather than replacing them.
// lipgloss.Place cannot do this: it composes the string onto a block of
// whitespace, erasing whatever was underneath.
func overlay(bg, fg string, x, y int) string {
	if fg == "" {
		return bg
	}
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	fgW := lipgloss.Width(fg)

	for i, fgLine := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bgLine := bgLines[row]

		left := ansi.Truncate(bgLine, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(bgLine, x+fgW, "")

		// Both cuts can land inside an SGR run, so close the style on each
		// side of the splice: without this the modal is painted in the
		// background panel's colors, and the background right of the modal
		// inherits the modal's.
		bgLines[row] = left + ansi.ResetStyle + fgLine + ansi.ResetStyle + right
	}
	return strings.Join(bgLines, "\n")
}

// clamp constrains v to [lo, hi]. A hi below lo means there is not enough
// room for the minimum, and lo wins.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}
