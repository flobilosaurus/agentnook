package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
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
func WatchFleet(scope string, load FleetLoader, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := tea.NewProgram(newFleetModel(ctx, scope, load), tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	return err
}

type fleetRefreshMsg struct{}
type fleetResultMsg struct {
	snapshot fleet.Snapshot
	err      error
	at       time.Time
}

type fleetModel struct {
	ctx                  context.Context
	scope                string
	load                 FleetLoader
	snapshot             fleet.Snapshot
	nodes                []fleetNode
	navCursor, navOffset int
	filtered             []int
	cursor               int // Index in snapshot, not in the filtered list.
	navigatorFocus       bool
	width, height        int
	details              viewport.Model
	lastSuccess          time.Time
	errText, notice      string
	loaded               bool
}

type fleetLayout struct {
	split                              bool
	bodyHeight, navWidth, navHeight    int
	inspectorWidth, rows, detailHeight int
}

func newFleetModel(ctx context.Context, scope string, load FleetLoader) fleetModel {
	m := fleetModel{ctx: ctx, scope: scope, load: load, width: 100, height: 36, navigatorFocus: true, details: viewport.New(60, 20)}
	m.rebuildDetails()
	return m
}

func (m fleetModel) fetch() tea.Msg {
	snapshot, err := m.load(m.ctx)
	return fleetResultMsg{snapshot: snapshot, err: err, at: time.Now()}
}

func (m fleetModel) Init() tea.Cmd { return m.fetch }

func (m fleetModel) selection() (string, string) {
	if item := m.currentInstance(); item != nil {
		return item.Name, item.Project
	}
	return "", ""
}

func (m fleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width-2), max(1, msg.Height)
		m.rebuildDetails()
	case fleetRefreshMsg:
		return m, m.fetch
	case fleetResultMsg:
		previous, workspace := m.selection()
		if msg.err != nil {
			m.errText = msg.err.Error()
			for i := range m.snapshot.Instances {
				m.snapshot.Instances[i].Activity.Fresh = false
			}
		} else {
			m.snapshot, m.lastSuccess, m.errText, m.loaded = msg.snapshot, msg.at, "", true
			m.rebuildNavigator(previous, workspace)
			if current, _ := m.selection(); current != previous {
				m.details.GotoTop()
				if previous != "" {
					m.notice = "Selected instance ended or left this scope; selection moved."
				}
			}
		}
		m.rebuildDetails()
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return fleetRefreshMsg{} })
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab", "shift+tab":
			m.navigatorFocus = !m.navigatorFocus
		case "left", "h":
			m.navigatorFocus = true
		case "right", "l":
			m.navigatorFocus = false
		case "up", "k", "down", "j":
			delta := 1
			if msg.String() == "up" || msg.String() == "k" {
				delta = -1
			}
			previous, _ := m.selection()
			m.moveVM(delta)
			if current, _ := m.selection(); current != previous {
				m.details.GotoTop()
			}
			m.notice = ""
		case "pgdown", "ctrl+d":
			m.details.HalfViewDown()
		case "pgup", "ctrl+u":
			m.details.HalfViewUp()
		case "home":
			m.details.GotoTop()
		case "end":
			m.details.GotoBottom()
		}
		m.rebuildDetails()
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.details, cmd = m.details.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m fleetModel) heading() []string {
	lines := []string{accent.Render("agentnook fleet"), muted.Render(fleetText(m.scope))}
	summary := "Discovering instances…"
	if m.loaded {
		indices := make([]int, len(m.snapshot.Instances))
		for i := range indices {
			indices[i] = i
		}
		summary = fleetCounts(m.snapshot.Instances, indices) + " · updated " + m.lastSuccess.Local().Format("15:04:05")
	}
	if m.errText != "" {
		summary = "Discovery unavailable · retrying"
		if m.loaded {
			summary = "STALE · last success " + m.lastSuccess.Local().Format("15:04:05")
		}
	}
	lines = append(lines, muted.Render(summary))
	if m.errText != "" {
		lines = append(lines, errorStyle.Render(fleetText(m.errText)))
	}
	if m.snapshot.Warning != "" {
		lines = append(lines, muted.Render(fleetText(m.snapshot.Warning)))
	}
	if m.notice != "" {
		lines = append(lines, muted.Render(m.notice))
	}
	return lines
}

