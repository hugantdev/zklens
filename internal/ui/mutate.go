package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hugantdev/zklens/internal/zk"
)

const editCharLimit = 4096

// --- create -----------------------------------------------------------------

// createStep tracks progress through the create-child wizard: name, then
// initial data, then the znode type.
type createStep int

const (
	createStepName createStep = iota
	createStepData
	createStepMode
)

// createState is the transient state of the "create a child znode" flow,
// entered from the tree on a selected parent node.
type createState struct {
	parent    *node
	step      createStep
	name      textinput.Model
	data      textinput.Model
	modeIndex int
}

// createModeOption pairs a zk.CreateMode with the label shown in the
// mode-picker step.
type createModeOption struct {
	mode  zk.CreateMode
	label string
}

var createModeOptions = []createModeOption{
	{zk.CreateModePersistent, "Persistent"},
	{zk.CreateModeEphemeral, "Ephemeral"},
	{zk.CreateModePersistentSequential, "Persistent sequential"},
	{zk.CreateModeEphemeralSequential, "Ephemeral sequential"},
}

// newTextInput builds a textinput.Model styled consistently with the rest
// of the app's centralized styles.
func newTextInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.PromptStyle = styles.InputPrompt
	ti.PlaceholderStyle = styles.Muted
	ti.Placeholder = placeholder
	ti.CharLimit = editCharLimit
	ti.Width = 60
	return ti
}

func renderCreate(cs createState) string {
	var b strings.Builder
	b.WriteString(styles.Title.Render(fmt.Sprintf("Create child of %s", cs.parent.path)))
	b.WriteString("\n\n")

	b.WriteString(styles.Label.Render("Name:"))
	b.WriteString(" ")
	b.WriteString(cs.name.View())
	b.WriteString("\n")

	if cs.step >= createStepData {
		b.WriteString(styles.Label.Render("Initial data:"))
		b.WriteString(" ")
		b.WriteString(cs.data.View())
		b.WriteString("\n")
	}

	if cs.step < createStepMode {
		b.WriteString("\n")
		b.WriteString(styles.Muted.Render("enter to continue, esc to cancel"))
		return b.String()
	}

	b.WriteString("\n")
	b.WriteString(styles.Label.Render("Type:"))
	b.WriteString("\n")
	for i, opt := range createModeOptions {
		marker := "  "
		style := styles.Node
		if i == cs.modeIndex {
			marker = "▸ "
			style = styles.NodeSelected
		}
		b.WriteString(style.Render(marker + opt.label))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styles.Muted.Render("←/→/↑/↓ to choose, enter to create, esc to cancel"))
	return b.String()
}

type createResultMsg struct {
	parentPath    string
	requestedPath string
	actualPath    string
	err           error
}

// createCmd is the only place internal/ui calls Client.Create: it runs
// inside the tea.Cmd closure bubbletea invokes off the Update goroutine,
// never inside Update itself.
func createCmd(client *zk.Client, parentPath, requestedPath string, data []byte, mode zk.CreateMode) tea.Cmd {
	return func() tea.Msg {
		actualPath, err := client.Create(requestedPath, data, mode)
		return createResultMsg{parentPath: parentPath, requestedPath: requestedPath, actualPath: actualPath, err: err}
	}
}

// --- edit ---------------------------------------------------------------

// editState is the transient state of the "edit a znode's data" flow. It
// always starts by reading the znode's current Stat (for its Version) so
// the eventual Set is version-guarded against a concurrent change, not a
// blind overwrite.
type editState struct {
	path          string
	loading       bool
	version       int32
	input         textinput.Model
	originalData  []byte
	originalValue string
	err           error
}

// editableData returns the textual representation accepted by the current
// single-line editor. Rejecting data it cannot faithfully represent is safer
// than allowing Save to truncate or normalize it.
func editableData(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", fmt.Errorf("data is not valid UTF-8 and cannot be edited safely")
	}
	value := string(data)
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("multiline data cannot be edited safely")
	}
	if utf8.RuneCountInString(value) > editCharLimit {
		return "", fmt.Errorf("data exceeds the %d-character editor limit", editCharLimit)
	}
	input := newTextInput("")
	input.SetValue(value)
	if input.Value() != value {
		return "", fmt.Errorf("data cannot round-trip through the editor without alteration")
	}
	return value, nil
}

