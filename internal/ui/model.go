// Package ui is zklens's terminal UI: an Elm-like bubbletea Model/Update/View
// that lets the user browse the znode tree of a connected ZooKeeper
// ensemble. It talks to the ensemble only through internal/zk, and only
// from inside tea.Cmd closures — Update never blocks.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/hugantdev/zklens/internal/zk"
)

var (
	keyToggle     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "expand/collapse"))
	keyExpand     = key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand"))
	keyCollapse   = key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse/up"))
	keyQuit       = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))
	keyForceQuit  = key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit"))
	keyDetail     = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "load data"))
	keyBack       = key.NewBinding(key.WithKeys("tab", "esc"), key.WithHelp("tab/esc", "back to tree"))
	keyCreate     = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new child"))
	keyEdit       = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit data"))
	keyDelete     = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "delete"))
	keyCancel     = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	keyConfirm    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm"))
	keyConfirmYes = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yes, delete"))
	keyPrev       = key.NewBinding(key.WithKeys("left", "h", "up", "k"), key.WithHelp("←/↑", "previous"))
	keyNext       = key.NewBinding(key.WithKeys("right", "l", "down", "j"), key.WithHelp("→/↓", "next"))
	keyWatch      = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "watch"))
	keyFocusNext  = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "switch panel"))
	keyHelp       = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))
)

// viewMode says whether a modal dialog is open over the panel layout.
// modeTree is the zero value, so a Model{} with no explicit mode behaves as
// the plain browser it always has.
type viewMode int

const (
	modeTree viewMode = iota
	modeCreate
	modeEdit
	modeConfirmDelete
)

// isModal reports whether a dialog is open, in which case it owns the whole
// keyboard and is drawn over the panels.
func (m viewMode) isModal() bool { return m != modeTree }

// connIndicator is the small dot drawn at the right edge of the status line.
// Unlike m.status, which every operation message overwrites, it always
// reflects the ensemble's current connection health, so that health stays
// visible even once the message has moved on to describe something else.
type connIndicator int

const (
	connUnknown connIndicator = iota // no connEventMsg observed yet
	connOK
	connWarn
	connBad
)

// focusArea is which panel the keyboard drives. It is deliberately separate
// from viewMode: opening a dialog must not destroy the focus to restore
// afterwards, which is what the old per-dialog returnMode field existed for.
type focusArea int

const (
	focusTree focusArea = iota
	focusData
	focusStat
)

// Model is zklens's bubbletea program state: the znode tree explored so far
// and the bubbles/list used to browse its currently visible nodes.
type Model struct {
	client *zk.Client
	events <-chan zk.Event
	title  string

	root  *node
	nodes map[string]*node
	// invalidatedChildren keeps a parent's last visible child slice until a
	// fresh Children result successfully reconciles it. The visible tree can
	// therefore clear stale rows while applyChildren still has enough history
	// to remove disappeared descendants from nodes.
	invalidatedChildren   map[string][]*node
	childrenRequestIDs    map[string]uint64
	nextChildrenRequestID uint64
	childStatGenerations  map[string]uint64
	nextChildStatGen      uint64
	childStats            *childStatScheduler

	list    list.Model
	spinner spinner.Model
	dataVP  viewport.Model
	statVP  viewport.Model
	help    help.Model

	mode   viewMode
	focus  focusArea
	detail detailState
	create createState
	edit   editState
	del    deleteState

	loadingCount int
	// detailRequestID identifies the one detail fetch whose result may update
	// the panels. Every Get gets a new ID, even for the same path, so an older
	// response cannot overwrite a later reload.
	detailRequestID     uint64
	nextDetailRequestID uint64
	// nextWatchGeneration is process-local watch identity. It must never be
	// reset with a node/detail state, because ZooKeeper replies for a deleted
	// and later recreated path can still arrive after its replacement starts.
	nextWatchGeneration uint64
	status              string
	statusErr           bool
	conn                connIndicator

	width, height int
}

// newTreeList builds the bubbles/list that backs the tree panel, with the
// default bindings that would fight zklens's own keys taken out of the way.
// New and the tests share it so the tests exercise the real key map.
func newTreeList() list.Model {
	l := list.New(nil, treeDelegate{}, 0, 0)
	l.SetShowTitle(false) // the panel frame draws the title now
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false) // so View's height is exactly what SetSize was given
	l.SetFilteringEnabled(false)

	km := l.KeyMap
	// Right/left and h/l are repurposed for expand/collapse, so free them from
	// the list's default previous/next-page bindings.
	km.PrevPage = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "prev page"))
	km.NextPage = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "next page"))
	// The list's own Quit is ("q","esc"), which would make esc kill the app
	// instead of returning focus to the tree; zklens handles quitting itself.
	km.Quit = key.NewBinding(key.WithDisabled())
	km.ForceQuit = key.NewBinding(key.WithDisabled())
	// "?" belongs to the app-wide help bar, not to the list's own help.
	km.ShowFullHelp = key.NewBinding(key.WithDisabled())
	km.CloseFullHelp = key.NewBinding(key.WithDisabled())
	l.KeyMap = km

	return l
}

// New builds the initial Model for a Client that has already established a
// session. title identifies the ensemble, e.g. its hosts.
func New(client *zk.Client, title string) Model {
	root := &node{path: "/", name: "/", depth: 0, expanded: true, loading: true}
	nodes := map[string]*node{"/": root}

	m := Model{
		client:       client,
		events:       client.Events(),
		root:         root,
		nodes:        nodes,
		title:        title,
		list:         newTreeList(),
		spinner:      spinner.New(spinner.WithSpinner(spinner.Dot)),
		dataVP:       viewport.New(0, 0),
		statVP:       viewport.New(0, 0),
		help:         newHelp(),
		loadingCount: 1,
		status:       "loading /…",
		childrenRequestIDs: map[string]uint64{
			"/": 1,
		},
		nextChildrenRequestID: 1,
	}
	m.refreshItems()
	return m
}

