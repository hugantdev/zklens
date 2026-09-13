package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func pickerItems() []ContextItem {
	return []ContextItem{
		{Name: "dev", Detail: "dev1:2181"},
		{Name: "prod", Detail: "prod1:2181,prod2:2181 • timeout 30s"},
	}
}

func TestContextPickerEnterSelectsHighlightedItem(t *testing.T) {
	p := NewContextPicker(pickerItems())

	newModel, cmd := p.Update(keyMsg("down"))
	p = newModel.(ContextPicker)
	if p.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", p.cursor)
	}
	if cmd != nil {
		t.Fatalf("Update(down) cmd = %v, want nil (only Enter/Esc quit)", cmd)
	}

	newModel, cmd = p.Update(keyMsg("enter"))
	p = newModel.(ContextPicker)
	if cmd == nil {
		t.Fatal("Update(enter) returned a nil cmd, want tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Update(enter) cmd produced %T, want tea.QuitMsg", cmd())
	}

	idx, ok := p.Result()
	if !ok {
		t.Fatal("Result() after Enter = cancelled, want confirmed")
	}
	if idx != 1 {
		t.Fatalf("Result() index = %d, want 1 (the highlighted item)", idx)
	}
}

func TestContextPickerEscCancels(t *testing.T) {
	for _, key := range []string{"esc", "q", "ctrl+c"} {
		p := NewContextPicker(pickerItems())
		newModel, cmd := p.Update(keyMsg(key))
		p = newModel.(ContextPicker)
		if cmd == nil {
			t.Fatalf("Update(%s) returned a nil cmd, want tea.Quit", key)
		}
		if _, ok := p.Result(); ok {
			t.Fatalf("Result() after %s = confirmed, want cancelled", key)
		}
	}
}

func TestContextPickerCursorStaysInBounds(t *testing.T) {
	p := NewContextPicker(pickerItems())

	newModel, _ := p.Update(keyMsg("up"))
	p = newModel.(ContextPicker)
	if p.cursor != 0 {
		t.Fatalf("cursor after up at the top = %d, want 0", p.cursor)
	}

	p = NewContextPicker(pickerItems())
	for i := 0; i < 5; i++ {
		newModel, _ = p.Update(keyMsg("down"))
		p = newModel.(ContextPicker)
	}
	if p.cursor != 1 {
		t.Fatalf("cursor after 5 downs on a 2-item list = %d, want 1 (clamped)", p.cursor)
	}
}

func TestContextPickerViewListsNamesAndHelp(t *testing.T) {
	p := NewContextPicker(pickerItems())
	view := p.View()
	for _, want := range []string{"dev", "prod", "prod1:2181,prod2:2181", "enter"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() does not contain %q:\n%s", want, view)
		}
	}
}
