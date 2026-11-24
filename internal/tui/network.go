package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/fleet"
)

type NetworkLoader func(...egress.HistoryQuery) (egress.Survey, error)
type NetworkDecider func(egress.Entry, string) error

func WatchNetwork(root string, initial egress.HistoryQuery, load NetworkLoader, decide NetworkDecider, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newNetworkModel(root, load, decide)
	m.machine, m.before, m.showAll = initial.Machine, initial.Before, initial.Machine != ""
	// Keep runtime discovery off the approval polling path.
	m.checkRuntime = func() tea.Msg {
		running, err := fleet.RunningMachineNames(ctx)
		return networkRuntimeLoaded{running: running, err: err}
	}
	_, err := tea.NewProgram(m, tea.WithInput(in), tea.WithOutput(out), tea.WithAltScreen()).Run()
	return err
}

type networkTick struct{}
type networkRuntimeTick struct{}
type networkRuntimeLoaded struct {
	running map[string]bool
	err     error
}
type networkLoaded struct {
	survey egress.Survey
	err    error
}
type networkDecided struct {
	choice string
	err    error
}

type networkModel struct {
	root          string
	load          NetworkLoader
	decide        NetworkDecider
	survey        egress.Survey
	selection     string // Pending identity, never a history cursor.
	status        string
	busy          bool
	width, height int
	historyMode   bool
	checkRuntime  tea.Cmd
	running       map[string]bool // nil until runtime status is available.
	runtimeErr    error
	showAll       bool
	machine       string
	before        int64
	newerPages    []int64
	pageLoading   bool
}

func newNetworkModel(root string, load NetworkLoader, decide NetworkDecider) networkModel {
	return networkModel{root: root, load: load, decide: decide, width: 90, height: 26, historyMode: true}
}

func (m networkModel) pageLimit() int { return max(1, min(200, m.height-21)) }
func (m networkModel) poll() tea.Cmd {
	query := egress.HistoryQuery{Machine: m.machine, Before: m.before, Limit: m.pageLimit()}
	return func() tea.Msg {
		survey, err := m.load(query)
		return networkLoaded{survey: survey, err: err}
	}
}
func (m networkModel) Init() tea.Cmd { return tea.Batch(m.poll(), m.checkRuntime) }

func (m networkModel) selectedIndex() int {
	for i, entry := range m.survey.Requests {
		if entry.Key() == m.selection {
			return i
		}
	}
	return -1
}

func (m networkModel) visibleMachines() []egress.MachineHistory {
	if m.showAll {
		return m.survey.History.Machines
	}
	var machines []egress.MachineHistory
	for _, machine := range m.survey.History.Machines {
		if m.running[machine.Machine] {
			machines = append(machines, machine)
		}
	}
	return machines
}

func (m networkModel) machineIndex(machines []egress.MachineHistory) int {
	for i, machine := range machines {
		if machine.Machine == m.machine {
			return i
		}
	}
	return -1
}

func (m *networkModel) selectMachine(name string) {
	if m.machine != name {
		m.machine = name
		m.before, m.newerPages, m.pageLoading = 0, nil, true
	}
}

func (m *networkModel) syncMachineSelection() {
	machines := m.visibleMachines()
	if m.machineIndex(machines) >= 0 {
		return
	}
	if len(machines) == 0 {
		m.selectMachine("")
		return
	}
	m.selectMachine(machines[0].Machine)
}