// Init kicks off the root znode's children fetch and starts listening for
// connection state changes, alongside the loading spinner.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchChildrenCmd(m.client, m.root.path, m.childrenRequestIDs[m.root.path]),
		listenEventsCmd(m.events),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyMsg:
		// A modal owns the whole keyboard while it is open.
		switch m.mode {
		case modeCreate:
			return m.updateCreate(msg)
		case modeEdit:
			return m.updateEdit(msg)
		case modeConfirmDelete:
			return m.updateConfirmDelete(msg)
		}

		switch {
		case key.Matches(msg, keyQuit):
			return m, tea.Quit
		case key.Matches(msg, keyHelp):
			// The expanded help is taller, so the panels have to give up the
			// rows it takes.
			m.help.ShowAll = !m.help.ShowAll
			m.layout()
			return m, nil
		case key.Matches(msg, keyFocusNext):
			m.focus = m.nextFocus()
			return m, nil
		}

		// Exactly one panel handles the key, and every branch returns: that is
		// what stops the list and the viewports from both consuming up/down.
		switch m.focus {
		case focusData:
			return m.updateDataPanel(msg)
		case focusStat:
			return m.updateStatPanel(msg)
		}

		switch {
		case key.Matches(msg, keyDetail):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m.openDetail(it.n)
			}
			return m, nil
		case key.Matches(msg, keyCreate):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m.startCreate(it.n)
			}
			return m, nil
		case key.Matches(msg, keyEdit):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m.startEdit(it.n)
			}
			return m, nil
		case key.Matches(msg, keyDelete):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m.startDelete(it.n)
			}
			return m, nil
		case key.Matches(msg, keyWatch):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m.toggleChildWatch(it.n)
			}
			return m, nil
		case key.Matches(msg, keyToggle):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m, m.setExpanded(it.n, !it.n.expanded)
			}
			return m, nil
		case key.Matches(msg, keyExpand):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				return m, m.setExpanded(it.n, true)
			}
			return m, nil
		case key.Matches(msg, keyCollapse):
			if it, ok := m.list.SelectedItem().(treeItem); ok {
				n := it.n
				if n.expanded {
					return m, m.setExpanded(n, false)
				}
				if n.parent != nil {
					m.selectNode(n.parent)
				}
			}
			return m, nil
		}

	case childrenMsg:
		return m.handleChildren(msg)

	case childStatMsg:
		return m.handleChildStat(msg)

	case dataMsg:
		return m.handleData(msg), nil

	case editStatMsg:
		return m.handleEditStat(msg)

	case setResultMsg:
		return m.handleSetResult(msg)

	case deleteStatMsg:
		return m.handleDeleteStat(msg), nil

	case deleteResultMsg:
		return m.handleDeleteResult(msg)

	case createResultMsg:
		return m.handleCreateResult(msg)

	case childWatchArmedMsg:
		return m.handleChildWatchArmed(msg)

	case childWatchFiredMsg:
		return m.handleChildWatchFired(msg)

	case dataWatchArmedMsg:
		return m.handleDataWatchArmed(msg)

	case dataWatchFiredMsg:
		return m.handleDataWatchFired(msg)

	case statWatchArmedMsg:
		return m.handleStatWatchArmed(msg)

	case statWatchFiredMsg:
		return m.handleStatWatchFired(msg)

	case statRefreshedMsg:
		return m.handleStatRefreshed(msg)

	case connEventMsg:
		m.applyConnEvent(msg.ev)
		return m, listenEventsCmd(m.events)

	case connClosedMsg:
		m.status = "connection closed"
		m.statusErr = true
		m.conn = connBad
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	body := m.renderPanels()

	if m.mode.isModal() {
		modal := m.renderModal()
		x := max(0, (m.width-lipgloss.Width(modal))/2)
		y := max(0, (lipgloss.Height(body)-lipgloss.Height(modal))/2)
		body = overlay(body, modal, x, y)
	}

	return lipgloss.JoinVertical(lipgloss.Left, body, m.renderHelp(), m.renderStatus())
}

// renderPanels composes the panel layout: the tree on the left, Data above
// Stat on the right. JoinHorizontal pads the shorter column, so the two
// right-hand panel heights must add up to the body height exactly or the
// columns drift apart.
func (m Model) renderPanels() string {
	g := m.geometry()
	if g.bodyH <= 0 || m.width <= 0 {
		return ""
	}

	if g.singlePanel {
		return panel(m.focusedTitle(), m.focusedContent(), m.width, g.bodyH, true)
	}

	tree := panel("Tree — "+m.title, m.list.View(), g.treeW, g.bodyH, m.focus == focusTree)

	if g.statH == 0 {
		// Too short to split the right column in two: show data and Stat as
		// one scrolling pane instead of two unusably small ones.
		detail := panel(m.dataTitle(), m.dataVP.View(), g.rightW, g.bodyH, m.focus != focusTree)
		return lipgloss.JoinHorizontal(lipgloss.Top, tree, detail)
	}

	data := panel(m.dataTitle(), m.dataVP.View(), g.rightW, g.dataH, m.focus == focusData)
	stat := panel("Stat", m.statVP.View(), g.rightW, g.statH, m.focus == focusStat)
	right := lipgloss.JoinVertical(lipgloss.Left, data, stat)

	return lipgloss.JoinHorizontal(lipgloss.Top, tree, right)
}

// dataTitle is the Data panel's heading, carrying the selected znode's full
// path as a breadcrumb — the tree column is narrow enough that deep names get
// truncated there.
func (m Model) dataTitle() string {
	if m.detail.path == "" {
		return "Data"
	}
	return "Data — " + m.detail.path
}

func (m Model) focusedTitle() string {
	switch m.focus {
	case focusData:
		return m.dataTitle()
	case focusStat:
		return "Stat"
	default:
		return "Tree — " + m.title
	}
}

func (m Model) focusedContent() string {
	switch m.focus {
	case focusData:
		return m.dataVP.View()
	case focusStat:
		return m.statVP.View()
	default:
		return m.list.View()
	}
}

// connIndicatorStyle picks the dot's color for the current connection health.
func connIndicatorStyle(c connIndicator) lipgloss.Style {
	switch c {
	case connOK:
		return styles.StatusConnOK
	case connWarn:
		return styles.StatusConnWarn
	case connBad:
		return styles.StatusConnBad
	default:
		return styles.StatusConnUnknown
	}
}

// renderStatus draws the bottom status line: the latest transient message
// (an operation result, a connection-state change, the loading spinner) on
// the left, on the terminal's own background rather than a painted bar, and a
// small dot at the right edge that always shows the current connection
// health — so that health stays visible even once the message has moved on
// to something unrelated. The two segments are rendered separately and
// joined with plain spaces, never nested, so the dot's color can never be
// reset by the message's own style.
func (m Model) renderStatus() string {
	msgStyle := styles.StatusBar
	if m.statusErr {
		msgStyle = styles.StatusError
	}

	msg := m.status
	if m.loadingCount > 0 {
		msg = m.spinner.View() + " " + msg
	}

	if m.width <= 0 {
		return msgStyle.Render(msg)
	}

	dot := connIndicatorStyle(m.conn).Render("●")
	dotW := lipgloss.Width(dot)
	if m.width <= dotW {
		return truncCells(dot, m.width)
	}

	avail := m.width - dotW - 1 // 1-column gap between the message and the dot
	line := msgStyle.Render(truncCells(msg, avail))

	gap := m.width - lipgloss.Width(line) - dotW
	if gap < 0 {
		gap = 0
	}
	return line + strings.Repeat(" ", gap) + dot
}

