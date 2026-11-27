package tui

import (
	"fmt"
	"maps"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

// NetworkLoader is the CLI's injectable survey reader. The interactive view
// polls approvals and stored history independently to keep decisions responsive.
type NetworkLoader func(...egress.HistoryQuery) (egress.Survey, error)
type NetworkDecider func(egress.Entry, string) error

type networkLiveTick struct{}
type networkHistoryTick struct {
	archive    bool
	generation uint64
}
type networkRuntimeTick struct{}
type networkRuntimeLoaded struct {
	running map[string]bool
	err     error
}
type networkLiveLoaded struct {
	survey egress.Survey
	err    error
}
type networkHistoryLoaded struct {
	archive    bool
	generation uint64
	timeline   egress.Timeline
	err        error
}
type networkDecided struct {
	choice string
	err    error
}

type networkKeyID struct {
	request string
	machine string
	seq     uint64
}

type networkLeaf struct {
	id      networkKeyID
	row     monitorRow
	request *egress.Entry
	event   *egress.HistoryRecord
}

type timelinePosition struct {
	before int64
	cursor map[string]int64
}

type networkModel struct {
	loadLive      func() (egress.Survey, error)
	loadHistory   func(egress.TimelineQuery) (egress.Timeline, error)
	checkRuntime  tea.Cmd
	decide        NetworkDecider
	width, height int

	requests     []egress.Entry
	timeline     egress.Timeline
	selected     networkKeyID
	busy         bool
	decision     string
	liveProblem  string
	historyErr   error
	runtimeErr   error
	running      map[string]bool
	liveLoaded   bool
	historyReady bool

	archive     bool
	machine     string // --machine opens a scoped, read-only history.
	runningOnly bool
	position    timelinePosition
	newer       []timelinePosition
	generation  uint64 // Discard async pages from before a mode/filter/page change.
}

func newNetworkModel(archive bool, initial egress.HistoryQuery, decide NetworkDecider) networkModel {
	return networkModel{width: 90, height: 26, decide: decide,
		archive: archive, machine: initial.Machine, position: timelinePosition{before: initial.Before}}
}

func (m networkModel) livePoll() tea.Cmd {
	if m.machine != "" || m.loadLive == nil {
		return nil
	}
	return func() tea.Msg {
		survey, err := m.loadLive()
		return networkLiveLoaded{survey, err}
	}
}

const networkRecentLimit, networkHistoryLimit = 3, 5

func (m networkModel) historyQuery() egress.TimelineQuery {
	q := egress.TimelineQuery{Limit: networkRecentLimit}
	if m.archive {
		q.Limit, q.Machine, q.Before, q.Cursor = networkHistoryLimit, m.machine, m.position.before, m.position.cursor
		if m.runningOnly {
			q.Running = m.running
			if q.Running == nil {
				q.Running = map[string]bool{} // Unconfirmed is not running.
			}
		}
	}
	return q
}

func (m networkModel) historyPoll() tea.Cmd {
	if m.loadHistory == nil {
		return nil
	}
	q, generation, archive := m.historyQuery(), m.generation, m.archive
	return func() tea.Msg {
		timeline, err := m.loadHistory(q)
		return networkHistoryLoaded{archive, generation, timeline, err}
	}
}

func (m networkModel) Init() tea.Cmd {
	return tea.Batch(m.livePoll(), m.historyPoll(), m.checkRuntime)
}

func (m *networkModel) reloadHistory() tea.Cmd {
	m.generation++
	m.historyReady, m.historyErr = false, nil
	m.selected = networkKeyID{}
	m.timeline.Records, m.timeline.Next = nil, nil
	return m.historyPoll()
}

func (m networkModel) leaves() (pending, history []networkLeaf) {
	if !m.archive {
		pending = m.approvalLeaves(time.Now())
	}
	return pending, m.historyLeaves()
}

func (m networkModel) current(leaves []networkLeaf) int {
	for i, entry := range leaves {
		if entry.id == m.selected {
			return i
		}
	}
	return -1
}

func (m *networkModel) checkSelection() {
	if m.selected == (networkKeyID{}) {
		return
	}
	pending, history := m.leaves()
	if m.current(pending) < 0 && m.current(history) < 0 {
		m.selected = networkKeyID{}
	}
}

func (m networkModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case networkLiveTick:
		return m, m.livePoll()
	case networkHistoryTick:
		if msg.generation == m.generation {
			return m, m.historyPoll()
		}
	case networkRuntimeTick:
		return m, m.checkRuntime
	case networkRuntimeLoaded:
		if msg.err != nil {
			msg.running = nil
		}
		changed := !maps.Equal(m.running, msg.running)
		m.running, m.runtimeErr = msg.running, msg.err
		tick := tea.Tick(time.Second, func(time.Time) tea.Msg { return networkRuntimeTick{} })
		if m.archive && m.runningOnly && changed {
			m.position, m.newer = timelinePosition{}, nil
			cmd := m.reloadHistory()
			return m, tea.Batch(cmd, tick)
		}
		return m, tick
	case networkLiveLoaded:
		m.liveLoaded = true
		m.requests = approvalOrder(msg.survey.Requests)
		m.liveProblem = msg.survey.Warning
		if msg.err != nil {
			m.requests, m.liveProblem = nil, msg.err.Error()
		}
		m.checkSelection()
		return m, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return networkLiveTick{} })
	case networkHistoryLoaded:
		if msg.generation != m.generation {
			break
		}
		m.historyErr, m.historyReady = msg.err, true
		if msg.err == nil {
			m.timeline = msg.timeline
		}
		m.checkSelection()
		if m.archive && m.selected == (networkKeyID{}) {
			if history := m.historyLeaves(); len(history) > 0 {
				m.selected = history[0].id
			}
		}
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return networkHistoryTick{msg.archive, msg.generation} })
	case networkDecided:
		m.busy = false
		if msg.err != nil {
			m.decision = msg.err.Error()
		} else {
			m.decision = "Decision sent: " + msg.choice
		}
	case tea.KeyMsg:
		return m.key(msg.String())
	}
	return m, nil
}

