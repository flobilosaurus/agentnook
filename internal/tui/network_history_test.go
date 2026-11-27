package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func demoTimeline() egress.Timeline {
	work := egress.MachineHistory{Machine: "nook-run-work", Info: egress.Info{Profile: "work", Project: "/code/agentnook", Agent: "pi"}, Mode: "ask"}
	private := egress.MachineHistory{Machine: "nook-run-docs", Info: egress.Info{Profile: "private", Project: "/code/docs", Agent: "claude"}, Mode: "full", Coverage: "denials-only", Ended: time.Now()}
	return egress.Timeline{Machines: []egress.MachineHistory{work, private}, Records: []egress.HistoryRecord{
		{Machine: work, Event: egress.HistoryEvent{Sequence: 4, Target: "github.com", Decision: "allowed", Protocol: "dns", Reason: "local_policy"}},
		{Machine: private, Event: egress.HistoryEvent{Sequence: 1, Target: "10.0.0.5:443", Decision: "denied", Protocol: "tcp", Reason: "private_target"}},
		{Machine: work, Event: egress.HistoryEvent{Sequence: 3, Target: "203.0.113.5:443", Decision: "denied", Protocol: "tcp", Reason: "broker_decision"}},
	}, Next: map[string]int64{work.Machine: 120}}
}

func setTimeline(m networkModel, timeline egress.Timeline) networkModel {
	m, _ = networkUpdate(m, networkHistoryLoaded{generation: m.generation, timeline: timeline})
	return m
}

func TestNetworkOverviewGroupsPendingAndEventsWithOneSelection(t *testing.T) {
	m := newNetworkModel(false, egress.HistoryQuery{}, nil)
	m.width, m.height = 100, 45
	m = networkLive(m, networkEntry("pending", "203.0.113.30:80"))
	m = setTimeline(m, demoTimeline())
	view := ansi.Strip(m.View())
	for _, value := range []string{"Pending requests", "Recent decisions", "Profile private", "Profile work", "docs · VM docs · claude · ended", "github.com · DNS", "203.0.113.30:80"} {
		if !strings.Contains(view, value) {
			t.Fatal(value, view)
		}
	}
	m, _ = networkKey(m, "j")
	if m.selected.request == "" {
		t.Fatal("first navigation did not select pending request")
	}
	m, _ = networkKey(m, "j")
	if m.selected.machine != "nook-run-docs" || m.selected.request != "" || !strings.Contains(m.View(), "only denials are recorded") {
		t.Fatal(m.View())
	}
	for _, key := range []string{"a", "s", "d"} {
		if _, cmd := networkKey(m, key); cmd != nil {
			t.Fatal("approved an archived event", key)
		}
	}
	m, _ = networkKey(m, "j")
	if !strings.Contains(m.View(), "connection may still fail") {
		t.Fatal(m.View())
	}
	m = setTimeline(m, demoTimeline())
	if m.selected.seq != 4 {
		t.Fatal("refresh lost history selection")
	}
}

func TestNetworkHistoryFiltersPagesAndRejectsLateResults(t *testing.T) {
	m := newNetworkModel(true, egress.HistoryQuery{}, nil)
	m.loadHistory = func(q egress.TimelineQuery) (egress.Timeline, error) { return demoTimeline(), nil }
	if m.historyQuery().Running != nil || m.historyQuery().Limit != 5 {
		t.Fatal("wrong default audit query")
	}
	m, _ = networkUpdate(m, m.historyPoll()())
	if view := ansi.Strip(m.View()); !strings.Contains(view, "All VMs") || !strings.Contains(view, "ended") || strings.Contains(view, "203.0.113.30:80") {
		t.Fatal(view)
	}
	m, cmd := networkKey(m, "pgup")
	if cmd == nil || !reflect.DeepEqual(m.historyQuery().Cursor, demoTimeline().Next) || len(m.newer) != 1 {
		t.Fatal("older page has no cursor")
	}
	m, _ = networkUpdate(m, networkHistoryLoaded{generation: m.generation - 1, timeline: demoTimeline()})
	if m.historyReady || len(m.timeline.Records) > 0 {
		t.Fatal("late result installed on older page")
	}
	m, _ = networkUpdate(m, cmd())
	m, cmd = networkKey(m, "pgdown")
	if cmd == nil || m.historyQuery().Cursor != nil || len(m.newer) != 0 {
		t.Fatal("newer page did not restore latest")
	}
	m, _ = networkUpdate(m, networkRuntimeLoaded{running: map[string]bool{"nook-run-work": true}})
	m, cmd = networkKey(m, "f")
	if cmd == nil || !m.historyQuery().Running["nook-run-work"] || m.historyQuery().Running["nook-run-docs"] {
		t.Fatal("running filter not passed to reader")
	}
	m, _ = networkUpdate(m, networkRuntimeLoaded{err: errors.New("runtime unavailable")})
	if running := m.historyQuery().Running; running == nil || len(running) != 0 {
		t.Fatal("runtime outage assumed VMs are running")
	}
	if !strings.Contains(m.View(), "Live VM status unavailable") {
		t.Fatal(m.View())
	}
}

func TestNetworkHistoryShowsWarningsWithoutEvents(t *testing.T) {
	vm := egress.MachineHistory{Machine: "nook-run-empty", Mode: "full", Coverage: "denials-only", Warning: "incomplete capture"}
	m := newNetworkModel(true, egress.HistoryQuery{Machine: vm.Machine}, nil)
	m = setTimeline(m, egress.Timeline{Machines: []egress.MachineHistory{vm}})
	for _, value := range []string{"No recorded events", "only denials are recorded", "incomplete capture"} {
		if !strings.Contains(m.View(), value) {
			t.Fatal(value, m.View())
		}
	}
}

func TestNetworkScopedHistoryAndMinSize(t *testing.T) {
	m := newNetworkModel(true, egress.HistoryQuery{Machine: "nook-run-work", Before: 300}, nil)
	m = setTimeline(m, demoTimeline())
	if q := m.historyQuery(); !m.archive || q.Machine != "nook-run-work" || q.Before != 300 {
		t.Fatal(q)
	}
	for _, key := range []string{"a", "s", "d", "f"} {
		m, _ = networkKey(m, key)
		if !m.archive || m.historyQuery().Running != nil || m.historyQuery().Before != 300 {
			t.Fatal("scoped mode changed", key)
		}
	}
	m, _ = networkUpdate(m, tea.WindowSizeMsg{Width: 48, Height: 22})
	assertTerminalBounds(t, m.View(), 48, 22)
	m, _ = networkKey(m, "home")
	if m.historyQuery().Before != 0 {
		t.Fatal("home did not reset legacy byte cursor")
	}
}