// renderModal frames the open dialog so it reads as floating over the panels.
// The dialog bodies size themselves to their content, so they are clamped to
// the terminal here — an unclamped one would spill past the edge on a narrow
// window and smear the layout.
func (m Model) renderModal() string {
	var body string
	style := styles.ModalBorder

	switch m.mode {
	case modeCreate:
		body = renderCreate(m.create)
	case modeEdit:
		body = renderEdit(m.edit)
	case modeConfirmDelete:
		body, style = renderConfirmDelete(m.del), styles.ModalBorderDanger
	default:
		return ""
	}

	// 2 border columns + the style's 1 column of padding on each side.
	maxW := m.width - 4
	maxH := m.geometry().bodyH - 2
	if maxW <= 0 || maxH <= 0 {
		return ""
	}

	lines := strings.Split(body, "\n")
	if len(lines) > maxH {
		lines = lines[:maxH]
	}
	for i, line := range lines {
		lines[i] = truncCells(line, maxW)
	}
	return style.Render(strings.Join(lines, "\n"))
}

// openDetail loads n's data and Stat into the right-hand panels and moves the
// focus there, so the arrow keys scroll what was just loaded. Unlike children,
// this data is never cached: pressing tab again always re-reads the ensemble,
// since a znode's data can change at any time.
func (m Model) openDetail(n *node) (Model, tea.Cmd) {
	// A fresh detailState also clears watching, which is what stops the
	// previous node's data-watch from re-arming itself forever.
	m.stopDetailWatches()
	m.detail = detailState{path: n.path}
	m.focus = focusData
	m.dataVP.GotoTop()
	m.statVP.GotoTop()
	m, cmd := m.startDetailFetch(n.path)
	m.syncDetailViews()
	return m, cmd
}

// startDetailFetch begins a new version of the detail read. Completion is
// always counted, even if a newer read supersedes it before it returns.
func (m Model) startDetailFetch(path string) (Model, tea.Cmd) {
	m.nextDetailRequestID++
	m.detailRequestID = m.nextDetailRequestID
	m.detail.loading = true
	m.loadingCount++
	return m, fetchDataCmd(m.client, path, m.detailRequestID)
}

// updateDataPanel routes a key press while the Datos panel has the focus.
func (m Model) updateDataPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyBack):
		// Only the focus moves: the panel stays on screen with its content, so
		// an armed data-watch keeps updating it and is deliberately left alone.
		m.focus = focusTree
		return m, nil
	case key.Matches(msg, keyEdit):
		if n, ok := m.nodes[m.detail.path]; ok {
			return m.startEdit(n)
		}
		return m, nil
	case key.Matches(msg, keyWatch):
		if m.detail.path == "" {
			return m, nil
		}
		return m.toggleDataWatch()
	}
	var cmd tea.Cmd
	m.dataVP, cmd = m.dataVP.Update(msg)
	return m, cmd
}

// updateStatPanel routes a key press while the Stat panel has the focus.
func (m Model) updateStatPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyBack):
		m.focus = focusTree
		return m, nil
	case key.Matches(msg, keyWatch):
		// The Stat and Data panels show the same znode, and the watch belongs
		// to the node rather than to either panel, so 'w' means the same thing
		// in both. Without this it silently did nothing here.
		if m.detail.path == "" {
			return m, nil
		}
		return m.toggleDataWatch()
	}
	var cmd tea.Cmd
	m.statVP, cmd = m.statVP.Update(msg)
	return m, cmd
}

// nextFocus cycles Tree → Data → Stat → Tree, skipping the Stat panel when
// the terminal is too short to show it.
func (m Model) nextFocus() focusArea {
	g := m.geometry()
	switch m.focus {
	case focusTree:
		return focusData
	case focusData:
		if g.statH == 0 && !g.singlePanel {
			return focusTree
		}
		return focusStat
	default:
		return focusTree
	}
}

// syncDetailViews pushes the current detailState into both viewports. Every
// handler that changes m.detail goes through here so the two panels can never
// disagree about what is loaded.
func (m *Model) syncDetailViews() {
	if m.geometry().statH == 0 {
		// Merged right column: one pane showing data and Stat together. The
		// panel frame carries the path, so the header is left off.
		m.dataVP.SetContent(renderMergedPanel(m.detail))
		m.statVP.SetContent("")
		return
	}
	m.dataVP.SetContent(renderDataPanel(m.detail))
	m.statVP.SetContent(renderStatPanel(m.detail))
}

// handleData applies the result of fetching a znode's data/Stat. Each
// completion settles its own loading count before stale path or request-ID
// results are discarded.
func (m Model) handleData(msg dataMsg) Model {
	m.loadingCount--
	if msg.path != m.detail.path || msg.requestID != m.detailRequestID {
		return m
	}
	m.detail.loading = false

	m.detail.data = msg.data
	m.detail.stat = msg.stat
	m.detail.err = msg.err

	if msg.err != nil {
		m.status = fmt.Sprintf("error reading %s: %v", msg.path, msg.err)
		m.statusErr = true
	} else {
		m.status = fmt.Sprintf("%s: %d bytes", msg.path, len(msg.data))
		m.statusErr = false
	}

	m.syncDetailViews()
	return m
}

// updateCreate routes a key press while the create-child wizard is open.
func (m Model) updateCreate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyForceQuit):
		return m, tea.Quit
	case key.Matches(msg, keyCancel):
		m.mode = modeTree
		return m, nil
	case key.Matches(msg, keyConfirm):
		return m.advanceCreate()
	}

	switch m.create.step {
	case createStepName:
		var cmd tea.Cmd
		m.create.name, cmd = m.create.name.Update(msg)
		return m, cmd
	case createStepData:
		var cmd tea.Cmd
		m.create.data, cmd = m.create.data.Update(msg)
		return m, cmd
	case createStepMode:
		switch {
		case key.Matches(msg, keyPrev):
			m.create.modeIndex--
			if m.create.modeIndex < 0 {
				m.create.modeIndex = len(createModeOptions) - 1
			}
		case key.Matches(msg, keyNext):
			m.create.modeIndex = (m.create.modeIndex + 1) % len(createModeOptions)
		}
		return m, nil
	}
	return m, nil
}