const networkMinWidth, networkMinHeight = 48, 22

func (m networkModel) key(key string) (tea.Model, tea.Cmd) {
	if key == "q" || key == "esc" || key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.width < networkMinWidth || m.height < networkMinHeight {
		return m, nil
	}
	if m.archive {
		switch {
		case key == "f" && m.machine == "":
			m.runningOnly = !m.runningOnly
			m.position, m.newer = timelinePosition{}, nil
		case key == "pgup" && m.historyReady && m.historyErr == nil && m.timeline.Next != nil:
			m.newer = append(m.newer, m.position)
			m.position = timelinePosition{cursor: m.timeline.Next}
		case key == "pgdown" && m.historyReady && len(m.newer) > 0:
			m.position = m.newer[len(m.newer)-1]
			m.newer = m.newer[:len(m.newer)-1]
		case key == "home":
			m.position, m.newer = timelinePosition{}, nil
		default:
			return m.selectKey(key), nil // History never reaches the decider.
		}
		cmd := m.reloadHistory()
		return m, cmd
	}
	switch key {
	case "a", "s", "d":
		if m.busy || m.selected.request == "" || m.decide == nil {
			return m, nil
		}
		for _, entry := range m.requests {
			if entry.Key() != m.selected.request {
				continue
			}
			if !time.Now().Before(entry.Request.Deadline) {
				m.selected, m.decision = networkKeyID{}, "Request expired; retry the connection."
				return m, nil
			}
			target := entry.Request.Target
			_, detailWidth := watchColumns(m.width)
			if target == "" || monitorText(target) != target || ansi.StringWidth(target) > detailWidth {
				return m, nil // An exact, printable destination must fit on screen.
			}
			choice := map[string]string{"a": "once", "s": "session", "d": "deny"}[key]
			m.selected, m.busy, m.decision = networkKeyID{}, true, "Sending decision…"
			return m, func() tea.Msg { return networkDecided{choice, m.decide(entry, choice)} }
		}
		return m, nil
	default:
		return m.selectKey(key), nil
	}
}

func (m networkModel) selectKey(key string) networkModel {
	if key != "up" && key != "k" && key != "down" && key != "j" {
		return m
	}
	pending, history := m.leaves()
	leaves := append(pending, history...)
	if len(leaves) == 0 {
		return m
	}
	index := m.current(leaves)
	if index < 0 {
		index = 0
	} else if key == "up" || key == "k" {
		index = max(0, index-1)
	} else {
		index = min(len(leaves)-1, index+1)
	}
	m.selected, m.decision = leaves[index].id, ""
	return m
}

func (m networkModel) View() string {
	if m.width < networkMinWidth || m.height < networkMinHeight {
		message := fmt.Sprintf("Approvals and Audit need a terminal of at least %d×%d. Resize or switch tabs.", networkMinWidth, networkMinHeight+watchChromeRows)
		lines := strings.Split(ansi.Hardwrap(message, m.width, true), "\n")
		return strings.Join(lines[:min(len(lines), m.height)], "\n")
	}
	left, right := watchColumns(m.width)
	listModel, detailModel := m, m
	listModel.width, detailModel.width = left, right
	pending, history := listModel.leaves()
	pi, hi := m.current(pending), m.current(history)
	lines := m.networkHeading()
	details := detailModel.networkDetails(pending, history, pi, hi)
	footer := m.networkFooter(pi >= 0)
	budget := m.height - len(lines) - len(footer)
	if left < m.width {
		list := listModel.viewList(pending, history, pi, hi, budget)
		var wrapped []string
		for _, line := range details {
			wrapped = append(wrapped, strings.Split(ansi.Wrap(line, right, "/"), "\n")...)
		}
		lines = append(lines, monitorColumns(list, wrapped, left, right, budget)...)
	} else {
		lines = append(lines, m.viewList(pending, history, pi, hi, budget-len(details))...)
		lines = append(lines, details...)
	}
	return monitorView(append(lines, footer...), m.width)
}

func (m networkModel) viewList(pending, history []networkLeaf, pi, hi, budget int) []string {
	var lines []string
	if m.archive {
		lines = append(lines, monitorMuted.Render("Recorded decisions"))
		lines = append(lines, m.eventList(history, hi, budget-1)...)
	} else {
		lines = append(lines, monitorMuted.Render("Pending requests"))
		available := budget - 2 // One heading per section.
		var requestLines []string
		if len(pending) > 0 {
			pendingBudget := available - 1
			if len(history) != 0 {
				pendingBudget = max(3, available-3)
				if hi >= 0 {
					pendingBudget = min(pendingBudget, max(3, available/2))
				}
			}
			requestLines = monitorList(leafRows(pending), pi, m.width, pendingBudget)
		} else if !m.liveLoaded {
			requestLines = []string{"Checking for pending requests…"}
		} else if m.liveProblem == "" {
			requestLines = []string{"No pending connections."}
		} else {
			requestLines = []string{"Pending requests unavailable."}
		}
		lines = append(lines, requestLines...)
		lines = append(lines, monitorMuted.Render("Recent decisions"))
		lines = append(lines, m.eventList(history, hi, available-len(requestLines))...)
	}
	return lines
}

func leafRows(leaves []networkLeaf) []monitorRow {
	rows := make([]monitorRow, len(leaves))
	for i, leaf := range leaves {
		rows[i] = leaf.row
	}
	return rows
}
