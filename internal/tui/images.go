package tui

import (
	"fmt"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/images"
)

type ImagePrompter interface {
	ChooseImage(project string, options config.Options, entries []config.BuiltImage) (string, error)
}

func (p BubblePrompter) ChooseImage(project string, options config.Options, entries []config.BuiltImage) (string, error) {
	result, err := tea.NewProgram(newImageModel(project, options, entries, p.Agents),
		tea.WithInput(p.In), tea.WithOutput(p.Out), tea.WithAltScreen()).Run()
	if err != nil {
		return "", err
	}
	m := result.(imageModel)
	if m.cancelled {
		return "", ErrCancelled
	}
	return m.name, nil
}

// imageModel is used both standalone and as the launcher's image submenu.
type imageModel struct {
	agents    agents.Catalog
	project   string
	options   config.Options
	entries   []config.BuiltImage
	cursor    int
	width     int
	height    int
	done      bool
	cancelled bool
	name      string
	errorText string
}

func newImageModel(project string, options config.Options, entries []config.BuiltImage, catalog agents.Catalog) imageModel {
	return imageModel{project: project, options: options, entries: entries, agents: catalog, width: 76, height: 24}
}

func (m imageModel) Init() tea.Cmd { return nil }

func (m imageModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(16, min(msg.Width-4, 100)), msg.Height
	case tea.KeyMsg:
		m.errorText = ""
		switch msg.String() {
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			m.cursor = min(len(m.entries), m.cursor+1)
		case "enter":
			if m.cursor > 0 {
				entry := m.entries[m.cursor-1]
				if !images.Available(entry) || entry.Architecture != runtime.GOARCH {
					m.errorText = "Image unavailable: archive missing/empty or architecture does not match host."
					return m, nil
				}
				m.name = entry.Name
			}
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m imageModel) View() string {
	lines := []string{accent.Render("agentnook · Images"), muted.Render("Project: " + m.project),
		images.Summary(m.options, m.entries, m.agents), "", "Built images · CPU/RAM are launch presets", ""}
	// Keep highlighted row visible even with a large build catalog.
	count := max(1, (m.height-18)/2)
	start := max(0, m.cursor-count+1)
	end := min(len(m.entries)+1, start+count)
	for index := start; index < end; index++ {
		label := "Keep current image and CPU/RAM"
		if index > 0 {
			entry := m.entries[index-1]
			status := "missing"
			if images.Available(entry) {
				status = "ready"
			}
			if entry.Architecture != runtime.GOARCH {
				status = "wrong architecture"
			}
			label = fmt.Sprintf("%s [%s]\n    %s · %s %s · CPU %d · RAM %d MiB",
				entry.Name, status, entry.BaseImage, m.agents.Label(entry.VM.Agent), entry.VM.AgentVersion, entry.VM.CPUs, entry.VM.Memory)
		}
		if index == m.cursor {
			lines = append(lines, selected.Render("▸ "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	if len(m.entries) == 0 {
		lines = append(lines, muted.Render("No managed builds. Use: nook images build NAME"))
	}
	if m.cursor > 0 {
		entry := m.entries[m.cursor-1]
		lines = append(lines, "", muted.Render("Built: "+entry.BuiltAt+" · linux/"+entry.Architecture),
			muted.Render("Artifact: "+entry.VM.Image))
	}
	if m.errorText != "" {
		lines = append(lines, errorStyle.Render(m.errorText))
	}
	lines = append(lines, "", muted.Render("↑/↓/j/k move • enter apply • esc back • ctrl+c cancel"))
	return panel(m.width, strings.Join(lines, "\n"))
}

func panel(width int, content string) string {
	return panelStyle().Width(width).Render(content)
}

// Compile-time check that the concrete launcher supports a separate image picker.
var _ ImagePrompter = BubblePrompter{}