// advanceCreate moves the wizard to its next step, or on the final step
// (mode selection) fires the actual Create.
func (m Model) advanceCreate() (Model, tea.Cmd) {
	switch m.create.step {
	case createStepName:
		if strings.TrimSpace(m.create.name.Value()) == "" {
			return m, nil
		}
		m.create.step = createStepData
		m.create.name.Blur()
		cmd := m.create.data.Focus()
		return m, cmd
	case createStepData:
		m.create.step = createStepMode
		m.create.data.Blur()
		return m, nil
	case createStepMode:
		parent := m.create.parent
		name := strings.TrimSpace(m.create.name.Value())
		data := []byte(m.create.data.Value())
		mode := createModeOptions[m.create.modeIndex].mode
		requestedPath := childPath(parent.path, name)

		m.mode = modeTree
		m.loadingCount++
		return m, createCmd(m.client, parent.path, requestedPath, data, mode)
	}
	return m, nil
}

// startCreate opens the create-child wizard for a new child of parent.
func (m Model) startCreate(parent *node) (Model, tea.Cmd) {
	m.create = createState{
		parent: parent,
		step:   createStepName,
		name:   newTextInput("name"),
		data:   newTextInput("initial data (optional)"),
	}
	cmd := m.create.name.Focus()
	m.mode = modeCreate
	return m, cmd
}

// handleCreateResult applies the result of Create: on success it
// invalidates (and, if currently expanded, immediately re-fetches) the
// parent's cached children so the new znode shows up without restarting
// the TUI.
func (m Model) handleCreateResult(msg createResultMsg) (Model, tea.Cmd) {
	m.loadingCount--
	if msg.err != nil {
		m.status = fmt.Sprintf("error creating %s: %v", msg.requestedPath, msg.err)
		m.statusErr = true
		return m, nil
	}
	m.status = fmt.Sprintf("created %s", msg.actualPath)
	m.statusErr = false
	cmd := m.invalidateAndMaybeRefetch(msg.parentPath)
	return m, cmd
}

// updateEdit routes a key press while the edit-data form is open.
func (m Model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keyForceQuit):
		return m, tea.Quit
	case key.Matches(msg, keyCancel):
		m.mode = modeTree
		return m, nil
	case key.Matches(msg, keyConfirm):
		return m.submitEdit()
	}

	if m.edit.loading || m.edit.err != nil {
		return m, nil
	}
	var cmd tea.Cmd
	m.edit.input, cmd = m.edit.input.Update(msg)
	return m, cmd
}

// submitEdit fires the actual Set using the version read when the edit
// form was opened.
func (m Model) submitEdit() (Model, tea.Cmd) {
	if m.edit.loading || m.edit.err != nil {
		return m, nil
	}
	path := m.edit.path
	data := m.edit.payload()
	version := m.edit.version

	m.mode = modeTree
	m.loadingCount++
	return m, setCmd(m.client, path, data, version)
}

// startEdit opens the edit-data form for n, reading its current data and
// Stat first so the eventual Set is version-guarded rather than a blind
// overwrite.
func (m Model) startEdit(n *node) (Model, tea.Cmd) {
	m.edit = editState{path: n.path, loading: true}
	m.mode = modeEdit
	m.loadingCount++
	return m, fetchForEditCmd(m.client, n.path)
}

// handleEditStat applies the pre-fill read for the edit form: on success
// it builds and focuses the textinput seeded with the current data; on
// failure the error is shown in the form itself (and the status bar)
// instead of silently discarding the edit attempt.
func (m Model) handleEditStat(msg editStatMsg) (Model, tea.Cmd) {
	if msg.path != m.edit.path || !m.edit.loading {
		return m, nil
	}
	m.edit.loading = false
	m.loadingCount--

	if msg.err != nil {
		m.edit.err = msg.err
		m.status = fmt.Sprintf("error reading %s for edit: %v", msg.path, msg.err)
		m.statusErr = true
		return m, nil
	}

	value, err := editableData(msg.data)
	if err != nil {
		m.edit.err = err
		m.status = fmt.Sprintf("cannot edit %s safely: %v", msg.path, err)
		m.statusErr = true
		return m, nil
	}

	m.edit.version = msg.version
	input := newTextInput("")
	input.SetValue(value)
	input.CursorEnd()
	cmd := input.Focus()
	m.edit.input = input
	m.edit.originalData = append([]byte(nil), msg.data...)
	m.edit.originalValue = value
	return m, cmd
}

// handleSetResult applies the result of Set: on success, if the edited
// znode's detail panel is the one currently open, it is refreshed so the
// new data is visible immediately instead of showing stale content.
func (m Model) handleSetResult(msg setResultMsg) (Model, tea.Cmd) {
	m.loadingCount--
	if msg.err != nil {
		m.status = fmt.Sprintf("error saving %s: %v", msg.path, msg.err)
		m.statusErr = true
		return m, nil
	}
	m.status = fmt.Sprintf("saved %s", msg.path)
	m.statusErr = false

	if m.detail.path != "" && m.detail.path == msg.path {
		m, cmd := m.startDetailFetch(msg.path)
		m.syncDetailViews()
		return m, cmd
	}
	return m, nil
}

// updateConfirmDelete routes a key press while the delete confirmation is
// open. Any key other than the explicit "yes" binding cancels — a single
// accidental keypress (including the "x" that opened this prompt) can
// never delete anything.
func (m Model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keyForceQuit) {
		return m, tea.Quit
	}
	if key.Matches(msg, keyConfirmYes) {
		if m.del.loading || m.del.err != nil || m.del.target == nil {
			return m, nil
		}
		return m.submitDelete()
	}
	m.mode = modeTree
	return m, nil
}

// submitDelete fires the actual Delete using the version read when the
// confirmation prompt was opened.
func (m Model) submitDelete() (Model, tea.Cmd) {
	target := m.del.target
	parentPath := ""
	if target.parent != nil {
		parentPath = target.parent.path
	}
	version := m.del.version

	m.mode = modeTree
	m.loadingCount++
	return m, deleteCmd(m.client, target.path, parentPath, version)
}

// startDelete opens the delete confirmation for n, reading its current
// Stat first so the eventual Delete is version-guarded rather than
// forced.
func (m Model) startDelete(n *node) (Model, tea.Cmd) {
	m.del = deleteState{target: n, loading: true}
	m.mode = modeConfirmDelete
	return m, fetchStatForDeleteCmd(m.client, n.path)
}

// handleDeleteStat applies the pre-confirmation Stat read: on failure the
// error is shown in the confirmation panel itself rather than silently
// aborting, so the user actually sees why the delete can't proceed.
func (m Model) handleDeleteStat(msg deleteStatMsg) Model {
	if m.del.target == nil || msg.path != m.del.target.path || !m.del.loading {
		return m
	}
	m.del.loading = false
	if msg.err != nil {
		m.del.err = msg.err
		m.status = fmt.Sprintf("error reading %s: %v", msg.path, msg.err)
		m.statusErr = true
		return m
	}
	m.del.version = msg.version
	return m
}