func (m fleetModel) layout() fleetLayout {
	l := fleetLayout{bodyHeight: max(1, m.height-len(m.heading())-4)}
	l.split = m.width >= 100 && m.height >= 20
	l.inspectorWidth = m.width
	if l.split {
		l.navWidth = min(42, m.width/3)
		l.navHeight = l.bodyHeight - 1
		l.inspectorWidth = m.width - l.navWidth - 3
	} else {
		l.navWidth = m.width
		l.navHeight = max(1, min(len(m.nodes), l.bodyHeight/4))
	}
	l.rows = min(len(m.filtered), max(1, min(6, (l.bodyHeight-5)/3)))
	l.detailHeight = l.bodyHeight - 3 - l.rows
	if !l.split {
		l.detailHeight -= 1 + l.navHeight
	}
	l.detailHeight = max(1, l.detailHeight)
	return l
}

func (m fleetModel) navigator(l fleetLayout) []string {
	title := "Navigator"
	if m.navigatorFocus {
		title += " · FOCUS"
	}
	lines := []string{accent.Render(title)}
	start := max(0, min(m.navOffset, max(0, len(m.nodes)-l.navHeight)))
	for i := start; i < min(len(m.nodes), start+l.navHeight); i++ {
		n := m.nodes[i]
		prefix := strings.Repeat("  ", n.depth)
		if n.depth > 0 {
			prefix += "└─ "
		}
		label := prefix + n.label
		if n.kind == "VM" {
			item := m.snapshot.Instances[n.indices[0]]
			label += " · " + item.Agent
		} else {
			count := fleetCounts(m.snapshot.Instances, n.indices)
			// Keep compact counts readable without secondary path rows.
			room := l.navWidth - lipgloss.Width(count) - 2
			if room >= lipgloss.Width(prefix)+5 {
				label = ansi.Truncate(fleetText(label), room, "…")
				label += strings.Repeat(" ", max(1, l.navWidth-lipgloss.Width(label)-lipgloss.Width(count))) + count
			} else {
				label += " · " + strconv.Itoa(len(n.indices)) + " VM"
			}
		}
		if n.kind == "VM" && i == m.navCursor {
			label = selected.Render(label)
		}
		lines = append(lines, label)
	}
	return lines
}

