package tui

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/images"
)

// ShowConfigStatus displays a read-only catalog snapshot in an alternate screen.
func ShowConfigStatus(status images.CatalogStatus, in io.Reader, out io.Writer) error {
	_, err := tea.NewProgram(newConfigStatusModel(status), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	return err
}

type configStatusModel struct {
	status        images.CatalogStatus
	width, height int
	cursor        int
	open          []bool
	rows          []configStatusRow
	details       viewport.Model
	legend        bool
}

type configStatusRow struct {
	image, assignment int // assignment is -1 for an image row.
}

func newConfigStatusModel(status images.CatalogStatus) configStatusModel {
	m := configStatusModel{
		status: status, width: 80, height: 24, details: viewport.New(1, 1),
		open: make([]bool, len(status.Images)),
	}
	selected := configStatusRow{0, -1}
	for i, image := range status.Images {
		if !image.Current {
			continue
		}
		for j, assignment := range image.Assignments {
			if assignment.CurrentFolder && assignment.Active {
				m.open[i] = true
				selected = configStatusRow{i, j}
				break
			}
		}
		break
	}
	m.buildRows()
	for i, row := range m.rows {
		if row == selected {
			m.cursor = i
			break
		}
	}
	m.updateDetails()
	return m
}

func (m *configStatusModel) buildRows() {
	rows := make([]configStatusRow, 0, len(m.status.Images))
	for i, image := range m.status.Images {
		rows = append(rows, configStatusRow{i, -1})
		if m.open[i] {
			for j := range image.Assignments {
				rows = append(rows, configStatusRow{i, j})
			}
		}
	}
	m.rows = rows
}

func (m *configStatusModel) selectRow(cursor int) {
	if cursor != m.cursor {
		m.cursor = cursor
		m.details.GotoTop()
		m.updateDetails()
	}
}

func (m *configStatusModel) toggleBranch(key string) {
	if len(m.rows) == 0 {
		return
	}
	row := m.rows[m.cursor]
	if row.assignment >= 0 {
		if key == "left" {
			m.selectRow(m.cursor - row.assignment - 1)
		}
		return
	}
	if len(m.status.Images[row.image].Assignments) == 0 {
		return
	}
	open := !m.open[row.image]
	if key == "right" {
		open = true
	} else if key == "left" {
		open = false
	}
	if open != m.open[row.image] {
		m.open[row.image] = open
		m.buildRows()
		m.updateDetails()
	}
}

func (m configStatusModel) Init() tea.Cmd { return nil }

func (m configStatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.updateDetails()
	case tea.KeyMsg:
		switch msg.String() {
		case "?":
			m.legend = !m.legend
			m.updateDetails()
		case "esc":
			if m.legend {
				m.legend = false
				m.updateDetails()
				return m, nil
			}
			return m, tea.Quit
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k", "down", "j":
			delta := 1
			if msg.String() == "up" || msg.String() == "k" {
				delta = -1
			}
			m.selectRow(max(0, min(len(m.rows)-1, m.cursor+delta)))
		case "enter", "left", "right":
			m.toggleBranch(msg.String())
		case "pgup", "ctrl+u":
			m.details.ViewUp()
		case "pgdown", "ctrl+d":
			m.details.ViewDown()
		case "home":
			m.details.GotoTop()
		case "end":
			m.details.GotoBottom()
		}
	}
	return m, nil
}

const configColumnGap = " │ "

type configStatusLayout struct {
	listWidth, listRows, detailWidth, detailRows int
	split                                        bool
}

func (m configStatusModel) layout() configStatusLayout {
	body := m.height - m.headerRows() - len(m.footer())
	if m.width >= 76 {
		left := min(36, max(24, m.width/3))
		return configStatusLayout{
			listWidth: left, listRows: max(1, (body-1)/2), split: true,
			detailWidth: m.width - left - ansi.StringWidth(configColumnGap), detailRows: max(1, body-1),
		}
	}
	rows := min(len(m.rows), max(2, (body-2)/4))
	return configStatusLayout{
		listWidth: m.width, listRows: rows,
		detailWidth: m.width, detailRows: max(1, body-2-rows*2),
	}
}

func (m configStatusModel) selectionDetails() (string, []string) {
	row := m.rows[m.cursor]
	image := m.status.Images[row.image]
	if row.assignment < 0 {
		return image.Title(), image.Details()
	}
	assignment := image.Assignments[row.assignment]
	agent := assignment.Agent
	if assignment.Active {
		agent += " (active for this path)"
	}
	if assignment.AgentDefault == "" {
		assignment.AgentDefault = "none configured"
	}
	title := "Saved selection · " + filepath.Base(assignment.Path)
	if assignment.CurrentFolder {
		title += " [current folder]"
	}
	return title, []string{
		"Path: " + assignment.Path,
		"Agent: " + agent,
		"Profile: " + assignment.Profile,
		"Saved image: " + image.Name + " (" + image.ArtifactState + ")",
		"Agent default for new selections: " + assignment.AgentDefault,
		assignment.EnvironmentDetail(),
	}
}

func (m *configStatusModel) updateDetails() {
	if len(m.status.Images) == 0 {
		return
	}
	layout := m.layout()
	m.details.Width, m.details.Height = layout.detailWidth, layout.detailRows
	title, lines := m.selectionDetails()
	wrapped := make([]string, len(lines))
	for i, line := range lines {
		wrapped[i] = ansi.Wrap(monitorText(line), layout.detailWidth, "/")
	}
	// Keep a truncated title (long image name and badges) readable by scrolling.
	title = monitorText(title)
	if ansi.StringWidth(title) > layout.detailWidth {
		wrapped = append([]string{ansi.Wrap(title, layout.detailWidth, "")}, wrapped...)
	}
	m.details.SetContent(strings.Join(wrapped, "\n"))
	m.details.SetYOffset(m.details.YOffset)
}