// handleDeleteResult applies the result of Delete: on success it drops the
// deleted node from the node cache, backs out of a now-stale detail view
// for it, and invalidates (and, if currently expanded, immediately
// re-fetches) the parent's cached children — ZK's own rejection
// (zk.ErrNotEmpty) if the znode still has children is surfaced as-is via
// the status bar, never worked around with an implicit recursive delete.
func (m Model) handleDeleteResult(msg deleteResultMsg) (Model, tea.Cmd) {
	m.loadingCount--
	if msg.err != nil {
		m.status = fmt.Sprintf("error deleting %s: %v", msg.path, msg.err)
		m.statusErr = true
		return m, nil
	}
	m.status = fmt.Sprintf("deleted %s", msg.path)
	m.statusErr = false

	if n, ok := m.nodes[msg.path]; ok {
		stopChildWatch(n)
	}
	delete(m.nodes, msg.path)
	if m.detail.path == msg.path {
		m.stopDetailWatches()
		m.detail = detailState{}
		m.focus = focusTree
		m.syncDetailViews()
	}
	cmd := m.invalidateAndMaybeRefetch(msg.parentPath)
	return m, cmd
}

// invalidateAndMaybeRefetch marks path's node as needing a fresh Children
// fetch (so the next expand re-reads the ensemble instead of reusing a
// cache that a Create/Delete just made stale) and, if the node is
// currently expanded, immediately re-fetches so the tree reflects the
// mutation without the user having to collapse and re-expand it.
func (m *Model) invalidateAndMaybeRefetch(path string) tea.Cmd {
	n, ok := m.nodes[path]
	if !ok {
		return nil
	}
	n.loaded = false
	if m.invalidatedChildren == nil {
		m.invalidatedChildren = make(map[string][]*node)
	}
	if _, alreadySaved := m.invalidatedChildren[path]; !alreadySaved {
		m.invalidatedChildren[path] = n.children
	}
	n.children = nil
	n.err = nil

	if !n.expanded {
		m.refreshItems()
		return nil
	}
	cmd := m.startChildrenFetch(n)
	m.refreshItems()
	return cmd
}

// setExpanded sets n's expanded state, kicking off a children fetch the
// first time a node is expanded (n.loaded stays true afterwards, so
// re-expanding later is instant and does not hit the ensemble again).
func (m *Model) setExpanded(n *node, expand bool) tea.Cmd {
	if expand == n.expanded {
		return nil
	}
	n.expanded = expand

	var cmd tea.Cmd
	if expand && !n.loaded && !n.loading {
		cmd = m.startChildrenFetch(n)
	}
	m.refreshItems()
	return cmd
}

// selectNode moves the list cursor to target, if it is currently visible.
func (m *Model) selectNode(target *node) {
	for i, it := range m.list.Items() {
		if ti, ok := it.(treeItem); ok && ti.n == target {
			m.list.Select(i)
			return
		}
	}
}

// handleChildren applies the result of fetching a node's children: on
// success it materializes child nodes (registering them in m.nodes for
// later lookups); on failure it records the error on the node so the
// delegate can show it inline, and surfaces it in the status bar.
func (m Model) handleChildren(msg childrenMsg) (Model, tea.Cmd) {
	if msg.requestID != 0 {
		// Every command-created request settles exactly once, even if a newer
		// refresh has made its result stale or the parent was removed.
		m.loadingCount--
	}
	n, ok := m.nodes[msg.path]
	if !ok {
		return m, nil
	}
	if msg.requestID != 0 {
		if m.childrenRequestIDs[msg.path] != msg.requestID {
			return m, nil
		}
		delete(m.childrenRequestIDs, msg.path)
		n.loading = false
	} else if n.loading {
		// Zero-ID messages are used by focused unit tests that construct a
		// completion directly. Production fetches always carry an ID.
		n.loading = false
		m.loadingCount--
	}

	if msg.err != nil {
		n.err = msg.err
		m.status = fmt.Sprintf("error listing %s: %v", msg.path, msg.err)
		m.statusErr = true
		m.refreshItems()
		return m, nil
	}

	cmd := m.applyChildren(n, msg.children)
	m.status = fmt.Sprintf("%s: %d children", msg.path, len(n.children))
	m.statusErr = false
	m.refreshItems()
	return m, cmd
}

// startChildrenFetch assigns a new identity to n's Children request. A later
// request for the same path supersedes this one, but both completions still
// settle loadingCount in handleChildren.
func (m *Model) startChildrenFetch(n *node) tea.Cmd {
	if m.childrenRequestIDs == nil {
		m.childrenRequestIDs = make(map[string]uint64)
	}
	m.nextChildrenRequestID++
	requestID := m.nextChildrenRequestID
	m.childrenRequestIDs[n.path] = requestID
	n.loading = true
	m.loadingCount++
	return fetchChildrenCmd(m.client, n.path, requestID)
}

// childStatMsg carries one listed child's own child count back to Update.
type childStatMsg struct {
	path        string
	generation  uint64
	found       bool
	numChildren int32
	err         error
	scheduled   bool
	next        tea.Cmd
}

type childStatLookup func(path string) (bool, *zk.Stat, error)

type childStatJob struct {
	path       string
	generation uint64
}

const maxConcurrentChildStatLookups = 8

// childStatScheduler owns the one lookup budget for the whole Model. Jobs
// from overlapping tree refreshes wait in this queue instead of each refresh
// creating another set of blocking workers.
type childStatScheduler struct {
	limit   int
	running int
	pending []childStatJob
	lookup  childStatLookup
}

func newChildStatScheduler(limit int, lookup childStatLookup) *childStatScheduler {
	if limit < 1 {
		limit = 1
	}
	return &childStatScheduler{limit: limit, lookup: lookup}
}

func (s *childStatScheduler) enqueue(jobs []childStatJob) tea.Cmd {
	s.pending = append(s.pending, jobs...)
	return s.dispatch()
}

func (s *childStatScheduler) complete() tea.Cmd {
	if s.running > 0 {
		s.running--
	}
	return s.dispatch()
}

func (s *childStatScheduler) dispatch() tea.Cmd {
	slots := s.limit - s.running
	if slots <= 0 || len(s.pending) == 0 {
		return nil
	}
	if slots > len(s.pending) {
		slots = len(s.pending)
	}
	cmds := make([]tea.Cmd, 0, slots)
	for range slots {
		job := s.pending[0]
		s.pending = s.pending[1:]
		s.running++
		cmds = append(cmds, childStatLookupCmd(job, s.lookup, true))
	}
	return tea.Batch(cmds...)
}

