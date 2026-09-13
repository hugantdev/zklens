package ui

import "github.com/charmbracelet/bubbles/key"

// appKeyMap adapts the Model's current mode and focus to bubbles/help's
// key.Map interface, so the help bar lists only the keys that actually do
// something right now.
type appKeyMap struct {
	mode  viewMode
	focus focusArea
}

func (m Model) keyMap() appKeyMap { return appKeyMap{mode: m.mode, focus: m.focus} }

func (k appKeyMap) ShortHelp() []key.Binding {
	switch {
	case k.mode == modeCreate:
		return []key.Binding{keyPrev, keyNext, keyConfirm, keyCancel}
	case k.mode == modeEdit:
		return []key.Binding{keyConfirm, keyCancel}
	case k.mode == modeConfirmDelete:
		return []key.Binding{keyConfirmYes, keyCancel}
	case k.focus == focusData:
		return []key.Binding{keyBack, keyEdit, keyWatch, keyFocusNext, keyHelp, keyQuit}
	case k.focus == focusStat:
		return []key.Binding{keyBack, keyWatch, keyFocusNext, keyHelp, keyQuit}
	default:
		return []key.Binding{keyToggle, keyDetail, keyCreate, keyEdit, keyDelete, keyFocusNext, keyHelp, keyQuit}
	}
}

func (k appKeyMap) FullHelp() [][]key.Binding {
	if k.mode.isModal() {
		return [][]key.Binding{k.ShortHelp()}
	}
	return [][]key.Binding{
		{keyToggle, keyExpand, keyCollapse},
		{keyDetail, keyCreate, keyEdit, keyDelete, keyWatch},
		{keyFocusNext, keyBack, keyHelp, keyQuit},
	}
}
