package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

type AgentPrompter interface {
	ChooseAgent(project, current string) (string, error)
}

func (p BubblePrompter) ChooseAgent(project, current string) (string, error) {
	if len(p.Agents) == 0 {
		return "", fmt.Errorf("agent picker requires a catalog")
	}
	m := agentModel{project: project, width: 64, agents: p.Agents}
	for i, name := range p.Agents.Names() {
		if name == current {
			m.cursor = i
		}
	}
	result, err := tea.NewProgram(m, tea.WithInput(p.In), tea.WithOutput(p.Out), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	chosen := result.(agentModel)
	if chosen.cancelled {
		return "", ErrCancelled
	}
	return p.Agents.Names()[chosen.cursor], nil
}

type agentModel struct {
	agents        agents.Catalog
	project       string
	cursor, width int
	cancelled     bool
}

func (m agentModel) Init() tea.Cmd { return nil }
func (m agentModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(16, min(msg.Width-4, 76))
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(m.agents)-1, m.cursor+1)
		case "enter":
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}
func (m agentModel) View() string {
	lines := []string{accent.Render("agentnook · Set up this folder"), muted.Render("Project: " + m.project),
		"", "Which agent do you normally use here?",
		muted.Render("This choice is remembered. Next time, run nook."), ""}
	for i, name := range m.agents.Names() {
		label := fmt.Sprintf("%s (%s)", m.agents.Label(name), name)
		if i == m.cursor {
			lines = append(lines, selected.Render("▸ "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", muted.Render("↑/↓/j/k move • enter select • esc/ctrl+c cancel"))
	return panel(m.width, strings.Join(lines, "\n"))
}