func (m networkModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(20, msg.Width), max(10, msg.Height)
	case networkTick:
		return m, m.poll()
	case networkRuntimeTick:
		return m, m.checkRuntime
	case networkRuntimeLoaded:
		m.running, m.runtimeErr = msg.running, msg.err
		// A failed lookup is not evidence that VMs ended. Keep the selection
		// for recovery, but show no unconfirmed rows in the running-only view.
		if msg.err == nil && !m.showAll {
			m.syncMachineSelection()
			m.finishPageLoad()
		}
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return networkRuntimeTick{} })
	case networkLoaded:
		m.survey = msg.survey
		if msg.err != nil {
			m.status = msg.err.Error()
		} else if msg.survey.Warning != "" {
			m.status = msg.survey.Warning
		}
		if m.selectedIndex() < 0 {
			m.selection = "" // Never silently retarget an approval after expiry.
		}
		if msg.err == nil && (m.showAll || m.running != nil) {
			m.syncMachineSelection()
		}
		m.finishPageLoad()
		return m, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return networkTick{} })
	case networkDecided:
		m.busy = false
		if msg.err != nil {
			m.status = msg.err.Error()
		} else {
			m.status = "Decision sent: " + msg.choice
		}
	case tea.KeyMsg:
		key := msg.String()
		if key == "q" || key == "esc" || key == "ctrl+c" {
			return m, tea.Quit
		}
		if key == "tab" || key == "shift+tab" {
			m.historyMode = !m.historyMode
			m.selection = "" // Switching tabs never arms a request.
			return m, nil
		}
		if m.historyMode {
			m = m.historyKey(key)
			return m, nil // Archived rows can NEVER receive approvals.
		}
		if m.busy || m.width < 48 || m.height < 22 {
			return m, nil
		}
		i := m.selectedIndex()
		switch key {
		case "up", "k", "down", "j":
			if len(m.survey.Requests) == 0 {
				break
			}
			if i < 0 {
				i = 0
			} else if key == "up" || key == "k" {
				i = max(0, i-1)
			} else {
				i = min(len(m.survey.Requests)-1, i+1)
			}
			m.selection = m.survey.Requests[i].Key()
		case "a", "s", "d":
			if i < 0 {
				break
			}
			entry := m.survey.Requests[i]
			choice := map[string]string{"a": "once", "s": "session", "d": "deny"}[key]
			m.selection, m.busy = "", true
			return m, func() tea.Msg { return networkDecided{choice: choice, err: m.decide(entry, choice)} }
		}
	}
	return m, nil
}

func (m *networkModel) finishPageLoad() {
	page := m.survey.History.Page
	if page.Machine == m.machine && page.Before == m.before {
		m.pageLoading = false
	}
}

func (m networkModel) historyKey(key string) networkModel {
	if key == "f" {
		m.showAll = !m.showAll
		if m.showAll || m.running != nil {
			m.syncMachineSelection()
			m.finishPageLoad()
		}
		return m
	}
	machines := m.visibleMachines()
	if len(machines) == 0 {
		return m
	}
	switch key {
	case "up", "k", "down", "j":
		i := m.machineIndex(machines)
		if i < 0 {
			i = 0
		} else if key == "up" || key == "k" {
			i = max(0, i-1)
		} else {
			i = min(len(machines)-1, i+1)
		}
		m.selectMachine(machines[i].Machine)
		m.finishPageLoad()
	case "pgup":
		page := m.survey.History.Page
		if !m.pageLoading && page.Machine == m.machine && page.HasOlder {
			m.newerPages = append(m.newerPages, m.before)
			m.before, m.pageLoading = page.NextBefore, true
		}
	case "pgdown":
		if !m.pageLoading && len(m.newerPages) > 0 {
			last := len(m.newerPages) - 1
			m.before, m.pageLoading = m.newerPages[last], true
			m.newerPages = m.newerPages[:last]
		}
	case "home":
		m.before, m.newerPages, m.pageLoading = 0, nil, true
	}
	return m
}

func (m networkModel) View() string {
	if m.width < 48 || m.height < 22 {
		return ansi.Hardwrap("Network view needs at least 48 columns and 22 rows. Resize or q to quit; --json works at any size.", m.width, true)
	}
	lines := []string{accent.Render("agentnook · Network"), "AGENTNOOK_HOME: " + fleetText(m.root),
		fmt.Sprintf("%d live approval broker(s) · %d pending · %d VM histories", m.survey.Sessions, len(m.survey.Requests), len(m.survey.History.Machines))}
	if m.historyMode {
		lines = append(lines, "[History] · Tab → Approvals", "")
		lines = append(lines, m.historyView()...)
	} else {
		lines = append(lines, "[Approvals] · Tab → History · select explicitly with ↑↓/jk", "")
		lines = append(lines, m.approvalView()...)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], m.width, "…")
	}
	return strings.Join(lines, "\n")
}