func childStatLookupCmd(job childStatJob, lookup childStatLookup, scheduled bool) tea.Cmd {
	return func() tea.Msg {
		found, stat, err := lookup(job.path)
		msg := childStatMsg{path: job.path, generation: job.generation, err: err, scheduled: scheduled}
		if err == nil && found {
			msg.found = true
			msg.numChildren = stat.NumChildren
		}
		return msg
	}
}

// childStatsCmd asks the ensemble for each child's Stat, whose NumChildren is
// the only way to tell a leaf from an unexpanded subtree: Children returns
// names and nothing else.
//
// These lookups are deliberately left out of loadingCount. The tree is already
// drawn and usable when they start; counting them would leave the spinner
// running after every expansion for what is only a refinement of the markers.
func childStatsCmd(client *zk.Client, children []*node, generations map[string]uint64) tea.Cmd {
	return childStatsCmdWithLookup(children, generations, maxConcurrentChildStatLookups, func(path string) (bool, *zk.Stat, error) {
		return client.Exists(path)
	})
}

// childStatsCmdWithLookup runs at most limit Exists lookups at a time. Each
// worker returns a single result and is continued by handleChildStat, so UI
// markers update as results arrive instead of waiting for a wide node's whole
// child list to finish.
func childStatsCmdWithLookup(children []*node, generations map[string]uint64, limit int, lookup childStatLookup) tea.Cmd {
	if len(children) == 0 {
		return nil
	}
	if limit < 1 {
		limit = 1
	}

	jobs := make(chan childStatJob, len(children))
	for _, c := range children {
		jobs <- childStatJob{path: c.path, generation: generations[c.path]}
	}
	close(jobs)

	workers := limit
	if workers > len(children) {
		workers = len(children)
	}
	cmds := make([]tea.Cmd, 0, workers)
	for range workers {
		cmds = append(cmds, childStatWorkerCmd(jobs, lookup))
	}
	return tea.Batch(cmds...)
}

func childStatWorkerCmd(jobs <-chan childStatJob, lookup childStatLookup) tea.Cmd {
	return func() tea.Msg {
		job, ok := <-jobs
		if !ok {
			return nil
		}

		msg := childStatLookupCmd(job, lookup, false)().(childStatMsg)
		msg.next = childStatWorkerCmd(jobs, lookup)
		return msg
	}
}

// queueChildStats adds a refresh's marker lookups to the Model-wide
// scheduler. The queue means overlapping refreshes share one cap without
// parking a goroutine for every queued job.
func (m *Model) queueChildStats(children []*node, generations map[string]uint64) tea.Cmd {
	if len(children) == 0 {
		return nil
	}
	if m.childStats == nil {
		m.childStats = newChildStatScheduler(maxConcurrentChildStatLookups, func(path string) (bool, *zk.Stat, error) {
			return m.client.Exists(path)
		})
	}
	jobs := make([]childStatJob, 0, len(children))
	for _, child := range children {
		jobs = append(jobs, childStatJob{path: child.path, generation: generations[child.path]})
	}
	return m.childStats.enqueue(jobs)
}

// handleChildStat records a child's count so the tree can draw it as a leaf or
// as an expandable node.
//
// A failed lookup is swallowed on purpose: the count only picks a marker, and
// a node whose Stat cannot be read (deleted meanwhile, ACL) is better drawn
// with the neutral collapsed marker than announced as an error the user can do
// nothing about. Any real problem with that node surfaces when it is expanded.
func (m Model) handleChildStat(msg childStatMsg) (Model, tea.Cmd) {
	cmd := msg.next
	if msg.scheduled && m.childStats != nil {
		cmd = m.childStats.complete()
	}
	n, ok := m.nodes[msg.path]
	if !ok || msg.err != nil || !msg.found {
		return m, cmd
	}
	if msg.generation != 0 && m.childStatGenerations[msg.path] != msg.generation {
		return m, cmd
	}
	// Listing the node's children is authoritative and may have happened while
	// this was in flight; do not let a stale count override it.
	if n.loaded {
		return m, cmd
	}
	n.childCount = int(msg.numChildren)
	n.countKnown = true
	m.refreshItems()
	return m, cmd
}

// applyChildren reconciles n.children with a fresh list of child names. It
// reuses nodes already known by path so their loaded subtrees, expanded state,
// and active watches survive a refresh. Nodes absent from the fresh list (and
// every cached descendant below them) are removed from m.nodes. Shared by a
// plain children fetch and a children-watch firing, since both need to do
// exactly the same thing with the names the ensemble just returned.
//
// It returns a command that looks up each new child's own child count, which
// Children does not report — see childStatsCmd.
func (m *Model) applyChildren(n *node, children []string) tea.Cmd {
	n.err = nil
	n.loaded = true

	previousChildren := n.children
	if saved, invalidated := m.invalidatedChildren[n.path]; invalidated {
		previousChildren = saved
		delete(m.invalidatedChildren, n.path)
	}
	previous := make(map[string]*node, len(previousChildren))
	for _, child := range previousChildren {
		previous[child.path] = child
	}

	n.children = make([]*node, 0, len(children))
	retained := make(map[string]struct{}, len(children))
	for _, name := range children {
		path := childPath(n.path, name)
		child, ok := m.nodes[path]
		if !ok {
			child = &node{path: path, name: name, depth: n.depth + 1, parent: n}
			m.nodes[path] = child
		} else {
			// A matching path is the same ZooKeeper node. Keep its cached
			// subtree and UI state, while restoring the parent metadata that
			// determines where it appears in this refreshed list.
			child.name = name
			child.depth = n.depth + 1
			child.parent = n
		}
		n.children = append(n.children, child)
		retained[path] = struct{}{}
	}

	for path := range previous {
		if _, ok := retained[path]; !ok {
			m.removeNodeSubtree(path)
		}
	}
	generations := m.beginChildStatLookups(n.children)
	return m.queueChildStats(n.children, generations)
}

func (m *Model) beginChildStatLookups(children []*node) map[string]uint64 {
	if m.childStatGenerations == nil {
		m.childStatGenerations = make(map[string]uint64)
	}
	m.nextChildStatGen++
	generation := m.nextChildStatGen
	generations := make(map[string]uint64, len(children))
	for _, child := range children {
		m.childStatGenerations[child.path] = generation
		generations[child.path] = generation
	}
	return generations
}

