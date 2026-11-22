package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestFirstSetupOffersDefaultProfileWithoutImageChoices(t *testing.T) {
	options := config.Defaults()
	options.Agent = "pi"
	m := newLauncherModel("/repo", nil, "", options, nil, registry.All(), false)
	if !m.creating || m.input.Value() != "default" {
		t.Fatal("first setup must directly offer default profile", m)
	}
	view := m.View()
	for _, want := range []string{"Agent: Pi", "Create a Profile", "Next time, run nook.", "default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q: %s", want, view)
		}
	}
	for _, hidden := range []string{"Image:", "Linux base:", "RAM:", "Browse / select images", "i images"} {
		if strings.Contains(view, hidden) {
			t.Fatalf("normal setup exposes %q: %s", hidden, view)
		}
	}
	m, cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.choice != (Choice{Profile: "default", Create: true}) {
		t.Fatal("default profile needs only Enter", m.choice)
	}
}

func TestNormalProfilePickerNeverOpensImageManagement(t *testing.T) {
	entry := imageEntry(t, "work-v1")
	m := newLauncherModel("/repo", []config.Profile{{Name: "work"}}, "work", entry.VM, []config.BuiltImage{entry}, registry.All(), false)
	m = runeKey(m, "i")
	if m.picker != nil {
		t.Fatal("normal setup opened image picker")
	}
	for range 5 {
		m, _ = key(m, tea.KeyDown)
	}
	if m.cursor != len(m.profiles) {
		t.Fatal("cursor reached hidden image item", m.cursor)
	}
	if strings.Contains(m.View(), "Browse / select images") {
		t.Fatal("hidden menu item rendered")
	}
	m, _ = key(m, tea.KeyUp)
	m, cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.choice != (Choice{Profile: "work"}) {
		t.Fatal(m.choice)
	}
}

func TestFirstSetupCanRenameOrCancelDefaultProfile(t *testing.T) {
	options := config.Defaults()
	options.Agent = "pi"
	m := newLauncherModel("/repo", nil, "", options, nil, registry.All(), false)
	m.input.SetValue("private")
	m, cmd := key(m, tea.KeyEnter)
	if cmd == nil || m.choice.Profile != "private" || !m.choice.Create {
		t.Fatal(m.choice)
	}
	m = newLauncherModel("/repo", nil, "", options, nil, registry.All(), false)
	m, cmd = key(m, tea.KeyEsc)
	if cmd == nil || !m.cancelled || m.choice.Profile != "" {
		t.Fatal("cancel selected a profile")
	}
}