func (m configStatusModel) list(layout configStatusLayout) []string {
	start := max(0, m.cursor-layout.listRows+1)
	end := min(len(m.rows), start+layout.listRows)
	heading := "Images"
	if end-start < len(m.rows) {
		heading = fmt.Sprintf("Images · rows %d–%d/%d", start+1, end, len(m.rows))
	}
	lines := []string{monitorTitle.Render(heading)}
	for i := start; i < end; i++ {
		row := m.rows[i]
		image := m.status.Images[row.image]
		marker := "  "
		if i == m.cursor {
			marker = selectionMarker
		}
		label, status, meta := m.rowLabels(row, image)
		nameWidth := layout.listWidth - ansi.StringWidth(marker+status) - 1
		name := ansi.Truncate(monitorText(label), nameWidth, "…")
		padding := strings.Repeat(" ", max(1, nameWidth+1-ansi.StringWidth(name)))
		line := marker + name + padding
		switch {
		case i == m.cursor:
			line = monitorSelected.Render(line + status)
		case row.assignment < 0 && image.Warning:
			line += monitorWarning.Render(status)
		default:
			line += monitorMuted.Render(status)
		}
		lines = append(lines, line)
		lines = append(lines, monitorMuted.Render(ansi.Truncate(monitorText(meta), layout.listWidth, "…")))
	}
	return lines
}

func (m configStatusModel) rowLabels(row configStatusRow, image images.ImageStatus) (label, status, meta string) {
	if row.assignment >= 0 {
		assignment := image.Assignments[row.assignment]
		if assignment.CurrentFolder {
			status = "current"
		}
		return "  └ " + filepath.Base(assignment.Path), status, "      " + assignment.Path
	}
	label, status, meta = image.Name, image.ArtifactState, "  "+image.Agent
	if len(image.Assignments) > 0 {
		branch := "▹ "
		if m.open[row.image] {
			branch = "▾ "
		}
		label = branch + label
		meta = fmt.Sprintf("  %d saved · %s", len(image.Assignments), image.Agent)
	}
	if image.Default {
		meta += " · default"
	}
	return label, status, meta
}

func (m configStatusModel) headerRows() int {
	if m.legend {
		// In short panes, reclaim header space before hiding list rows.
		return min(3, max(1, m.height-11))
	}
	return 4
}

func (m configStatusModel) footer() []string {
	legendKey := "? legend"
	var lines []string
	if m.legend {
		legendKey = "? hide legend"
		for _, entry := range [][2]string{
			{"default — Agent fallback image", "default — Agent’s image when this folder has no saved choice."},
			{"current — This folder’s choice", "current — Saved choice for this folder; the active agent determines what starts."},
			{"N saved — Folder assignments", "N saved — Folder assignments, not running VMs."},
		} {
			text := entry[1]
			if ansi.StringWidth(text) > m.width {
				text = entry[0]
			}
			lines = append(lines, monitorMuted.Render(text))
		}
	}
	help := legendKey + " · q quit"
	if len(m.rows) > 0 {
		help = "↑↓/jk select · Enter toggle · ← parent · PgUp/PgDn details · " + help
		if ansi.StringWidth(help) > m.width {
			help = "↑↓ Enter PgUp/PgDn " + legendKey + " q"
			if ansi.StringWidth(help) > m.width {
				help = "↑↓ Enter PgUp/PgDn ? hide q"
			}
		}
	}
	return append(lines, monitorMuted.Render(help))
}

func (m configStatusModel) withFooter(lines []string) string {
	footer := m.footer()
	if m.legend {
		for len(lines)+len(footer) < m.height {
			lines = append(lines, "")
		}
	}
	return monitorView(append(lines, footer...), m.width)
}

func (m configStatusModel) View() string {
	if m.width < 32 || m.height < 12 {
		lines := strings.Split(ansi.Hardwrap("Image status needs at least 32×12. Resize or q to quit.", m.width, true), "\n")
		return monitorView(lines[:min(len(lines), m.height)], m.width)
	}
	lines := []string{
		monitorTitle.Render("agentnook · Image Status"),
		monitorMuted.Render("Config: " + monitorText(m.status.ConfigFile)),
		monitorMuted.Render(fmt.Sprintf("%d managed images · read-only", len(m.status.Images))),
		"",
	}
	lines = lines[:m.headerRows()]
	if len(m.status.Images) == 0 {
		lines = append(lines, strings.Split(ansi.Wrap(images.NoManagedBuilds, m.width, ""), "\n")...)
		return m.withFooter(lines)
	}
	layout := m.layout()
	list := m.list(layout)
	title, _ := m.selectionDetails()
	details := []string{monitorTitle.Render(ansi.Truncate(monitorText(title), layout.detailWidth, "…"))}
	details = append(details, strings.Split(m.details.View(), "\n")...)
	if layout.split {
		divider := monitorMuted.Render(configColumnGap)
		for i, right := range details {
			left := ""
			if i < len(list) {
				left = list[i]
			}
			lines = append(lines, left+strings.Repeat(" ", max(0, layout.listWidth-ansi.StringWidth(left)))+divider+right)
		}
	} else {
		lines = append(lines, list...)
		lines = append(lines, details...)
	}
	return m.withFooter(lines)
}
