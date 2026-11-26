package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/images"
)

func configStatusFixture(current string) configStatusModel {
	c := config.Empty()
	for i := range 18 {
		entry := config.BuiltImage{
			Name: fmt.Sprintf("dev-%02d", i), BaseImage: config.BaseImage, Architecture: "arm64",
			Tools: []string{"fd@10.3.0"},
			VM:    config.Options{Image: fmt.Sprintf("/images/dev-%02d/image.smolmachine", i), Agent: "pi", AgentVersion: "1.4.0", CPUs: 2, Memory: 2048, Storage: 20},
		}
		if i == 1 {
			for j := range 24 {
				entry.Tools = append(entry.Tools, fmt.Sprintf("tool-%02d@1.0", j))
			}
		}
		c.Images = append(c.Images, entry)
	}
	c.DefaultImages["pi"] = "dev-00"
	c.Projects["/work/current"] = config.Workspace{Agent: "pi", Agents: map[string]config.Project{
		"pi": {Profile: "work", VM: c.Images[1].VM},
	}}
	c.Projects["/work/alpha"] = config.Workspace{Agent: "pi", Agents: map[string]config.Project{
		"pi": {Profile: "private", VM: c.Images[1].VM},
	}}
	return newConfigStatusModel(images.DescribeStatus(c, "/config.toml", current, nil))
}

func TestConfigStatusLayoutAndNavigation(t *testing.T) {
	m := configStatusFixture("")
	resize := func(w, h int) {
		t.Helper()
		updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m = updated.(configStatusModel)
		lines := strings.Split(m.View(), "\n")
		if len(lines) > h {
			t.Fatalf("render has %d lines for %d-row terminal", len(lines), h)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > w {
				t.Fatalf("render wider than %d columns: %q", w, line)
			}
		}
	}
	key := func(kind tea.KeyType, runes ...rune) {
		updated, _ := m.Update(tea.KeyMsg{Type: kind, Runes: runes})
		m = updated.(configStatusModel)
	}
	resize(100, 24)
	key(tea.KeyDown)
	wide := ansi.Strip(m.View())
	if m.cursor != 1 || !strings.Contains(wide, "▸ ▹ dev-01") || !strings.Contains(wide, "│ dev-01") || !strings.Contains(wide, "fd@10.3.0") {
		t.Fatalf("expected initial selection and side-by-side details:\n%s", wide)
	}
	key(tea.KeyPgDown)
	if m.details.YOffset == 0 {
		t.Fatal("details did not scroll")
	}
	key(tea.KeyEnd)
	if !strings.Contains(ansi.Strip(m.View()), "tool-23@1.0") {
		t.Fatal("last tool unreachable")
	}
	resize(50, 17)
	stacked := ansi.Strip(m.View())
	if strings.Contains(stacked, "│") || !strings.Contains(stacked, "\ndev-01\n") {
		t.Fatalf("expected stacked layout:\n%s", stacked)
	}
	key(tea.KeyRunes, 'j')
	if m.cursor != 2 || m.details.YOffset != 0 || !strings.Contains(ansi.Strip(m.View()), "\ndev-02\n") {
		t.Fatal("changing image did not select fresh details")
	}
	for range 20 {
		key(tea.KeyDown)
	}
	if m.cursor != 17 || !strings.Contains(ansi.Strip(m.View()), "▸ dev-17") {
		t.Fatal("last image not visible after scrolling list")
	}
	resize(32, 12)
	resize(31, 11)
	resize(1, 1)
	if _, quit := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); quit == nil {
		t.Fatal("q did not exit")
	}
}

func TestConfigStatusOpensAndSelectsCurrentProject(t *testing.T) {
	m := configStatusFixture("/work/current")
	view := func() string { return ansi.Strip(m.View()) }
	key := func(kind tea.KeyType) {
		next, _ := m.Update(tea.KeyMsg{Type: kind})
		m = next.(configStatusModel)
	}
	for _, want := range []string{
		"▾ dev-01", "▸   └ current", "Saved selection · current [current folder]", "Path: /work/current",
		"Agent: pi (active for this path)", "Profile: work", "Saved image: dev-01 (missing)",
		"Agent default for new selections: dev-00",
	} {
		if !strings.Contains(view(), want) {
			t.Fatalf("missing %q:\n%s", want, view())
		}
	}
	key(tea.KeyLeft)
	if !strings.Contains(view(), "▸ ▾ dev-01") {
		t.Fatalf("left did not select parent:\n%s", view())
	}
	key(tea.KeyEnter)
	if strings.Contains(view(), "└ current") || !strings.Contains(view(), "▸ ▹ dev-01") {
		t.Fatalf("branch did not collapse:\n%s", view())
	}
	key(tea.KeyEnter)
	key(tea.KeyDown)
	if !strings.Contains(view(), "▸   └ current") {
		t.Fatalf("branch did not reopen to the current path:\n%s", view())
	}
	key(tea.KeyDown)
	if !strings.Contains(view(), "Path: /work/alpha") {
		t.Fatalf("next path not selected:\n%s", view())
	}
}

func TestConfigStatusLegend(t *testing.T) {
	m := configStatusFixture("/work/current")
	resize := func(w, h int) {
		next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
		m = next.(configStatusModel)
	}
	key := func(kind tea.KeyType, runes ...rune) {
		next, _ := m.Update(tea.KeyMsg{Type: kind, Runes: runes})
		m = next.(configStatusModel)
	}
	resize(32, 12)
	before := m.View()
	if !strings.Contains(ansi.Strip(before), "? legend") {
		t.Fatal("legend shortcut missing from narrow footer")
	}
	key(tea.KeyRunes, '?')
	legend := ansi.Strip(m.View())
	for _, want := range []string{"▾ dev-01", "└ current", "default —", "current —", "N saved —", "? hide"} {
		if !strings.Contains(legend, want) {
			t.Fatalf("missing %q:\n%s", want, legend)
		}
	}
	lines := strings.Split(legend, "\n")
	if len(lines) != 12 || !strings.HasPrefix(lines[8], "default —") || !strings.Contains(lines[11], "? hide") {
		t.Fatalf("legend is not anchored to the bottom of the 12-row terminal:\n%s", legend)
	}
	key(tea.KeyRunes, '?')
	if m.View() != before {
		t.Fatal("closing legend did not restore selection")
	}
	key(tea.KeyRunes, '?')
	resize(100, 24)
	key(tea.KeyDown)
	wide := ansi.Strip(m.View())
	if !strings.Contains(wide, "│ Saved selection · alpha") || !strings.Contains(wide, "N saved — Folder assignments, not running VMs.") {
		t.Fatalf("legend interrupted navigation:\n%s", wide)
	}
	key(tea.KeyEsc)
	if m.legend || !strings.Contains(ansi.Strip(m.View()), "│ Saved selection · alpha") {
		t.Fatal("Esc did not close legend while preserving selection")
	}
}

func TestConfigStatusEmptyCatalog(t *testing.T) {
	m := newConfigStatusModel(images.CatalogStatus{ConfigFile: "/config.toml"})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	view := ansi.Strip(updated.View())
	if !strings.Contains(view, "No managed builds") || strings.Contains(view, "│") {
		t.Fatalf("unexpected empty view:\n%s", view)
	}
}
