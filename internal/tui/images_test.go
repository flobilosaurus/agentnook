package tui

import (
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/flobilosaurus/agentnook/internal/config"
)

func imageEntry(t *testing.T, name string) config.BuiltImage {
	t.Helper()
	options := config.Defaults()
	options.Agent = "pi"
	options.AgentVersion = "1.4.0"
	options.Image = filepath.Join(t.TempDir(), name+".smolmachine")

	options.CPUs, options.Memory = 2, 2048
	if err := os.WriteFile(options.Image, []byte("test-only archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.BuiltImage{Name: name, BaseImage: "node:24-bookworm", Architecture: runtime.GOARCH,
		BuiltAt: "2026-11-17T10:00:00Z", VM: options}
}

func imageKey(m imageModel, kind tea.KeyType) (imageModel, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: kind})
	return next.(imageModel), cmd
}

func TestImagePickerShowsDetailsAndSelectsPreset(t *testing.T) {
	entry := imageEntry(t, "work-v1")
	m := newImageModel("/project", config.Defaults(), []config.BuiltImage{entry}, registry.All())
	m, _ = imageKey(m, tea.KeyDown)
	for _, text := range []string{"work-v1", "node:24-bookworm", "Pi 1.4.0", "CPU 2", "RAM 2048 MiB", "ready", entry.BuiltAt} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("missing %q in %s", text, m.View())
		}
	}
	m, cmd := imageKey(m, tea.KeyEnter)
	if !m.done || m.name != entry.Name || cmd == nil {
		t.Fatalf("wrong choice: %+v", m)
	}
}

func TestImagePickerEmptyKeepCancelAndMissing(t *testing.T) {
	m := newImageModel("/project", config.Defaults(), nil, registry.All())
	if !strings.Contains(m.View(), "No managed builds") {
		t.Fatal("missing empty state")
	}
	m, _ = imageKey(m, tea.KeyDown)
	if m.cursor != 0 {
		t.Fatal("cursor escaped empty list")
	}
	m, _ = imageKey(m, tea.KeyEnter)
	if !m.done || m.name != "" {
		t.Fatal("keep current did not work")
	}
	for _, kind := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m = newImageModel("/project", config.Defaults(), nil, registry.All())
		m, cmd := imageKey(m, kind)
		if !m.cancelled || cmd == nil {
			t.Fatal("cancel failed")
		}
	}
	entry := imageEntry(t, "missing")
	if err := os.Remove(entry.VM.Image); err != nil {
		t.Fatal(err)
	}
	m = newImageModel("/project", config.Defaults(), []config.BuiltImage{entry}, registry.All())
	m, _ = imageKey(m, tea.KeyDown)
	m, cmd := imageKey(m, tea.KeyEnter)
	if m.done || cmd != nil || m.errorText == "" {
		t.Fatal("missing archive selected")
	}
}

func TestLauncherImageSubmenuPersistsChoiceAndProfile(t *testing.T) {
	entry := imageEntry(t, "work-v1")
	m := newLauncherModel("/project", []config.Profile{{Name: "work"}}, "work", config.Defaults(), []config.BuiltImage{entry}, registry.All(), true)
	for _, text := range []string{"Profile: work", "Image:", "Linux base:", "RAM: 2048 MiB"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("launcher missing %q", text)
		}
	}
	m = runeKey(m, "i")
	if m.picker == nil {
		t.Fatal("image submenu did not open")
	}
	m, _ = key(m, tea.KeyDown)
	m, cmd := key(m, tea.KeyEnter)
	if cmd != nil || m.picker != nil || m.options != entry.VM {
		t.Fatal("image selection quit launcher or lost preset")
	}
	if !strings.Contains(m.View(), "Image: work-v1") {
		t.Fatal("image selection not displayed")
	}
	m, cmd = key(m, tea.KeyEnter)
	if cmd == nil || m.choice.Profile != "work" || m.choice.Image != entry.Name {
		t.Fatalf("wrong launcher result: %+v", m.choice)
	}
}

func TestLauncherImageMenuCancelAndCreate(t *testing.T) {
	entry := imageEntry(t, "work-v1")
	m := newLauncherModel("/project", nil, "", config.Defaults(), []config.BuiltImage{entry}, registry.All(), true)
	m, _ = key(m, tea.KeyDown) // Images menu item after Create new profile.
	m, _ = key(m, tea.KeyEnter)
	if m.picker == nil {
		t.Fatal("image menu item did not open")
	}
	m, cmd := key(m, tea.KeyEsc)
	if cmd != nil || m.picker != nil || m.cancelled || m.options != config.Defaults() {
		t.Fatal("escape did not return without mutation")
	}
	m = runeKey(m, "i")
	m, _ = key(m, tea.KeyDown)
	m, _ = key(m, tea.KeyEnter)
	m, _ = key(m, tea.KeyUp)
	m, _ = key(m, tea.KeyEnter)
	m.input.SetValue("new")
	m, cmd = key(m, tea.KeyEnter)
	if cmd == nil || m.choice != (Choice{Profile: "new", Create: true, Image: entry.Name}) {
		t.Fatalf("create lost image: %+v", m.choice)
	}
	m = newLauncherModel("/project", nil, "", config.Defaults(), nil, registry.All(), true)
	m = runeKey(m, "i")
	m, cmd = key(m, tea.KeyCtrlC)
	if !m.cancelled || cmd == nil {
		t.Fatal("ctrl+c in submenu did not cancel launcher")
	}
}

func TestImagePickerLargeCatalogKeepsHighlightVisible(t *testing.T) {
	entry := imageEntry(t, "last-image")
	entries := make([]config.BuiltImage, 30)
	for i := range entries {
		entries[i] = entry
	}
	m := newImageModel("/project", config.Defaults(), entries, registry.All())
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = next.(imageModel)
	for range 30 {
		m, _ = imageKey(m, tea.KeyDown)
	}
	if m.cursor != 30 || strings.Count(m.View(), "[ready]") > 3 || !strings.Contains(m.View(), "▸ last-image") {
		t.Fatal("catalog not scrolled to highlight")
	}
}