// payload returns the exact bytes fetched for an unchanged edit. In
// particular, this avoids relying on the text input's internal rune storage
// to reproduce the stored payload on a no-op Save.
func (es editState) payload() []byte {
	if es.input.Value() == es.originalValue {
		return es.originalData
	}
	return []byte(es.input.Value())
}

func renderEdit(es editState) string {
	var b strings.Builder
	b.WriteString(styles.Title.Render("Edit " + es.path))
	b.WriteString("\n\n")

	if es.loading {
		b.WriteString(styles.Muted.Render("loading current data…"))
		return b.String()
	}
	if es.err != nil {
		b.WriteString(styles.NodeError.Render(es.err.Error()))
		b.WriteString("\n\n")
		b.WriteString(styles.Muted.Render("esc to go back"))
		return b.String()
	}

	b.WriteString(styles.Label.Render("Data:"))
	b.WriteString(" ")
	b.WriteString(es.input.View())
	b.WriteString("\n\n")
	b.WriteString(styles.Muted.Render(fmt.Sprintf("current version: %d — enter to save, esc to cancel", es.version)))
	return b.String()
}

type editStatMsg struct {
	path    string
	data    []byte
	version int32
	err     error
}

// fetchForEditCmd reads path's current data/Stat to seed the edit form.
// Like fetchDataCmd, it only ever runs inside a tea.Cmd closure.
func fetchForEditCmd(client *zk.Client, path string) tea.Cmd {
	return func() tea.Msg {
		data, stat, err := client.Get(path)
		if err != nil {
			return editStatMsg{path: path, err: err}
		}
		return editStatMsg{path: path, data: data, version: stat.Version}
	}
}

type setResultMsg struct {
	path string
	err  error
}

// setCmd is the only place internal/ui calls Client.Set.
func setCmd(client *zk.Client, path string, data []byte, version int32) tea.Cmd {
	return func() tea.Msg {
		_, err := client.Set(path, data, version)
		return setResultMsg{path: path, err: err}
	}
}

// --- delete ---------------------------------------------------------------

// deleteState is the transient state of the "delete a znode" confirmation
// flow. Like edit, it first reads the znode's current Stat for its
// Version, so Delete is version-guarded rather than forced with -1.
type deleteState struct {
	target  *node
	loading bool
	version int32
	err     error
}

func renderConfirmDelete(ds deleteState) string {
	path := ""
	if ds.target != nil {
		path = ds.target.path
	}

	var b strings.Builder
	b.WriteString(styles.Title.Render("Delete " + path))
	b.WriteString("\n\n")

	if ds.loading {
		b.WriteString(styles.Muted.Render("loading node information…"))
		return b.String()
	}
	if ds.err != nil {
		b.WriteString(styles.NodeError.Render(ds.err.Error()))
		b.WriteString("\n\n")
		b.WriteString(styles.Muted.Render("any key to go back"))
		return b.String()
	}

	b.WriteString(styles.DialogDanger.Render(fmt.Sprintf("Delete %s? This action cannot be undone.", path)))
	b.WriteString("\n\n")
	b.WriteString(styles.Muted.Render("y = yes, delete   ·   any other key = cancel"))
	return b.String()
}

type deleteStatMsg struct {
	path    string
	version int32
	err     error
}

// fetchStatForDeleteCmd reads path's current Stat for its Version, so the
// confirmation prompt's eventual Delete is version-guarded.
func fetchStatForDeleteCmd(client *zk.Client, path string) tea.Cmd {
	return func() tea.Msg {
		_, stat, err := client.Get(path)
		if err != nil {
			return deleteStatMsg{path: path, err: err}
		}
		return deleteStatMsg{path: path, version: stat.Version}
	}
}

type deleteResultMsg struct {
	path       string
	parentPath string
	err        error
}

// deleteCmd is the only place internal/ui calls Client.Delete.
func deleteCmd(client *zk.Client, path, parentPath string, version int32) tea.Cmd {
	return func() tea.Msg {
		err := client.Delete(path, version)
		return deleteResultMsg{path: path, parentPath: parentPath, err: err}
	}
}
