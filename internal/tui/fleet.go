package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/fleet"
)

type FleetLoader func(context.Context) (fleet.Snapshot, error)

type fleetRefreshMsg struct{}
type fleetResultMsg struct {
	snapshot fleet.Snapshot
	err      error
	at       time.Time
}

type fleetModel struct {
	ctx           context.Context
	load          FleetLoader
	snapshot      fleet.Snapshot
	cursor        int
	width, height int
	details       viewport.Model
	lastSuccess   time.Time
	errText       string
	loaded        bool
}

func newFleetModel(ctx context.Context, load FleetLoader) fleetModel {
	return fleetModel{ctx: ctx, load: load, cursor: -1, width: 88, height: 32, details: viewport.New(88, 12)}
}

func (m fleetModel) fetch() tea.Msg {
	snapshot, err := m.load(m.ctx)
	return fleetResultMsg{snapshot: snapshot, err: err, at: time.Now()}
}

func (m fleetModel) Init() tea.Cmd { return m.fetch }

func (m fleetModel) instance() *fleet.Instance {
	if m.cursor < 0 || m.cursor >= len(m.snapshot.Instances) {
		return nil
	}
	return &m.snapshot.Instances[m.cursor]
}

func watchProject(vm fleet.Instance) string {
	if vm.Workspace.Repository != "" {
		return vm.Workspace.Repository
	}
	return vm.Project
}

func (m *fleetModel) replaceSnapshot(snapshot fleet.Snapshot) {
	previous := m.instance()
	m.snapshot = snapshot
	m.snapshot.Instances = append([]fleet.Instance(nil), snapshot.Instances...)
	sort.SliceStable(m.snapshot.Instances, func(i, j int) bool {
		return m.snapshot.Instances[i].Profile < m.snapshot.Instances[j].Profile
	})
	m.cursor = -1
	best := 5
	for i, vm := range m.snapshot.Instances {
		rank := 4
		if previous != nil {
			switch {
			case vm.Name == previous.Name:
				rank = 0
			case vm.Profile == previous.Profile && vm.Project == previous.Project:
				rank = 1
			case vm.Profile == previous.Profile && watchProject(vm) == watchProject(*previous):
				rank = 2
			case vm.Profile == previous.Profile:
				rank = 3
			}
		}
		if rank < best {
			m.cursor, best = i, rank
		}
	}
	if best != 0 {
		m.details.GotoTop()
	}
}

func (m fleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case fleetRefreshMsg:
		return m, m.fetch
	case fleetResultMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
		} else {
			m.replaceSnapshot(msg.snapshot)
			m.errText, m.lastSuccess, m.loaded = "", msg.at, true
		}
		cmd = tea.Tick(time.Second, func(time.Time) tea.Msg { return fleetRefreshMsg{} })
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k", "down", "j":
			delta := 1
			if msg.String() == "up" || msg.String() == "k" {
				delta = -1
			}
			if len(m.snapshot.Instances) != 0 {
				cursor := min(len(m.snapshot.Instances)-1, max(0, m.cursor+delta))
				if cursor != m.cursor {
					m.cursor = cursor
					m.details.GotoTop()
				}
			}
		case "pgup", "ctrl+u":
			m.details.HalfViewUp()
		case "pgdown", "ctrl+d":
			m.details.HalfViewDown()
		case "home":
			m.details.GotoTop()
		case "end":
			m.details.GotoBottom()
		}
	case tea.MouseMsg:
		m.details, cmd = m.details.Update(msg)
	}
	m.refreshDetails()
	return m, cmd
}

func (m fleetModel) activity(vm *fleet.Instance) (state, uptime string) {
	if m.errText != "" || !vm.Activity.Fresh {
		return "unknown", "unknown"
	}
	return vm.Activity.State(), (time.Duration(vm.Activity.Report.UptimeSeconds) * time.Second).String()
}

func (m fleetModel) heading() []string {
	var lines []string
	switch {
	case m.errText != "" && m.loaded:
		lines = append(lines, monitorWarning.Render("STALE · last update "+m.lastSuccess.Local().Format("15:04:05")+" · "+monitorText(m.errText)))
	case m.errText != "":
		lines = append(lines, monitorWarning.Render("Discovery unavailable · "+monitorText(m.errText)))
	case m.loaded:
		projects := make(map[string]struct{})
		unknown := 0
		for i := range m.snapshot.Instances {
			vm := &m.snapshot.Instances[i]
			projects[watchProject(*vm)] = struct{}{}
			if state, _ := m.activity(vm); state == "unknown" {
				unknown++
			}
		}
		line := fmt.Sprintf("%d VMs · %d projects", len(m.snapshot.Instances), len(projects))
		if unknown > 0 {
			line += " · " + monitorWarning.Render(fmt.Sprintf("%d with stale or missing telemetry", unknown))
		}
		lines = append(lines, line)
	default:
		lines = append(lines, "Discovering VMs…")
	}
	if m.snapshot.Warning != "" {
		lines = append(lines, monitorMuted.Render(monitorText(m.snapshot.Warning)))
	}
	return lines
}

