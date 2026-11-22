// Package tui implements the agentenv-style select/create profile flow.
package tui

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/images"
)

var ErrCancelled = errors.New("selection cancelled")

type Choice struct {
	Profile string
	Create  bool
	Image   string // Optional managed build selected in the image submenu.
}

type Prompter interface {
	ChooseProfile(project string, profiles []config.Profile, current string) (Choice, error)
}

type BubblePrompter struct {
	Agents   agents.Catalog
	In       io.Reader
	Out      io.Writer
	Options  config.Options
	Images   []config.BuiltImage
	Advanced bool // Show image management only when explicitly changing the setup.
}

func (p BubblePrompter) ChooseProfile(project string, profiles []config.Profile, current string) (Choice, error) {
	result, err := tea.NewProgram(
		newLauncherModel(project, profiles, current, p.Options, p.Images, p.Agents, p.Advanced),
		tea.WithInput(p.In), tea.WithOutput(p.Out), tea.WithAltScreen(),
	).Run()
	if err != nil {
		return Choice{}, err
	}
	m := result.(model)
	if m.cancelled {
		return Choice{}, ErrCancelled
	}
	return m.choice, nil
}

type model struct {
	agents    agents.Catalog
	project   string
	profiles  []config.Profile
	cursor    int
	creating  bool
	input     textinput.Model
	errorText string
	choice    Choice
	cancelled bool
	width     int
	height    int
	current   string
	launcher  bool
	advanced  bool
	options   config.Options
	images    []config.BuiltImage
	imageName string
	picker    *imageModel
}

func newModel(project string, profiles []config.Profile, current string) model {
	input := textinput.New()
	input.Placeholder = "profile-name"
	input.CharLimit = 64
	input.Focus()
	m := model{project: project, profiles: profiles, input: input, width: 64, height: 24}
	for i, profile := range profiles {
		if profile.Name == current {
			m.cursor = i
		}
	}
	return m
}

func newLauncherModel(project string, profiles []config.Profile, current string, options config.Options, entries []config.BuiltImage, catalog agents.Catalog, advanced bool) model {
	m := newModel(project, profiles, current)
	m.current, m.options, m.images, m.launcher, m.agents, m.advanced = current, options, entries, true, catalog, advanced
	if len(profiles) == 0 && !advanced {
		m.creating = true
		m.input.SetValue("default")
	}
	return m
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = max(16, min(size.Width-4, 76)), size.Height
	}
	if m.picker != nil {
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}
		next, cmd := m.picker.Update(msg)
		picker := next.(imageModel)
		if picker.done || picker.cancelled {
			if picker.name != "" {
				for _, entry := range m.images {
					if entry.Name == picker.name {
						m.options, m.imageName = entry.VM, entry.Name
					}
				}
			}
			m.picker = nil
			return m, nil // Do not forward the child's tea.Quit to the launcher.
		}
		m.picker = &picker
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()
		if key == "esc" || key == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}
		if m.creating {
			if key == "enter" {
				name := strings.TrimSpace(m.input.Value())
				if err := config.ValidateProfileName(name); err != nil {
					m.errorText = err.Error()
					return m, nil
				}
				for _, profile := range m.profiles {
					if strings.EqualFold(profile.Name, name) {
						m.errorText = fmt.Sprintf("Profile %q already exists", name)
						return m, nil
					}
				}
				m.choice = Choice{Profile: name, Create: true, Image: m.imageName}
				return m, tea.Quit
			}
			m.errorText = ""
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		switch key {
		case "up", "k":
			m.cursor = max(0, m.cursor-1)
		case "down", "j":
			last := len(m.profiles)
			if m.advanced {
				last++
			}
			m.cursor = min(last, m.cursor+1)
		case "i":
			if m.advanced {
				m.openImages()
			}
		case "enter":
			if m.advanced && m.cursor == len(m.profiles)+1 {
				m.openImages()
				return m, nil
			}
			if m.cursor == len(m.profiles) {
				m.creating = true
				return m, nil
			}
			m.choice = Choice{Profile: m.profiles[m.cursor].Name, Image: m.imageName}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *model) openImages() {
	picker := newImageModel(m.project, m.options, m.images, m.agents)
	picker.width, picker.height = m.width, m.height
	m.picker = &picker
}

var (
	accent     = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	selected   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	muted      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

func (m model) View() string {
	if m.picker != nil {
		return m.picker.View()
	}
	lines := []string{accent.Render("agentnook"), muted.Render("Project: " + m.project), ""}
	if m.launcher {
		profile := m.current
		if profile == "" {
			profile = "(choose below)"
		}
		if m.advanced {
			lines = append(lines, "Profile: "+profile, images.Summary(m.options, m.images, m.agents), "")
		} else {
			lines = append(lines, "Agent: "+m.agents.Label(m.options.Agent),
				"Saved for this folder. Next time, run nook.", "")
		}
	}
	if m.creating {
		lines = append(lines,
			accent.Render("Create a Profile"),
			"Letters, numbers, dot, dash, underscore; max 64 characters.",
			"", m.input.View(), "",
			muted.Render("enter create • esc/ctrl+c cancel"),
		)
		if m.errorText != "" {
			lines = append(lines, errorStyle.Render(m.errorText))
		}
	} else {
		lines = append(lines, accent.Render("Select a Profile"), "Keep logins, settings and sessions separate from other profiles.", "")
		total := len(m.profiles) + 1
		if m.advanced {
			total++
		}
		count := max(1, m.height-18)
		start := max(0, m.cursor-count+1)
		for index := start; index < min(total, start+count); index++ {
			label := "＋ Create new profile"
			if index < len(m.profiles) {
				label = m.profiles[index].Name
			} else if index > len(m.profiles) {
				label = "Browse / select images (i)"
			}
			lines = append(lines, m.item(index, label))
		}
		help := "↑/↓/j/k move • enter select • esc/ctrl+c cancel"
		if m.advanced {
			help = "↑/↓/j/k move • i images • enter select • esc/ctrl+c cancel"
		}
		lines = append(lines, "", muted.Render(help))
	}
	return panel(m.width, strings.Join(lines, "\n"))
}

func panelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).Padding(1, 2)
}

func (m model) item(index int, label string) string {
	if index == m.cursor {
		return selected.Render("▸ " + label)
	}
	return "  " + label
}
