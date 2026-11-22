package tui

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/fleet"
)

type FleetLoader func(context.Context) (fleet.Snapshot, error)

// WatchFleet is display-only. Cancellation stops discovery, not any VM.
func WatchFleet(project string, load FleetLoader, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := tea.NewProgram(newFleetModel(ctx, project, load), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	return err
}

type fleetRefreshMsg struct{}
type fleetResultMsg struct {
	snapshot fleet.Snapshot
	err      error
	at       time.Time
}

type fleetModel struct {
	ctx         context.Context
	project     string
	load        FleetLoader
	snapshot    fleet.Snapshot
	cursor      int
	width       int
	height      int
	details     viewport.Model
	lastSuccess time.Time
	errText     string
	loaded      bool
}

func newFleetModel(ctx context.Context, project string, load FleetLoader) fleetModel {
	m := fleetModel{ctx: ctx, project: project, load: load, width: 100, height: 36, details: viewport.New(100, 20)}
	m.rebuildDetails()
	return m
}

func (m fleetModel) fetch() tea.Msg {
	snapshot, err := m.load(m.ctx)
	return fleetResultMsg{snapshot: snapshot, err: err, at: time.Now()}
}

func (m fleetModel) Init() tea.Cmd { return m.fetch }

func (m fleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width-2), max(1, msg.Height)
		m.rebuildDetails()
	case fleetRefreshMsg:
		return m, m.fetch
	case fleetResultMsg:
		if msg.err != nil {
			m.errText = msg.err.Error() // Keep VM data, but never imply fresh agent activity.
			for i := range m.snapshot.Instances {
				m.snapshot.Instances[i].Activity.Fresh = false
			}
		} else {
			previous := ""
			if len(m.snapshot.Instances) > 0 {
				previous = m.snapshot.Instances[m.cursor].Name
			}
			m.snapshot, m.lastSuccess, m.errText, m.loaded = msg.snapshot, msg.at, "", true
			m.cursor = min(m.cursor, max(0, len(m.snapshot.Instances)-1))
			for i, item := range m.snapshot.Instances {
				if item.Name == previous {
					m.cursor = i
					break
				}
			}
			if len(m.snapshot.Instances) == 0 || m.snapshot.Instances[m.cursor].Name != previous {
				m.details.GotoTop()
			}
		}
		m.rebuildDetails()
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return fleetRefreshMsg{} })
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k", "down", "j":
			delta := 1
			if msg.String() == "up" || msg.String() == "k" {
				delta = -1
			}
			next := max(0, min(m.cursor+delta, len(m.snapshot.Instances)-1))
			if next != m.cursor {
				m.cursor = next
				m.details.GotoTop()
				m.rebuildDetails()
			}
		case "pgdown", "ctrl+d":
			m.details.HalfViewDown()
		case "pgup", "ctrl+u":
			m.details.HalfViewUp()
		case "home":
			m.details.GotoTop()
		case "end":
			m.details.GotoBottom()
		}
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.details, cmd = m.details.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m fleetModel) tableCount() int {
	return min(len(m.snapshot.Instances), max(1, min(8, (m.height-17)/2)))
}

func (m fleetModel) heading() []string {
	lines := []string{accent.Render("agentnook fleet"), muted.Render(fleetText(m.project))}
	summary := "Discovering instances…"
	if m.loaded {
		counts := map[string]int{}
		for _, item := range m.snapshot.Instances {
			counts[item.State]++
		}
		summary = fmt.Sprintf("%d instances", len(m.snapshot.Instances))
		for _, state := range []string{"running", "unreachable", "frozen"} {
			if counts[state] > 0 {
				summary += fmt.Sprintf(" · %d %s", counts[state], state)
			}
		}
		summary += " · updated " + m.lastSuccess.Local().Format("15:04:05")
	}
	if m.errText != "" {
		summary = "Discovery unavailable · retrying"
		if m.loaded {
			summary = "STALE · last success " + m.lastSuccess.Local().Format("15:04:05")
		}
	}
	lines = append(lines, muted.Render(ansi.Truncate(summary, m.width, "…")))
	if m.errText != "" {
		lines = append(lines, errorStyle.Render(ansi.Truncate(fleetText(m.errText), m.width, "…")))
	}
	if m.snapshot.Warning != "" {
		lines = append(lines, muted.Render(ansi.Truncate(fleetText(m.snapshot.Warning), m.width, "…")))
	}
	return lines
}

