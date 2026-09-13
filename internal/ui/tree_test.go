package ui

import (
	"errors"
	"reflect"
	"testing"
)

func TestChildPath(t *testing.T) {
	cases := []struct {
		parent, name, want string
	}{
		{"/", "foo", "/foo"},
		{"/foo", "bar", "/foo/bar"},
		{"/foo/bar", "baz", "/foo/bar/baz"},
	}
	for _, tc := range cases {
		if got := childPath(tc.parent, tc.name); got != tc.want {
			t.Errorf("childPath(%q, %q) = %q, want %q", tc.parent, tc.name, got, tc.want)
		}
	}
}

func namesOf(nodes []*node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.path
	}
	return names
}

func TestVisibleNodesRootOnlyWhenCollapsed(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: false}
	root.children = []*node{{path: "/a", name: "a", parent: root}}

	got := namesOf(visibleNodes(root))
	want := []string{"/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleNodes() = %v, want %v", got, want)
	}
}

func TestVisibleNodesExpandedIncludesDirectChildren(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	a := &node{path: "/a", name: "a", parent: root}
	b := &node{path: "/b", name: "b", parent: root}
	root.children = []*node{a, b}

	got := namesOf(visibleNodes(root))
	want := []string{"/", "/a", "/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleNodes() = %v, want %v", got, want)
	}
}

func TestVisibleNodesCollapsedSubtreeHidesGrandchildren(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	a := &node{path: "/a", name: "a", parent: root, expanded: false}
	a1 := &node{path: "/a/1", name: "1", parent: a}
	a.children = []*node{a1}
	b := &node{path: "/b", name: "b", parent: root, expanded: true}
	b1 := &node{path: "/b/1", name: "1", parent: b}
	b.children = []*node{b1}
	root.children = []*node{a, b}

	got := namesOf(visibleNodes(root))
	// a is collapsed: a1 must not appear. b is expanded: b1 must appear.
	want := []string{"/", "/a", "/b", "/b/1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleNodes() = %v, want %v", got, want)
	}
}

func TestVisibleNodesDeeplyNestedExpansion(t *testing.T) {
	root := &node{path: "/", name: "/", expanded: true}
	a := &node{path: "/a", name: "a", parent: root, expanded: true}
	a1 := &node{path: "/a/1", name: "1", parent: a, expanded: true}
	a1x := &node{path: "/a/1/x", name: "x", parent: a1}
	a1.children = []*node{a1x}
	a.children = []*node{a1}
	root.children = []*node{a}

	got := namesOf(visibleNodes(root))
	want := []string{"/", "/a", "/a/1", "/a/1/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleNodes() = %v, want %v", got, want)
	}
}

// --- markers ----------------------------------------------------------------

func TestNodeMarkerReflectsChildState(t *testing.T) {
	cases := []struct {
		name string
		n    *node
		want string
	}{
		{
			"sin explorar todavía",
			&node{path: "/a"},
			markerCollapsed,
		},
		{
			"contado y vacío",
			&node{path: "/a", countKnown: true, childCount: 0},
			markerLeaf,
		},
		{
			"contado y con hijos",
			&node{path: "/a", countKnown: true, childCount: 3},
			markerCollapsed,
		},
		{
			// Listing the node is authoritative, whatever the count said.
			"listado y vacío",
			&node{path: "/a", loaded: true},
			markerLeaf,
		},
		{
			"listado, con hijos, colapsado",
			&node{path: "/a", loaded: true, children: []*node{{path: "/a/b"}}},
			markerCollapsed,
		},
		{
			"listado, con hijos, desplegado",
			&node{path: "/a", loaded: true, expanded: true, children: []*node{{path: "/a/b"}}},
			markerExpanded,
		},
		{
			// An expanded node that turned out to be empty is a leaf, not an
			// open branch.
			"desplegado pero vacío",
			&node{path: "/a", loaded: true, expanded: true},
			markerLeaf,
		},
		{
			"error gana sobre todo lo demás",
			&node{path: "/a", loaded: true, expanded: true, err: errors.New("boom")},
			markerNodeError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.n.marker(); got != tc.want {
				t.Fatalf("marker() = %q, want %q", got, tc.want)
			}
		})
	}
}
