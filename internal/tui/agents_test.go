package tui

import (
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAgentPickerNavigationAndCancellation(t *testing.T) {
	m := agentModel{project: "/repo", width: 64, agents: registry.All()}
	for _, name := range []string{"Pi", "OpenCode", "Claude Code"} {
		if !strings.Contains(m.View(), name) {
			t.Fatal("missing", name)
		}
	}
	for i := 0; i < 5; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(agentModel)
	}
	if m.cursor != 2 {
		t.Fatal(m.cursor)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if next.(agentModel).cancelled || cmd == nil {
		t.Fatal("select failed")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !next.(agentModel).cancelled || cmd == nil {
		t.Fatal("cancel failed")
	}
}
