package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

func renderTreeItem(n *node) string {
	l := list.New([]list.Item{treeItem{n: n}}, treeDelegate{}, 80, 10)
	var buf bytes.Buffer
	treeDelegate{}.Render(&buf, l, 0, treeItem{n: n})
	return buf.String()
}

func TestTreeDelegateShowsWatchMarkerOnlyWhenWatching(t *testing.T) {
	watched := &node{path: "/a", name: "a", watching: true}
	unwatched := &node{path: "/b", name: "b", watching: false}

	gotWatched := renderTreeItem(watched)
	if !strings.Contains(gotWatched, "◆") {
		t.Fatalf("render of a watching node = %q, want it to contain the watch marker ◆", gotWatched)
	}

	gotUnwatched := renderTreeItem(unwatched)
	if strings.Contains(gotUnwatched, "◆") {
		t.Fatalf("render of a non-watching node = %q, want no watch marker", gotUnwatched)
	}
}

// The tree lives in a narrow column, and list.View pads rows but never trims
// them: an untruncated row would wrap and push the panel out of its box.
func TestTreeDelegateTruncatesToTheListWidth(t *testing.T) {
	n := &node{path: "/x", name: strings.Repeat("nombre-largo-", 20), depth: 3}

	l := list.New([]list.Item{treeItem{n: n}}, treeDelegate{}, 24, 10)
	var buf bytes.Buffer
	treeDelegate{}.Render(&buf, l, 0, treeItem{n: n})

	got := buf.String()
	if w := lipgloss.Width(got); w > 24 {
		t.Fatalf("rendered row width = %d, want at most the list's 24 (%q)", w, got)
	}
	if !strings.Contains(got, "▸") {
		t.Fatalf("row = %q, want the expand marker kept when truncating", got)
	}
	if !strings.HasPrefix(got, "      ") {
		t.Fatalf("row = %q, want the depth indent kept when truncating", got)
	}
}

// The watch marker is appended after styling, so truncation has to leave room
// for it or it would push the row past the panel width.
func TestTreeDelegateKeepsWatchMarkerWithinWidth(t *testing.T) {
	n := &node{path: "/x", name: strings.Repeat("z", 60), watching: true}

	l := list.New([]list.Item{treeItem{n: n}}, treeDelegate{}, 20, 10)
	var buf bytes.Buffer
	treeDelegate{}.Render(&buf, l, 0, treeItem{n: n})

	got := buf.String()
	if w := lipgloss.Width(got); w > 20 {
		t.Fatalf("rendered row width = %d, want at most the list's 20 (%q)", w, got)
	}
	if !strings.Contains(got, "◆") {
		t.Fatalf("row = %q, want the watch marker still present", got)
	}
}

// The markers are coloured apart so the shape of a subtree reads at a glance.
func TestTreeDelegateColoursMarkersByState(t *testing.T) {
	if styles.MarkerCollapsed.GetForeground() == styles.MarkerExpanded.GetForeground() {
		t.Error("MarkerCollapsed and MarkerExpanded share a colour, want them told apart")
	}
	if styles.MarkerLeaf.GetForeground() == styles.MarkerCollapsed.GetForeground() {
		t.Error("MarkerLeaf and MarkerCollapsed share a colour, want them told apart")
	}

	leaf := &node{path: "/a", name: "a", loaded: true}
	branch := &node{path: "/b", name: "b", loaded: true, children: []*node{{path: "/b/c"}}}

	if got := markerStyle(leaf); got.GetForeground() != styles.MarkerLeaf.GetForeground() {
		t.Error("a leaf is not drawn with the leaf marker colour")
	}
	if got := markerStyle(branch); got.GetForeground() != styles.MarkerCollapsed.GetForeground() {
		t.Error("a collapsed branch is not drawn with the collapsed marker colour")
	}
}

func TestTreeDelegateShowsTheMarkerForEachState(t *testing.T) {
	cases := []struct {
		name string
		n    *node
		want string
	}{
		{"hoja", &node{path: "/a", name: "a", loaded: true}, markerLeaf},
		{"colapsado", &node{path: "/b", name: "b", loaded: true, children: []*node{{path: "/b/c"}}}, markerCollapsed},
		{"desplegado", &node{path: "/c", name: "c", loaded: true, expanded: true, children: []*node{{path: "/c/d"}}}, markerExpanded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTreeItem(tc.n)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("render = %q, want the marker %q", got, tc.want)
			}
		})
	}
}

// Truncation has to survive the marker and label being styled separately.
func TestTreeDelegateTruncatesWithASeparatelyStyledMarker(t *testing.T) {
	n := &node{path: "/x", name: strings.Repeat("largo-", 30), depth: 2, loaded: true}

	l := list.New([]list.Item{treeItem{n: n}}, treeDelegate{}, 22, 10)
	var buf bytes.Buffer
	treeDelegate{}.Render(&buf, l, 0, treeItem{n: n})

	got := buf.String()
	if w := lipgloss.Width(got); w > 22 {
		t.Fatalf("row width = %d, want at most 22 (%q)", w, got)
	}
	if !strings.Contains(got, markerLeaf) {
		t.Fatalf("row = %q, want the leaf marker kept when truncating", got)
	}
}
