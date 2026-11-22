package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/flobilosaurus/agentnook/internal/config"
)

func key(m model, kind tea.KeyType) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: kind})
	return next.(model), cmd
}

func runeKey(m model, value string) model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
	return next.(model)
}

func TestSelectExistingAndCurrent(t *testing.T) {
	profiles := []config.Profile{{Name: "alpha"}, {Name: "work"}}
	m := newModel("/repo", profiles, "work")
	if m.cursor != 1 {
		t.Fatal("current profile not highlighted")
	}
	m = runeKey(m, "k")
	if m.cursor != 0 {
		t.Fatal("k did not move up")
	}
	m = runeKey(m, "j")
	m, cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.choice.Profile != "work" || m.choice.Create {
		t.Fatalf("%+v", m.choice)
	}
}

func TestCreateEmptyProfileList(t *testing.T) {
	m := newModel("/repo", nil, "")
	m, _ = key(m, tea.KeyEnter)
	if !m.creating {
		t.Fatal("create screen not opened")
	}
	// Navigation keys and spaces must reach the text input while creating.
	for _, char := range " jack " {
		m = runeKey(m, string(char))
	}
	m, cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.choice != (Choice{Profile: "jack", Create: true}) {
		t.Fatalf("%+v", m.choice)
	}
}

func TestCreateValidationAndDuplicate(t *testing.T) {
	m := newModel("/repo", []config.Profile{{Name: "work"}}, "")
	m = runeKey(m, "j")
	m, _ = key(m, tea.KeyEnter)
	for _, value := range []string{"../escape", "work", "WORK"} {
		m.input.SetValue(value)
		m, cmd := key(m, tea.KeyEnter)
		if cmd != nil || m.errorText == "" || m.choice.Profile != "" {
			t.Fatalf("accepted %q", value)
		}
		if !strings.Contains(m.View(), "invalid profile") && !strings.Contains(m.View(), "already exists") {
			t.Fatal("missing validation feedback")
		}
	}
	m.input.SetValue("other")
	m, cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.choice.Profile != "other" {
		t.Fatal("valid creation failed")
	}
}

func TestBoundsCancelAndView(t *testing.T) {
	for _, creating := range []bool{false, true} {
		for _, kind := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
			m := newModel("/project", []config.Profile{{Name: "work"}}, "")
			m.creating = creating
			m, cmd := key(m, kind)
			if !m.cancelled || cmd == nil || m.choice.Profile != "" {
				t.Fatal("cancel failed")
			}
		}
	}
	m := newModel("/project", nil, "")
	m, _ = key(m, tea.KeyUp)
	m, _ = key(m, tea.KeyDown)
	if m.cursor != 0 {
		t.Fatal("cursor escaped empty list")
	}
	for _, text := range []string{"agentnook", "/project", "Create new profile", "esc/ctrl+c"} {
		if !strings.Contains(m.View(), text) {
			t.Errorf("missing %q", text)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40})
	if next.(model).width != 36 {
		t.Fatal("width not updated")
	}
	if m.Init() == nil {
		t.Fatal("missing text input initialization")
	}
}
