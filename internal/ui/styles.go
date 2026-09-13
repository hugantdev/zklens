package ui

import "github.com/charmbracelet/lipgloss"

// styles centralizes every lipgloss style used by the tree view so callers
// never build ad-hoc styles inline.
var styles = struct {
	Title             lipgloss.Style
	Node              lipgloss.Style
	NodeSelected      lipgloss.Style
	NodeError         lipgloss.Style
	Marker            lipgloss.Style
	Muted             lipgloss.Style
	StatusBar         lipgloss.Style
	StatusError       lipgloss.Style
	StatusConnOK      lipgloss.Style
	StatusConnWarn    lipgloss.Style
	StatusConnBad     lipgloss.Style
	StatusConnUnknown lipgloss.Style
	SectionTitle      lipgloss.Style
	Label             lipgloss.Style
	InputPrompt       lipgloss.Style
	DialogDanger      lipgloss.Style

	PanelBorder        lipgloss.Style
	PanelBorderFocused lipgloss.Style
	PanelTitle         lipgloss.Style
	PanelTitleFocused  lipgloss.Style
	ModalBorder        lipgloss.Style
	ModalBorderDanger  lipgloss.Style
	Breadcrumb         lipgloss.Style
	Placeholder        lipgloss.Style
	MarkerExpanded     lipgloss.Style
	MarkerCollapsed    lipgloss.Style
	MarkerLeaf         lipgloss.Style
	MarkerNodeError    lipgloss.Style
	HelpKey            lipgloss.Style
	HelpDesc           lipgloss.Style
	HelpSep            lipgloss.Style
}{
	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#1F1F1F", Dark: "#E4E4E4"}).
		Padding(0, 1),

	Node: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#1F1F1F", Dark: "#DDDDDD"}),

	NodeSelected: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
		Background(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#5A56E0"}),

	NodeError: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#B8280A", Dark: "#FF6B57"}),

	Marker: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}),

	Muted: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6B6B6B"}).
		Italic(true),

	// StatusBar and StatusError style the status line's message text directly
	// on the terminal's own background — no painted bar — the same minimal
	// chrome the help bar already uses just above it.
	StatusBar: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#1F1F1F", Dark: "#DDDDDD"}),

	StatusError: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#B8280A", Dark: "#FF6B57"}),

	// The four StatusConn* styles color the small connection-health dot that
	// sits at the right edge of the status line, independently of whatever
	// transient message currently occupies the left side.
	StatusConnOK: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#1F7A3D", Dark: "#4ADE80"}),

	StatusConnWarn: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9A6B00", Dark: "#FBBF24"}),

	StatusConnBad: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#B8280A", Dark: "#FF6B57"}),

	StatusConnUnknown: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9A9A9A", Dark: "#6B6B6B"}),

	SectionTitle: lipgloss.NewStyle().
		Bold(true).
		Underline(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#1F1F1F", Dark: "#E4E4E4"}),

	Label: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}),

	// InputPrompt styles the leading marker of a bubbles/textinput field
	// (create/edit forms), so every text field in the app looks the same.
	InputPrompt: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}),

	// DialogDanger marks an irreversible-action warning, e.g. the delete
	// confirmation prompt — heavier than NodeError so it reads as "read
	// this before pressing a key", not just an inline error.
	DialogDanger: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
		Background(lipgloss.AdaptiveColor{Light: "#B8280A", Dark: "#B8280A"}).
		Padding(0, 1),

	// PanelBorder and PanelBorderFocused deliberately set only border
	// properties: panel content arrives already styled, and a wrapper that set
	// Foreground/Background would be cleared by the first reset code inside
	// that content (the same trap delegate.go's Render documents). They also
	// set no Width/Height — panel() sizes the content itself, so applyBorder
	// has nothing left to re-measure.
	PanelBorder: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#C0C0C0", Dark: "#4A4A4A"}),

	PanelBorderFocused: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}),

	PanelTitle: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}),

	PanelTitleFocused: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}),

	ModalBorder: lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}).
		Padding(0, 1),

	// ModalBorderDanger frames the delete confirmation, so the frame itself
	// carries the same warning the prompt text does.
	ModalBorderDanger: lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#B8280A", Dark: "#FF6B57"}).
		Padding(0, 1),

	Breadcrumb: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}),

	Placeholder: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6B6B6B"}).
		Italic(true),

	HelpKey: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}),

	HelpDesc: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}),

	HelpSep: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#C0C0C0", Dark: "#4A4A4A"}),

	// The three tree markers are coloured apart so the shape of a subtree
	// reads at a glance: the collapsed marker takes the accent because it is
	// the one that invites a keypress, the expanded marker a second hue to
	// show it is already open, and a leaf stays grey so the eye skips it.
	MarkerCollapsed: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#9C99F7"}),

	MarkerExpanded: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#67E8F9"}),

	MarkerLeaf: lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#9A9A9A", Dark: "#6B6B6B"}),

	MarkerNodeError: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "#B8280A", Dark: "#FF6B57"}),
}
