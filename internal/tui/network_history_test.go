package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func TestNetworkDefaultsToHistoryAndNeverApprovesArchivedRows(t *testing.T) {
	calls := 0
	m := newNetworkModel("/home", nil, func(egress.Entry, string) error { calls++; return nil })
	entry := networkEntry("pending", "203.0.113.1:443")
	m.survey.Requests, m.selection = []egress.Entry{entry}, entry.Key()
	for _, key := range []rune{'a', 's', 'd'} {
		var cmd tea.Cmd
		m, cmd = networkKey(m, key)
		if cmd != nil || !m.historyMode {
			t.Fatal("history acted as approval")
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(networkModel)
	if m.historyMode || m.selection != "" || calls != 0 {
		t.Fatal(m.selection, calls)
	}
	m, _ = networkKey(m, 'j')
	m, cmd := networkKey(m, 'a')
	if cmd == nil {
		t.Fatal("live approval unavailable")
	}
	_ = cmd()
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestNetworkCannotApproveWhenTerminalCannotShowTarget(t *testing.T) {
	m := newNetworkModel("/home", nil, func(egress.Entry, string) error { t.Fatal("hidden approval"); return nil })
	entry := networkEntry("pending", "203.0.113.1:443")
	m.historyMode, m.width = false, 20
	m.survey.Requests, m.selection = []egress.Entry{entry}, entry.Key()
	_, cmd := networkKey(m, 'a')
	if cmd != nil || !strings.Contains(m.View(), "48") {
		t.Fatal("undersized terminal armed an approval")
	}
}

func TestNetworkHistoryMachineSelectionAndStablePaging(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.machine = "nook-run-one"
	m.showAll = true
	m.survey.History = egress.HistoryView{Machines: []egress.MachineHistory{{Machine: "nook-run-one"}, {Machine: "nook-run-two"}},
		Page: egress.HistoryPage{Machine: m.machine, HasOlder: true, NextBefore: 100}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(networkModel)
	if m.before != 100 || len(m.newerPages) != 1 || !m.pageLoading {
		t.Fatal(m.before)
	}
	next, _ = m.Update(networkLoaded{survey: egress.Survey{History: egress.HistoryView{Machines: m.survey.History.Machines,
		Page: egress.HistoryPage{Machine: m.machine, Before: 100}}}})
	m = next.(networkModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(networkModel)
	if m.before != 0 || len(m.newerPages) != 0 {
		t.Fatal(m.before)
	}
	m, _ = networkKey(m, 'j')
	if m.machine != "nook-run-two" || m.before != 0 || len(m.newerPages) != 0 {
		t.Fatal(m.machine)
	}
}

func TestNetworkHistoryFiltersAndMarksRunningVMs(t *testing.T) {
	const live, archived, alsoLive = "nook-run-live", "nook-run-archived", "nook-run-also-live"
	machines := []egress.MachineHistory{{Machine: live}, {Machine: archived}, {Machine: alsoLive}}
	var query egress.HistoryQuery
	m := newNetworkModel("/home", func(q ...egress.HistoryQuery) (egress.Survey, error) {
		query = q[0]
		return egress.Survey{}, nil
	}, nil)
	next, _ := m.Update(networkLoaded{survey: egress.Survey{History: egress.HistoryView{
		Machines: machines,
		Page:     egress.HistoryPage{Machine: archived, Events: []egress.HistoryEvent{{Target: "archived.example"}}},
	}}})
	m = next.(networkModel)
	if m.showAll || m.machine != "" || !strings.Contains(m.View(), "Checking VM status") {
		t.Fatal(m.View())
	}

	next, _ = m.Update(networkRuntimeLoaded{running: map[string]bool{live: true, alsoLive: true, "nook-run-other-home": true}})
	m = next.(networkModel)
	view := ansi.Strip(m.View())
	if m.machine != live || !strings.Contains(view, "LIVE "+live) || !strings.Contains(view, "LIVE "+alsoLive) ||
		strings.Contains(view, archived) || strings.Contains(view, "archived.example") || strings.Contains(view, "nook-run-other-home") {
		t.Fatal(view)
	}

	m, _ = networkKey(m, 'j')
	if m.machine != alsoLive {
		t.Fatal(m.machine)
	}
	m, _ = networkKey(m, 'f')
	view = ansi.Strip(m.View())
	if m.machine != alsoLive || !strings.Contains(view, archived) || strings.Contains(view, "LIVE "+archived) {
		t.Fatal(view)
	}
	m, _ = networkKey(m, 'k')
	if m.machine != archived {
		t.Fatal(m.machine)
	}

	m.before, m.newerPages = 100, []int64{0}
	m, _ = networkKey(m, 'f')
	_ = m.poll()()
	if m.machine != live || query.Machine != live || query.Before != 0 || len(m.newerPages) != 0 {
		t.Fatal(m.machine, query, m.newerPages)
	}
	next, _ = m.Update(networkRuntimeLoaded{running: map[string]bool{alsoLive: true}})
	m = next.(networkModel)
	if m.machine != alsoLive || strings.Contains(m.View(), "LIVE "+live) {
		t.Fatal(m.View())
	}
	next, _ = m.Update(networkRuntimeLoaded{running: map[string]bool{}})
	m = next.(networkModel)
	if m.machine != "" || !strings.Contains(m.View(), "No running VMs with history") {
		t.Fatal(m.View())
	}
}

func TestNetworkHistoryCanOpenArchivesWithoutRuntime(t *testing.T) {
	const archived = "nook-run-archived"
	m := newNetworkModel("/home", nil, nil)
	next, _ := m.Update(networkRuntimeLoaded{err: errors.New("smolvm not found")})
	next, _ = next.Update(networkLoaded{survey: egress.Survey{History: egress.HistoryView{
		Machines: []egress.MachineHistory{{Machine: archived}},
		Page:     egress.HistoryPage{Machine: archived, Events: []egress.HistoryEvent{{Target: "archived.example"}}},
	}}})
	m = next.(networkModel)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "VM status unavailable") || strings.Contains(view, archived) {
		t.Fatal(view)
	}
	m, _ = networkKey(m, 'f')
	view = ansi.Strip(m.View())
	if m.machine != archived || !strings.Contains(view, "archived.example") || strings.Contains(view, "LIVE "+archived) {
		t.Fatal(view)
	}
}

func TestNetworkRuntimeRefreshDoesNotResetInitialArchiveQuery(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.showAll, m.machine, m.before = true, "nook-run-archive", 100
	next, _ := m.Update(networkRuntimeLoaded{running: map[string]bool{}})
	m = next.(networkModel)
	if m.machine != "nook-run-archive" || m.before != 100 {
		t.Fatal(m.machine, m.before)
	}
}

func TestNetworkHistoryShowsDecisionAndCoverageWithoutANSIInjection(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.machine = "nook-run-ended"
	m.showAll = true
	m.survey.History = egress.HistoryView{Machines: []egress.MachineHistory{{Machine: m.machine, Coverage: "denials-only", Ended: time.Now(), Count: 2}},
		Page: egress.HistoryPage{Machine: m.machine, Events: []egress.HistoryEvent{
			{Time: time.Now(), Protocol: "tcp", Decision: "denied", Target: "evil\x1b[31m", Reason: "local_policy"},
			{Time: time.Now(), Protocol: "dns", Decision: "allowed", Target: "example.com", Reason: "local_policy"},
		}}}
	view := ansi.Strip(m.View())
	for _, value := range []string{"DENIALS ONLY", "DENIED", "ALLOWED", "NOT captured", m.machine} {
		if !strings.Contains(view, value) {
			t.Fatal(value, view)
		}
	}
	if strings.Contains(view, "\x1b") {
		t.Fatal("terminal injection")
	}
}