func (m fleetModel) table() []string {
	if len(m.snapshot.Instances) == 0 {
		return nil
	}
	compact := m.width < 110
	widths := []int{m.width - 83, 11, 11, 12, 10, 5, 9, 9, 10, 6}
	headers := []string{"INSTANCE", "AGENT", "PROFILE", "VM STATUS", "ACTIVITY", "vCPU", "RAM", "VERSION¹", "UPTIME", "AGE²"}
	if compact {
		widths, headers = []int{m.width - 34, 12, 11, 11}, []string{"INSTANCE", "VM STATUS", "ACTIVITY", "UPTIME"}
	}
	row := func(values []string) string {
		var result strings.Builder
		for i, value := range values {
			value = ansi.Truncate(fleetText(value), widths[i]-1, "…")
			result.WriteString(value)
			result.WriteString(strings.Repeat(" ", max(0, widths[i]-lipgloss.Width(value))))
		}
		return result.String()
	}
	lines := []string{muted.Render(row(headers))}
	start := max(0, m.cursor-m.tableCount()+1)
	for i := start; i < min(len(m.snapshot.Instances), start+m.tableCount()); i++ {
		item := m.snapshot.Instances[i]
		marker := "  "
		if i == m.cursor {
			marker = "▸ "
		}
		values := []string{marker + item.Name, item.Agent, item.Profile, item.State, item.Activity.State(), strconv.Itoa(item.CPUs), fleetMemory(item.MemoryMiB), unknown(item.AgentVersion), fleetUptime(item), fleetAge(item.CreatedAt, m.lastSuccess)}
		if compact {
			values = []string{marker + item.Name, item.State, item.Activity.State(), fleetUptime(item)}
		}
		line := row(values)
		if i == m.cursor {
			line = selected.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *fleetModel) rebuildDetails() {
	m.details.Width = m.width
	// heading + blank + table + blank + selected heading + blank + footer.
	m.details.Height = max(1, m.height-len(m.heading())-len(m.table())-7)
	if len(m.snapshot.Instances) == 0 {
		m.details.SetContent("")
		return
	}
	item := m.snapshot.Instances[m.cursor]
	var lines []string
	add := func(text string) { lines = append(lines, ansi.Hardwrap(text, m.width, true)) }
	add(accent.Render("Machine"))
	image := item.Image
	if item.ImageName != "" {
		image = item.ImageName + " · " + item.Image
	}
	pairs := [][2]string{
		{"Instance", item.Name}, {"Agent", unknown(item.Agent)}, {"Image", unknown(image)}, {"Linux base", unknown(item.BaseImage)},
		{"CPU / RAM", fmt.Sprintf("%d vCPU / %s (limits)", item.CPUs, fleetMemory(item.MemoryMiB))},
		{"Version¹", unknown(item.AgentVersion) + " (image catalog; not guest-verified)"},
		{"Storage", fleetOptional(item.StorageGiB, " GiB")}, {"PID", fleetOptional(item.PID, "")},
		{"Created²", fleetCreated(item.CreatedAt)}, {"Activity", item.Activity.State()},
		{"Runtime", fleetUptime(item) + " (agent runtime, not VM or session)"},
		{"Heartbeat", fleetHeartbeat(item, m.lastSuccess)},
	}
	for _, pair := range pairs {
		add(fmt.Sprintf("%-12s%s", pair[0], fleetText(pair[1])))
	}
	if !item.Activity.Fresh {
		add(muted.Render("Agent telemetry missing, awaiting progress or stale; activity and uptime unknown."))
	}
	if item.State == "unreachable" {
		add(errorStyle.Render("VM unreachable according to smolvm; Agent telemetry is evaluated independently."))
	}
	lines = append(lines, "", accent.Render(fmt.Sprintf("Mounts · %d (recorded at launch)", len(item.Mounts))))
	for _, mount := range item.Mounts {
		mode := "rw"
		if mount.ReadOnly {
			mode = "ro"
		}
		add(fleetText(mount.Host) + " → " + fleetText(mount.Guest) + " [" + mode + "]")
	}
	m.details.SetContent(strings.Join(lines, "\n"))
}

func (m fleetModel) View() string {
	if m.width < 44 || m.height < 14 {
		return ansi.Hardwrap("Terminal too small (minimum 46×14). Resize or q to quit.", m.width, true)
	}
	lines := m.heading()
	lines = append(lines, "")
	if len(m.snapshot.Instances) == 0 {
		message := "Discovering instances…"
		if m.errText != "" && !m.loaded {
			message = "Cannot discover instances. Watch will retry."
		} else if m.loaded {
			message = "No running agentnook instances for this exact directory.\nStart nook here in another terminal; watch stays open."
		}
		lines = append(lines, ansi.Hardwrap(message, m.width, true), "")
	} else {
		lines = append(lines, m.table()...)
		detailHeading := fmt.Sprintf("── %s · %s · %d/%d", fleetText(m.snapshot.Instances[m.cursor].Name), fleetText(m.snapshot.Instances[m.cursor].Profile), m.cursor+1, len(m.snapshot.Instances))
		if m.details.TotalLineCount() > m.details.Height {
			detailHeading += fmt.Sprintf(" · details %.0f%% (PgUp/PgDn)", 100*m.details.ScrollPercent())
		}
		lines = append(lines, "", muted.Render(ansi.Truncate(detailHeading, m.width, "…")), "", m.details.View(), "")
	}
	lines = append(lines,
		muted.Render(ansi.Truncate("¹ Image metadata · ² Record age, not uptime · CPU/RAM limits", m.width, "…")),
		muted.Render(ansi.Truncate("↑↓/jk select · PgUp/PgDn scroll details · q quit watch only", m.width, "…")),
	)
	// The project path may be much wider than the terminal; never let it wrap
	// outside Bubble Tea's known screen dimensions.
	for i := range lines {
		if !strings.Contains(lines[i], "\n") {
			lines[i] = ansi.Truncate(lines[i], m.width, "…")
		}
	}
	return strings.Join(lines, "\n")
}

func fleetText(value string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, value)
}

func unknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func fleetMemory(mib int) string {
	if mib%1024 == 0 {
		return fmt.Sprintf("%d GiB", mib/1024)
	}
	return fmt.Sprintf("%d MiB", mib)
}

func fleetOptional(value *int, suffix string) string {
	if value == nil {
		return "unknown"
	}
	return strconv.Itoa(*value) + suffix
}

func fleetCreated(value time.Time) string {
	if value.IsZero() {
		return "unknown"
	}
	return value.Local().Format(time.RFC3339)
}

func fleetUptime(item fleet.Instance) string {
	if !item.Activity.Fresh {
		return "unknown"
	}
	return (time.Duration(item.Activity.Report.UptimeSeconds) * time.Second).String()
}

func fleetHeartbeat(item fleet.Instance, now time.Time) string {
	if item.Activity.HeartbeatAt.IsZero() {
		return "unknown · awaiting heartbeat progress"
	}
	age := max(time.Duration(0), now.Sub(item.Activity.HeartbeatAt)).Truncate(time.Second)
	value := age.String() + " ago (host-observed)"
	if !item.Activity.Fresh {
		value += " · stale / unavailable"
	}
	return value
}

func fleetAge(created, now time.Time) string {
	if created.IsZero() || now.Before(created) {
		return "?"
	}
	d := now.Sub(created)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
