package tui

import (
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

func TestNetworkHistoryShowsDecisionAndCoverageWithoutANSIInjection(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.machine = "nook-run-ended"
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
