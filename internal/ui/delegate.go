package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// treeItem adapts a *node to bubbles/list's Item interface.
type treeItem struct{ n *node }

func (i treeItem) FilterValue() string { return i.n.path }

// treeDelegate renders one tree row per visible node: indentation for
// depth, an expand/collapse marker, the node name, and an inline
// loading/error suffix when relevant.
type treeDelegate struct{}

func (treeDelegate) Height() int                         { return 1 }
func (treeDelegate) Spacing() int                        { return 0 }
func (treeDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (treeDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ti, ok := item.(treeItem)
	if !ok {
		return
	}
	n := ti.n

	label := n.name
	if n.path == "/" {
		label = "/"
	}

	// Everything after the marker, so the marker can be styled on its own.
	body := " " + label
	switch {
	case n.loading:
		body += "  (loading…)"
	case n.err != nil:
		body += "  — " + n.err.Error()
	}

	indent := strings.Repeat("  ", n.depth) // pure ASCII: len == display width
	marker := n.marker()

	// The tree lives in a narrow column, and list.View pads rows to its width
	// but never trims them: an untruncated row would wrap onto a second line
	// and shove the rest of the panel out of its box.
	avail := m.Width()
	if n.watching {
		avail -= 2 // " ◆"
	}

	labelStyle := styles.Node
	switch {
	case index == m.Index():
		labelStyle = styles.NodeSelected
	case n.err != nil:
		labelStyle = styles.NodeError
	case n.loading:
		labelStyle = styles.Muted
	}

	var rendered string
	switch {
	case avail <= len(indent):
		// So deep that not even the marker fits.
		rendered = labelStyle.Render(truncCells(indent+marker+body, avail))
	case index == m.Index():
		// The selected row keeps one style across the whole line: a marker
		// coloured for its state would sit on the selection background, where
		// grey-on-indigo is barely legible. The marker's shape still says
		// which state the node is in.
		rendered = labelStyle.Render(truncCells(indent+marker+body, avail))
	default:
		// Marker and label are rendered separately and concatenated, never
		// nested: one style's reset code would clear the other's formatting
		// for everything after it.
		rendered = indent +
			markerStyle(n).Render(marker) +
			labelStyle.Render(truncCells(body, avail-len(indent)-1))
	}

	if n.watching {
		// Its own self-contained segment too, for the same reason.
		rendered += " " + styles.Marker.Render("◆")
	}
	fmt.Fprint(w, rendered)
}

// markerStyle colours a tree marker by the state its glyph stands for.
func markerStyle(n *node) lipgloss.Style {
	switch n.marker() {
	case markerNodeError:
		return styles.MarkerNodeError
	case markerExpanded:
		return styles.MarkerExpanded
	case markerLeaf:
		return styles.MarkerLeaf
	default:
		return styles.MarkerCollapsed
	}
}