// removeNodeSubtree removes path and every descendant from the node lookup and
// its child-stat generation cache. Refreshes receive only direct child names,
// so a disappeared child makes all cached state below it stale too.
func (m *Model) removeNodeSubtree(path string) {
	prefix := path + "/"
	for candidate := range m.nodes {
		if candidate == path || strings.HasPrefix(candidate, prefix) {
			stopChildWatch(m.nodes[candidate])
			delete(m.nodes, candidate)
		}
	}
	for candidate := range m.childStatGenerations {
		if candidate == path || strings.HasPrefix(candidate, prefix) {
			delete(m.childStatGenerations, candidate)
		}
	}
	for candidate := range m.invalidatedChildren {
		if candidate == path || strings.HasPrefix(candidate, prefix) {
			delete(m.invalidatedChildren, candidate)
		}
	}
	for candidate := range m.childrenRequestIDs {
		if candidate == path || strings.HasPrefix(candidate, prefix) {
			delete(m.childrenRequestIDs, candidate)
		}
	}
}

// toggleChildWatch turns a live children-watch for n on or off. Turning it
// on arms a one-shot ZooKeeper watch (via ChildrenW) that, every time it
// fires, refreshes n's children and immediately re-arms itself — until the
// user turns it off again, or arming fails (e.g. n was deleted), in which
// case the watch stops itself and reports why instead of looping forever.
func stopWatchListener(cancel *chan struct{}) {
	if *cancel != nil {
		close(*cancel)
		*cancel = nil
	}
}

func stopChildWatch(n *node) {
	n.watching = false
	stopWatchListener(&n.watchCancel)
}

func (m Model) startChildWatch(n *node) (Model, tea.Cmd) {
	stopWatchListener(&n.watchCancel)
	m.nextWatchGeneration++
	n.watchGeneration = m.nextWatchGeneration
	n.watching = true
	n.watchCancel = make(chan struct{})
	m.loadingCount++
	return m, watchChildrenCmd(m.client, n.path, n.watchGeneration)
}

func (m Model) toggleChildWatch(n *node) (Model, tea.Cmd) {
	if n.watching {
		stopChildWatch(n)
		m.status = fmt.Sprintf("watch disabled: %s", n.path)
		m.statusErr = false
		return m, nil
	}
	return m.startChildWatch(n)
}

// handleChildWatchArmed applies the result of (re)arming a children-watch.
// If the node was turned off (or vanished from m.nodes) while the arm
// request was in flight, the result is discarded instead of resurrecting
// a watch nobody wants anymore.
func (m Model) handleChildWatchArmed(msg childWatchArmedMsg) (Model, tea.Cmd) {
	m.loadingCount--
	n, ok := m.nodes[msg.path]
	if !ok || !n.watching || msg.generation != n.watchGeneration {
		return m, nil
	}

	if msg.err != nil {
		stopChildWatch(n)
		n.err = msg.err
		m.status = fmt.Sprintf("watch on %s stopped: %v", msg.path, msg.err)
		m.statusErr = true
		m.refreshItems()
		return m, nil
	}

	statsCmd := m.applyChildren(n, msg.children)
	m.status = fmt.Sprintf("%s: %d children (watch active)", msg.path, len(n.children))
	m.statusErr = false
	m.refreshItems()

	cmds := []tea.Cmd{listenChildWatchCmd(msg.path, msg.generation, msg.events, n.watchCancel), statsCmd}
	if m.detail.path == msg.path {
		// The same node is loaded in the detail panels, and its children just
		// changed — which moves its own cversion, numChildren and pzxid. The
		// tree watch is the only thing that noticed, so refresh the Stat here
		// too; otherwise the Stat panel sits stale until the user reloads it
		// with tab. Re-reading rather than deriving numChildren from the list
		// keeps every Stat field consistent with each other.
		m.loadingCount++
		cmds = append(cmds, statRefreshCmd(m.client, msg.path))
	}
	return m, tea.Batch(cmds...)
}

// handleChildWatchFired re-arms the watch after it fires, unless the user
// already turned it off (or the node is gone) — in which case the fire is
// simply the watch's one and only shot being spent, with nothing left to
// do.
func (m Model) handleChildWatchFired(msg childWatchFiredMsg) (Model, tea.Cmd) {
	n, ok := m.nodes[msg.path]
	if !ok || !n.watching || msg.generation != n.watchGeneration {
		return m, nil
	}
	return m.startChildWatch(n)
}

// toggleDataWatch turns a live data-watch for the currently open detail
// node on or off, the same way toggleChildWatch does for a tree node's
// children. Turning it on also arms the stat-watch (see watch.go) so the
// Stat panel's child-count fields stay fresh, not just its data; turning it
// off relies on the same m.detail.watching guard to stop both.
func (m *Model) stopDetailWatches() {
	m.detail.watching = false
	stopWatchListener(&m.detail.dataWatchCancel)
	stopWatchListener(&m.detail.statWatchCancel)
}

func (m Model) startDataWatch() (Model, tea.Cmd) {
	stopWatchListener(&m.detail.dataWatchCancel)
	m.nextWatchGeneration++
	m.detail.dataWatchGeneration = m.nextWatchGeneration
	m.detail.dataWatchCancel = make(chan struct{})
	m.loadingCount++
	return m, watchDataCmd(m.client, m.detail.path, m.detail.dataWatchGeneration)
}

func (m Model) startStatWatch() (Model, tea.Cmd) {
	stopWatchListener(&m.detail.statWatchCancel)
	m.nextWatchGeneration++
	m.detail.statWatchGeneration = m.nextWatchGeneration
	m.detail.statWatchCancel = make(chan struct{})
	m.loadingCount++
	return m, statWatchCmd(m.client, m.detail.path, m.detail.statWatchGeneration)
}

func (m Model) toggleDataWatch() (Model, tea.Cmd) {
	if m.detail.watching {
		m.stopDetailWatches()
		m.status = fmt.Sprintf("watch disabled: %s", m.detail.path)
		m.statusErr = false
		m.syncDetailViews()
		return m, nil
	}
	m.detail.watching = true
	m.syncDetailViews()
	m, dataCmd := m.startDataWatch()
	m, statCmd := m.startStatWatch()
	return m, tea.Batch(dataCmd, statCmd)
}

// handleDataWatchArmed applies the result of (re)arming a data-watch. A
// response for a path other than the one currently open (or for a watch
// the user already turned off) is discarded, the same way handleData
// guards against a stale plain fetch.
func (m Model) handleDataWatchArmed(msg dataWatchArmedMsg) (Model, tea.Cmd) {
	m.loadingCount--
	if msg.path != m.detail.path || !m.detail.watching || msg.generation != m.detail.dataWatchGeneration {
		return m, nil
	}

	if msg.err != nil {
		m.stopDetailWatches()
		m.detail.err = msg.err
		m.status = fmt.Sprintf("watch on %s stopped: %v", msg.path, msg.err)
		m.statusErr = true
		m.syncDetailViews()
		return m, nil
	}

	m.detail.data = msg.data
	m.detail.stat = msg.stat
	m.detail.err = nil
	m.status = fmt.Sprintf("%s: %d bytes (watch active)", msg.path, len(msg.data))
	m.statusErr = false
	m.syncDetailViews()
	return m, listenDataWatchCmd(msg.path, msg.generation, msg.events, m.detail.dataWatchCancel)
}

