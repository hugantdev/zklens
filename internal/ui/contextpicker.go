package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ContextItem is one entry in the startup context picker: a context's
// name and the connection details shown next to it.
type ContextItem struct {
	Name   string
	Detail string
}

// ContextPicker is the interactive context-selection screen shown at
// startup when the config file declares several contexts and none was
// selected via --context, ZKLENS_CONTEXT, or explicit connection settings.
// Enter confirms the highlighted context; Esc or q cancels startup.
type ContextPicker struct {
	items     []ContextItem
	cursor    int
	selected  int
	done      bool
	cancelled bool
}

// NewContextPicker builds a picker over the given contexts (in display
// order).
func NewContextPicker(items []ContextItem) ContextPicker {
	return ContextPicker{items: items}
}

// Result reports which context the user picked and whether they confirmed
// at all. It is only meaningful once the picker program has finished.
func (p ContextPicker) Result() (int, bool) {
	return p.selected, !p.cancelled
}

func (p ContextPicker) Init() tea.Cmd { return nil }

func (p ContextPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if p.done {
		return p, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		p.cancelled = true
		p.done = true
		return p, tea.Quit
	case "enter":
		p.selected = p.cursor
		p.done = true
		return p, tea.Quit
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	}
	return p, nil
}

func (p ContextPicker) View() string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("zklens — select a context"))
	b.WriteString("\n\n")
	for i, item := range p.items {
		name := "  " + item.Name
		if i == p.cursor {
			name = styles.NodeSelected.Render("▸ " + item.Name)
		} else {
			name = styles.Node.Render(name)
		}
		fmt.Fprintf(&b, "%s\n", name)
		if item.Detail != "" {
			b.WriteString(styles.Muted.Render("    " + item.Detail))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(styles.HelpKey.Render("↑/↓") + styles.HelpDesc.Render(" move  ") +
		styles.HelpKey.Render("enter") + styles.HelpDesc.Render(" connect  ") +
		styles.HelpKey.Render("esc") + styles.HelpDesc.Render(" cancel"))
	b.WriteString("\n")
	return b.String()
}
