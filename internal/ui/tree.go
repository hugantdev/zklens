package ui

// node is one znode in the in-memory tree the user has navigated into so
// far. Children are fetched lazily: node.children stays nil (and loaded
// stays false) until the node is expanded for the first time.
type node struct {
	path   string
	name   string
	depth  int
	parent *node

	expanded bool
	loading  bool
	loaded   bool
	err      error
	children []*node

	// watching is true while a live children-watch is armed for this
	// node (toggled with 'w' in the tree). It is purely a UI-visible
	// flag; the actual watch lifecycle lives in Model's watch handlers.
	watching        bool
	watchGeneration uint64
	watchCancel     chan struct{}

	// childCount is the node's Stat.NumChildren, fetched separately right
	// after the parent lists its children — ZooKeeper's Children call returns
	// only names, so listing a node says nothing about whether its children
	// have children of their own. countKnown distinguishes "no children" from
	// "not asked yet", since the zero value has to mean the latter.
	childCount int
	countKnown bool
}

// Tree markers. All are one cell wide, so the depth indent stays aligned
// whatever a row shows.
const (
	markerExpanded  = "▾"
	markerCollapsed = "▸"
	markerLeaf      = "·"
	markerNodeError = "!"
)

// marker picks the glyph for n's current state. A node whose children have
// been listed is authoritative about itself; otherwise the separately fetched
// childCount answers, and until that arrives the node is drawn as if it might
// have children — the collapsed marker is the safe guess, since offering to
// expand something empty is a smaller lie than hiding a subtree.
func (n *node) marker() string {
	switch {
	case n.err != nil:
		return markerNodeError
	case n.loaded && len(n.children) == 0:
		return markerLeaf
	case n.expanded:
		return markerExpanded
	case n.countKnown && n.childCount == 0:
		return markerLeaf
	default:
		return markerCollapsed
	}
}

// childPath joins a parent znode path with a child name, respecting
// ZooKeeper's rule that the root path is "/" itself rather than "" and
// that no other path may end in "/".
func childPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// visibleNodes flattens the tree starting at root into the sequence of
// nodes currently shown to the user: root, then each expanded node's
// children in order, recursively. Collapsed nodes contribute only
// themselves, not their (possibly already-loaded) children.
func visibleNodes(root *node) []*node {
	var out []*node
	var walk func(n *node)
	walk = func(n *node) {
		out = append(out, n)
		if !n.expanded {
			return
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	return out
}