// handleDataWatchFired re-arms the data-watch after it fires, unless the
// user already left the panel or turned the watch off (or opened a
// different node) in the meantime.
func (m Model) handleDataWatchFired(msg dataWatchFiredMsg) (Model, tea.Cmd) {
	if msg.path != m.detail.path || !m.detail.watching || msg.generation != m.detail.dataWatchGeneration {
		return m, nil
	}
	return m.startDataWatch()
}

// handleStatWatchArmed applies the result of (re)arming the stat-watch. A
// response for a path other than the one currently open (or for a watch the
// user already turned off) is discarded, mirroring handleDataWatchArmed.
func (m Model) handleStatWatchArmed(msg statWatchArmedMsg) (Model, tea.Cmd) {
	m.loadingCount--
	if msg.path != m.detail.path || !m.detail.watching || msg.generation != m.detail.statWatchGeneration {
		return m, nil
	}

	if msg.err != nil {
		m.stopDetailWatches()
		m.detail.err = msg.err
		m.status = fmt.Sprintf("stat watch for %s stopped: %v", msg.path, msg.err)
		m.statusErr = true
		m.syncDetailViews()
		return m, nil
	}

	return m, listenStatWatchCmd(msg.path, msg.generation, msg.events, m.detail.statWatchCancel)
}

// handleStatWatchFired reacts to a child of the watched node being created
// or removed: it re-reads the node's Stat (statRefreshCmd) and re-arms the
// stat-watch, unless the user already left the panel or turned the watch
// off in the meantime.
func (m Model) handleStatWatchFired(msg statWatchFiredMsg) (Model, tea.Cmd) {
	if msg.path != m.detail.path || !m.detail.watching || msg.generation != m.detail.statWatchGeneration {
		return m, nil
	}
	m.loadingCount++
	m, rearmCmd := m.startStatWatch()
	return m, tea.Batch(statRefreshCmd(m.client, msg.path), rearmCmd)
}

// handleStatRefreshed applies a Stat re-read triggered by the stat-watch
// firing. A failed or negative read (e.g. the node was deleted concurrently)
// is left for the data-watch's own re-arm cycle to report — that already
// surfaces deletion, so this handler just leaves the panel as-is rather than
// duplicating that error handling.
func (m Model) handleStatRefreshed(msg statRefreshedMsg) (Model, tea.Cmd) {
	m.loadingCount--
	// Only the path is checked, deliberately: a refresh can be triggered by
	// the detail panel's own watch or by a tree children-watch on the same
	// node, and in the latter case m.detail.watching is false. Fresher Stat
	// for the node currently on screen is always worth applying.
	if msg.path != m.detail.path {
		return m, nil
	}
	if msg.err != nil || !msg.exists {
		return m, nil
	}

	if statIsOlder(msg.stat, m.detail.stat) {
		return m, nil
	}
	m.detail.stat = msg.stat
	m.syncDetailViews()
	return m, nil
}

// statIsOlder avoids replacing detail data fetched more recently with an
// earlier asynchronous Exists result. ZooKeeper's version counters and zxids
// are monotonic within a single znode incarnation, so a lower value in any of
// them is stale. Deleting and recreating a znode resets its version counters
// to zero, though: a candidate whose Czxid differs belongs to a different
// incarnation, and only an earlier one (lower Czxid) is stale.
func statIsOlder(candidate, current *zk.Stat) bool {
	if candidate == nil || current == nil {
		return false
	}
	if candidate.Czxid != current.Czxid {
		return candidate.Czxid < current.Czxid
	}
	return candidate.Mzxid < current.Mzxid ||
		candidate.Pzxid < current.Pzxid ||
		candidate.Version < current.Version ||
		candidate.Cversion < current.Cversion ||
		candidate.Aversion < current.Aversion
}

// applyConnEvent turns a raw connection state change into status bar text.
func (m *Model) applyConnEvent(ev zk.Event) {
	switch ev.State {
	case zk.StateHasSession:
		m.status = "connected"
		m.statusErr = false
		m.conn = connOK
	case zk.StateDisconnected:
		m.status = "disconnected, retrying…"
		m.statusErr = true
		m.conn = connWarn
	case zk.StateConnecting:
		m.status = "reconnecting…"
		m.statusErr = true
		m.conn = connWarn
	case zk.StateExpired:
		m.status = "session expired"
		m.statusErr = true
		m.conn = connBad
	case zk.StateAuthFailed:
		m.status = "authentication failed"
		m.statusErr = true
		m.conn = connBad
	}
}

func (m *Model) refreshItems() {
	vis := visibleNodes(m.root)
	items := make([]list.Item, len(vis))
	for i, n := range vis {
		items[i] = treeItem{n: n}
	}
	m.list.SetItems(items)
}

type childrenMsg struct {
	path      string
	requestID uint64
	children  []string
	err       error
}

type dataMsg struct {
	path      string
	requestID uint64
	data      []byte
	stat      *zk.Stat
	err       error
}

type connEventMsg struct{ ev zk.Event }

type connClosedMsg struct{}

// fetchChildrenCmd is the only place internal/ui makes a blocking call
// into internal/zk: it runs inside the tea.Cmd closure bubbletea invokes
// off the Update goroutine, never inside Update itself.
func fetchChildrenCmd(client *zk.Client, path string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		children, err := client.Children(path)
		return childrenMsg{path: path, requestID: requestID, children: children, err: err}
	}
}

// fetchDataCmd is the only place internal/ui calls Client.Get: like
// fetchChildrenCmd, it runs inside the tea.Cmd closure bubbletea invokes
// off the Update goroutine, never inside Update itself.
func fetchDataCmd(client *zk.Client, path string, requestID uint64) tea.Cmd {
	return func() tea.Msg {
		data, stat, err := client.Get(path)
		return dataMsg{path: path, requestID: requestID, data: data, stat: stat, err: err}
	}
}

// listenEventsCmd blocks on the client's event channel and re-issues
// itself after every event, so the UI keeps observing connection state
// changes (including reconnects) for the lifetime of the program.
func listenEventsCmd(events <-chan zk.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return connClosedMsg{}
		}
		return connEventMsg{ev: ev}
	}
}