func (m fleetModel) list(width, rowsAvailable int) []string {
	rows := make([]monitorRow, 0, len(m.snapshot.Instances))
	for i := range m.snapshot.Instances {
		vm := &m.snapshot.Instances[i]
		state, _ := m.activity(vm)
		status := state
		if vm.State != "running" {
			status = monitorKnown(vm.State)
		}
		labelWidth := width - 6 - min(ansi.StringWidth(status), width/3)
		rows = append(rows, monitorRow{profile: vm.Profile, label: monitorMachine(watchProject(*vm), vm.Name, vm.Agent, labelWidth),
			secondary: fmt.Sprintf("Network: %s · %d mounts", monitorKnown(vm.Network), len(vm.Mounts)),
			status:    status, warning: status != state || state == "unknown" || state == "blocked"})
	}
	return monitorList(rows, m.cursor, width, rowsAvailable)
}

func (m fleetModel) listBudget(body, left int) int {
	if left < m.width {
		return max(3, body)
	}
	return max(3, body/2)
}

func (m *fleetModel) refreshDetails() {
	vm := m.instance()
	left, right := watchColumns(m.width)
	body := m.height - len(m.heading()) - 2 // One heading per pane and a shared footer.
	m.details.Width = right
	m.details.Height = max(1, body)
	if left == m.width {
		m.details.Height = max(1, body-1-len(m.list(left, m.listBudget(body, left))))
	}
	if vm == nil {
		m.details.SetContent("")
		return
	}
	state, uptime := m.activity(vm)
	var lines []string
	add := func(label, value string) {
		lines = append(lines, ansi.Hardwrap(monitorMuted.Render(label)+"  "+value, m.details.Width, true))
	}
	add("Agent", monitorKnown(vm.Agent)+" / "+monitorKnown(vm.Profile))
	add("VM / activity", monitorKnown(vm.State)+" / "+state)
	add("Agent uptime", uptime)
	add("Launch limits", fmt.Sprintf("%d vCPU · %d MiB RAM", vm.CPUs, vm.MemoryMiB))
	add("Network mode", monitorKnown(vm.Network))
	env := "none"
	if vm.EnvNames == nil {
		env = "unknown"
	} else if len(vm.EnvNames) > 0 {
		env = monitorText(strings.Join(vm.EnvNames, ", "))
	}
	add("Forwarded environment", env)
	add("Mounts", fmt.Sprintf("%d", len(vm.Mounts)))
	for _, mount := range vm.Mounts {
		mode := "rw"
		if mount.ReadOnly {
			mode = "ro"
		}
		add("", fmt.Sprintf("%s → %s [%s]", monitorText(mount.Host), monitorText(mount.Guest), mode))
	}
	if state == "unknown" {
		lines = append(lines, ansi.Hardwrap("No fresh agent telemetry. VM state is reported separately.", m.details.Width, true))
	}
	add("VM ID", monitorKnown(vm.Name))
	add("Project", monitorKnown(watchProject(*vm)))
	if vm.Project != watchProject(*vm) {
		add("Workspace", monitorKnown(vm.Project))
	}
	if vm.Workspace.Branch != "" {
		add("Branch", monitorText(vm.Workspace.Branch))
	}
	image := monitorKnown(vm.Image)
	if vm.ImageName != "" {
		image = monitorText(vm.ImageName) + " · " + image
	}
	add("Image", image)
	add("Linux base", monitorKnown(vm.BaseImage))
	add("Agent version (image metadata)", monitorKnown(vm.AgentVersion))
	if vm.StorageGiB != nil {
		add("Storage", fmt.Sprintf("%d GiB", *vm.StorageGiB))
	}
	if vm.PID != nil {
		add("PID", fmt.Sprint(*vm.PID))
	}
	if !vm.CreatedAt.IsZero() {
		add("VM record created", vm.CreatedAt.Local().Format(time.RFC3339))
	}
	if !vm.Activity.HeartbeatAt.IsZero() {
		age := max(0, m.lastSuccess.Sub(vm.Activity.HeartbeatAt)).Truncate(time.Second)
		add("Last heartbeat at discovery", age.String()+" ago (host-observed)")
	}
	m.details.SetContent(strings.Join(lines, "\n"))
}

func (m fleetModel) View() string {
	if m.width < 46 || m.height < 14 {
		message := ansi.Hardwrap("Watch needs at least 46×14. Resize or q to quit.", m.width, true)
		lines := strings.Split(message, "\n")
		return strings.Join(lines[:min(len(lines), m.height)], "\n")
	}
	lines := m.heading()
	if vm := m.instance(); vm != nil {
		left, right := watchColumns(m.width)
		body := m.height - len(lines) - 2
		list := append([]string{monitorMuted.Render("Instances")}, m.list(left, m.listBudget(body, left))...)
		caption := monitorMachine(watchProject(*vm), vm.Name, "", right)
		details := append([]string{monitorTitle.Render(caption)}, strings.Split(m.details.View(), "\n")...)
		if left < m.width {
			lines = append(lines, monitorColumns(list, details, left, right, body+1)...)
		} else {
			lines = append(lines, list...)
			lines = append(lines, details...)
		}
	} else if m.loaded {
		lines = append(lines, "No running agentnook instances in this scope.")
	}
	lines = append(lines, monitorMuted.Render("↑↓/jk select · Enter VM audit · PgUp/PgDn details"))
	return monitorView(lines, m.width)
}