func (m networkModel) historyView() []string {
	heading := "Running VMs · f show all"
	if m.showAll {
		heading = "All VMs · f show running only"
	} else if m.runtimeErr != nil {
		heading = "VM status unavailable · f show all"
	}
	status := fleetText(m.status)
	if status == "" && m.runtimeErr != nil {
		status = "VM status unavailable: " + fleetText(m.runtimeErr.Error())
	}
	lines := []string{heading}
	machines := m.visibleMachines()
	index := m.machineIndex(machines)
	start := max(0, index-4)
	for i := start; i < min(len(machines), start+5); i++ {
		meta := machines[i]
		prefix := "  "
		if i == index {
			prefix = "▸ "
		}
		line := fleetText(fmt.Sprintf("%s · %s/%s · %d events", meta.Machine, meta.Info.Profile, meta.Info.Agent, meta.Count))
		if m.running[meta.Machine] {
			line = selected.Render("LIVE " + line)
		}
		lines = append(lines, prefix+line)
	}
	if index < 0 {
		message := "Select a VM with ↑↓/jk."
		switch {
		case !m.showAll && m.runtimeErr != nil:
			message = "Press f to browse stored history."
		case !m.showAll && m.running == nil:
			message = "Checking VM status…"
		case len(m.survey.History.Machines) == 0:
			message = "No VM history yet. History starts with new nook launches."
		case len(machines) == 0:
			message = "No running VMs with history."
		}
		return append(lines, message, "",
			"full: denials only; allowed traffic is NOT captured.", status, "Tab approvals · q close view")
	}
	meta := machines[index]
	coverage := "ask: engine decisions (TCP/DNS/UDP/ICMP when reported)"
	if meta.Coverage == "denials-only" {
		coverage = "full: DENIALS ONLY — allowed traffic is NOT captured"
	}
	end := "not finalized (running or abruptly interrupted)"
	if !meta.Ended.IsZero() {
		end = meta.Ended.Local().Format(time.RFC3339)
	}
	lines = append(lines, "", "Project: "+fleetText(meta.Info.Project), coverage,
		"End: "+end, "Audit warning: "+fleetText(meta.Warning),
		"Time                 Decision Protocol Target · policy reason")
	page := m.survey.History.Page
	if page.Machine != m.machine || page.Before != m.before {
		lines = append(lines, "Loading machine history…")
	} else {
		for _, event := range page.Events {
			lines = append(lines, fmt.Sprintf("%s %-7s %-4s %s · %s", event.Time.Local().Format("2006-01-02 15:04:05"),
				strings.ToUpper(fleetText(event.Decision)), strings.ToUpper(fleetText(event.Protocol)), fleetText(event.Target), fleetText(event.Reason)))
		}
		if len(page.Events) == 0 {
			lines = append(lines, "No recorded events (not proof of no traffic).")
		}
	}
	return append(lines, "", status, "↑↓/jk VM · PgUp older · PgDn newer · Home latest · Tab approvals · q quit",
		"ALLOWED = policy permitted, NOT connection or HTTP success.")
}

func (m networkModel) approvalView() []string {
	var lines []string
	index := m.selectedIndex()
	count := max(1, m.height-17)
	start := max(0, index-count+1)
	for i := start; i < min(len(m.survey.Requests), start+count); i++ {
		e := m.survey.Requests[i]
		seconds := max(0, int(time.Until(e.Request.Deadline).Seconds()))
		line := fmt.Sprintf("  %s · %s/%s · %ds", e.Request.Target, e.Info.Profile, e.Info.Agent, seconds)
		if i == index {
			line = "▸ " + strings.TrimSpace(line)
		}
		lines = append(lines, fleetText(line))
	}
	if len(m.survey.Requests) == 0 {
		lines = append(lines, "No pending connections. Tab shows persistent machine history.")
	}
	if index >= 0 {
		e := m.survey.Requests[index]
		lines = append(lines, "", "Project: "+fleetText(e.Info.Project), "Session: "+fleetText(e.Info.Session),
			"VM launch: "+fleetText(e.Request.Machine), "TCP destination: "+fleetText(e.Request.Target))
		if e.Request.Hint != "" {
			lines = append(lines, "TLS SNI (unverified, guest-supplied): "+fleetText(e.Request.Hint))
		}
		lines = append(lines, "Session approval covers ONLY this VM launch + exact IP/port.")
	}
	return append(lines, "", fleetText(m.status), "a allow once · s allow IP/port for VM session · d deny · Tab history · q quit",
		"12s approval window. Timeout denies; retry connection. DNS is not mediated.",
		"Approves TCP connections, NOT individual HTTP requests or their contents.")
}