func (m fleetModel) table(width, rows int) []string {
	if len(m.filtered) == 0 {
		return nil
	}
	widths := []int{width - 34, 12, 11, 11}
	headers := []string{"INSTANCE", "VM STATUS", "ACTIVITY", "UPTIME"}
	if width >= 80 {
		widths[0] -= 12
		widths = append(widths[:1], append([]int{12}, widths[1:]...)...)
		headers = []string{"INSTANCE", "AGENT", "VM STATUS", "ACTIVITY", "UPTIME"}
	}
	row := func(values []string) string {
		var result strings.Builder
		for i, value := range values {
			value = ansi.Truncate(fleetText(value), max(1, widths[i]-1), "…")
			result.WriteString(value)
			result.WriteString(strings.Repeat(" ", max(0, widths[i]-lipgloss.Width(value))))
		}
		return result.String()
	}
	lines := []string{muted.Render(row(headers))}
	position := 0
	for i, index := range m.filtered {
		if index == m.cursor {
			position = i
		}
	}
	start := max(0, position-rows+1)
	for _, index := range m.filtered[start:min(len(m.filtered), start+rows)] {
		item := m.snapshot.Instances[index]
		marker := "  "
		if index == m.cursor {
			marker = "▸ "
		}
		values := []string{marker + item.Name, item.State, item.Activity.State(), fleetUptime(item)}
		if width >= 80 {
			values = []string{marker + item.Name, item.Agent, item.State, item.Activity.State(), fleetUptime(item)}
		}
		line := row(values)
		if index == m.cursor {
			line = selected.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *fleetModel) rebuildDetails() {
	l := m.layout()
	m.details.Width, m.details.Height = l.inspectorWidth, l.detailHeight
	if m.navCursor < m.navOffset {
		m.navOffset = m.navCursor
	} else if m.navCursor >= m.navOffset+l.navHeight {
		m.navOffset = m.navCursor - l.navHeight + 1
	}
	m.navOffset = max(0, min(m.navOffset, max(0, len(m.nodes)-l.navHeight)))
	item := m.currentInstance()
	if item == nil {
		m.details.SetContent("")
		return
	}
	var lines []string
	add := func(text string) { lines = append(lines, ansi.Hardwrap(text, l.inspectorWidth, true)) }
	image := item.Image
	if item.ImageName != "" {
		image = item.ImageName + " · " + item.Image
	}
	pairs := [][2]string{
		{"Instance", item.Name}, {"Profile", item.Profile}, {"Project", unknown(fleetProject(*item))}, {"Workspace", unknown(item.Project)},
		{"Agent", unknown(item.Agent)}, {"Image", unknown(image)}, {"Linux base", unknown(item.BaseImage)},
		{"CPU / RAM", fmt.Sprintf("%d vCPU / %s (limits)", item.CPUs, fleetMemory(item.MemoryMiB))},
		{"Version¹", unknown(item.AgentVersion) + " (image catalog; not guest-verified)"},
		{"Storage", fleetOptional(item.StorageGiB, " GiB")}, {"PID", fleetOptional(item.PID, "")},
		{"Created²", fleetCreated(item.CreatedAt)}, {"VM status", item.State}, {"Activity", item.Activity.State()},
		{"Runtime", fleetUptime(*item) + " (agent runtime, not VM or session)"}, {"Heartbeat", fleetHeartbeat(*item, m.lastSuccess)},
	}
	if item.Workspace.Checkout != "" {
		kind := "Main checkout"
		if item.Workspace.Checkout != item.Workspace.Repository {
			kind = "Worktree"
		}
		pairs = append(pairs, [2]string{kind, item.Workspace.Checkout}, [2]string{"Branch", unknown(item.Workspace.Branch)})
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

func (m fleetModel) inspector(l fleetLayout) []string {
	title := "Instances"
	if !m.navigatorFocus {
		title += " · FOCUS"
	}
	if item := m.currentInstance(); item != nil {
		title += " · " + item.Profile + " / " + filepath.Base(item.Project) + fmt.Sprintf(" · %d VM", len(m.filtered))
	}
	lines := []string{accent.Render(fleetText(title))}
	lines = append(lines, m.table(l.inspectorWidth, l.rows)...)
	detailTitle := "Machine"
	if item := m.currentInstance(); item != nil {
		detailTitle += " · " + item.Name
	}
	if m.details.TotalLineCount() > m.details.Height {
		detailTitle += fmt.Sprintf(" · %.0f%% (PgUp/PgDn)", 100*m.details.ScrollPercent())
	}
	lines = append(lines, accent.Render(fleetText(detailTitle)), m.details.View())
	return lines
}

func fleetBlock(lines []string, width, height int) []string {
	var result []string
	for _, line := range lines {
		result = append(result, strings.Split(line, "\n")...)
	}
	result = result[:min(len(result), height)]
	for len(result) < height {
		result = append(result, "")
	}
	for i, line := range result {
		line = ansi.Truncate(line, width, "…")
		result[i] = line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
	}
	return result
}

func (m fleetModel) View() string {
	if m.width < 44 || m.height < 14 {
		return ansi.Hardwrap("Terminal too small (minimum 46×14). Resize or q to quit.", m.width, true)
	}
	lines := m.heading()
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	lines = append(lines, "")
	l := m.layout()
	if len(m.snapshot.Instances) == 0 {
		message := "Discovering instances…"
		if m.errText != "" && !m.loaded {
			message = "Cannot discover instances. Watch will retry."
		} else if m.loaded {
			message = "No running agentnook instances in this scope.\nStart nook in a watched project; watch stays open."
		}
		lines = append(lines, fleetBlock([]string{ansi.Hardwrap(message, m.width, true)}, m.width, l.bodyHeight)...)
	} else if l.split {
		left := fleetBlock(m.navigator(l), l.navWidth, l.bodyHeight)
		right := fleetBlock(m.inspector(l), l.inspectorWidth, l.bodyHeight)
		for i := range left {
			lines = append(lines, left[i]+muted.Render(" │ ")+right[i])
		}
	} else {
		body := fleetBlock(m.navigator(l), m.width, 1+l.navHeight)
		body = append(body, m.inspector(l)...)
		lines = append(lines, fleetBlock(body, m.width, l.bodyHeight)...)
	}
	lines = append(lines, "", muted.Render(ansi.Truncate("¹ Image metadata · ² Record age, not uptime · CPU/RAM limits", m.width, "…")),
		muted.Render(ansi.Truncate("Tab focus · ↑↓/jk VMs only · PgUp/PgDn details · q quit watch only", m.width, "…")))
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
