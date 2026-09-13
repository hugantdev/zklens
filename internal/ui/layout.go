package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/lipgloss"
)

// Panel sizing bounds. The minimums are what each column needs before it stops
// being useful; below them the layout degrades to a single panel rather than
// rendering a row of unreadable slivers.
const (
	treePct       = 35
	minTreeOuter  = 18 // 2 border columns + 16 usable
	minRightOuter = 24

	// A right-column panel is worth drawing only if it can show a few rows of
	// content; below that the two panes merge into one instead of becoming
	// slivers showing a couple of Stat fields each.
	minPanelOuter = panelChrome + 5

	// statPrefOuter fits the 11 Stat rows exactly, so the Stat panel never
	// needs scrolling on a normal terminal.
	statPrefOuter = 11 + panelChrome
)

// geometry is the computed size of every panel for the current terminal. All
// dimensions are outer sizes, borders included.
type geometry struct {
	bodyH  int
	treeW  int
	rightW int
	dataH  int
	statH  int // 0 when the right column is too short to split in two

	// singlePanel means there is no room for two columns, so only the focused
	// panel is drawn, full width.
	singlePanel bool
}

// geometry computes the panel sizes without mutating anything, so View and the
// key handlers can ask about the layout without going through layout().
func (m Model) geometry() geometry {
	// Measured from the same string View renders, so the two can never
	// disagree about how many rows the help bar takes.
	g := geometry{bodyH: max(0, m.height-1-lipgloss.Height(m.renderHelp()))} // -1: the status bar

	if m.width < minTreeOuter+minRightOuter {
		g.singlePanel = true
		return g
	}

	g.treeW = clamp(m.width*treePct/100, minTreeOuter, m.width-minRightOuter)
	g.rightW = m.width - g.treeW

	if g.statH = min(statPrefOuter, g.bodyH/2); g.statH < minPanelOuter {
		g.statH = 0
	}
	g.dataH = g.bodyH - g.statH

	return g
}

// layout resizes the list and the viewports to fit the current terminal, and
// re-pushes their content so a resize re-wraps it. It must be called both on a
// window-size change and whenever the help bar expands or collapses, since the
// help's height comes straight out of the panels'.
func (m *Model) layout() {
	g := m.geometry()
	m.help.Width = m.width

	if g.singlePanel {
		inner := max(0, m.width-2)
		m.list.SetSize(inner, max(0, g.bodyH-panelChrome))
		m.dataVP.Width, m.dataVP.Height = inner, max(0, g.bodyH-panelChrome)
		m.statVP.Width, m.statVP.Height = inner, max(0, g.bodyH-panelChrome)
		m.syncDetailViews()
		return
	}

	m.list.SetSize(g.treeW-2, max(0, g.bodyH-panelChrome))
	m.dataVP.Width, m.dataVP.Height = g.rightW-2, max(0, g.dataH-panelChrome)
	m.statVP.Width, m.statVP.Height = g.rightW-2, max(0, g.statH-panelChrome)

	m.syncDetailViews()
}

// newHelp builds the help bar with zklens's palette.
func newHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = styles.HelpKey
	h.Styles.FullKey = styles.HelpKey
	h.Styles.ShortDesc = styles.HelpDesc
	h.Styles.FullDesc = styles.HelpDesc
	h.Styles.ShortSeparator = styles.HelpSep
	h.Styles.FullSeparator = styles.HelpSep
	return h
}

// minBodyH is how many rows the panels keep no matter how tall the expanded
// help would like to be: without a floor, "?" on a short terminal would push
// the panels off the screen entirely.
const minBodyH = 4

// renderHelp renders the help bar clamped to the terminal. bubbles/help sizes
// its short view to Width but lays the full view out in columns that can
// overrun it, and on a short terminal the full view can be taller than the
// screen, so both axes are capped here.
func (m Model) renderHelp() string {
	out := m.help.View(m.keyMap())
	if m.width <= 0 {
		return out
	}

	maxH := max(1, m.height-1-minBodyH)
	lines := strings.Split(out, "\n")
	if len(lines) > maxH {
		lines = lines[:maxH]
	}
	for i, line := range lines {
		lines[i] = truncCells(line, m.width)
	}
	return strings.Join(lines, "\n")
}
