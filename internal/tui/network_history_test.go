package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func TestNetworkDefaultsToApprovalsAndNeverApprovesArchivedRows(t *testing.T) {
	calls := 0
	m := newNetworkModel("/home", nil, func(egress.Entry, string) error { calls++; return nil })
	entry := networkEntry("pending", "203.0.113.1:443")
	m.survey.Requests = []egress.Entry{entry}
	_, cmd := networkKey(m, 'a')
	if m.historyMode || !strings.Contains(m.View(), "[Approvals]") || cmd != nil {
		t.Fatal("expected approvals without an armed request")
	}
	m, _ = networkKey(m, 'j')
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(networkModel)
	if !m.historyMode || m.selection != "" {
		t.Fatal("expected history after Tab with selection cleared")
	}
	for _, key := range []rune{'a', 's', 'd'} {
		var cmd tea.Cmd
		m, cmd = networkKey(m, key)
		if cmd != nil || !m.historyMode {
			t.Fatal("history acted as approval")
		}
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(networkModel)
	if m.historyMode || m.selection != "" || calls != 0 {
		t.Fatal(m.selection, calls)
	}
	m, _ = networkKey(m, 'j')
	m, cmd = networkKey(m, 'a')
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
	m.historyMode = true
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
	m.historyMode = true
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
	m.historyMode = true
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

func TestNetworkHistoryGroupsProfilesAndNestsSelectedVMEvents(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode, m.showAll, m.machine = true, true, "work-new"
	machines := []egress.MachineHistory{
		{Machine: "work-new", Info: egress.Info{Profile: "work", Agent: "pi"}, Count: 2},
		{Machine: "personal", Info: egress.Info{Profile: "personal", Agent: "pi"}},
		{Machine: "work-old", Info: egress.Info{Profile: "work", Agent: "claude"}},
	}
	page := egress.HistoryPage{Machine: "work-new", Events: []egress.HistoryEvent{
		{Target: "example.com", Protocol: "dns", Decision: "allowed"},
		{Target: "203.0.113.1:443", Protocol: "tcp", Decision: "denied"},
	}}
	next, _ := m.Update(networkLoaded{survey: egress.Survey{History: egress.HistoryView{Machines: machines, Page: page}}})
	m = next.(networkModel)
	view := ansi.Strip(m.View())
	ordered := []string{
		"├─ Profile personal · 1 VM",
		"│  └─   VM pi · personal · 0 events",
		"└─ Profile work · 2 VMs",
		"   ├─ ▸ VM pi · work-new · 2 events",
		"   │  ├─ ALLOWED DNS example.com",
		"   │  └─ DENIED TCP 203.0.113.1:443",
		"   └─   VM claude · work-old · 0 events",
	}
	rest := view
	for _, row := range ordered {
		_, after, ok := strings.Cut(rest, row)
		if !ok {
			t.Fatalf("missing ordered row %q:\n%s", row, view)
		}
		rest = after
	}
	if machines[0].Machine != "work-new" {
		t.Fatal("history input order was mutated")
	}
	m, _ = networkKey(m, 'j')
	view = ansi.Strip(m.View())
	if m.machine != "work-old" || strings.Contains(view, "example.com") ||
		!strings.Contains(view, "      └─ Loading machine history…") {
		t.Fatal(view)
	}
	m, _ = networkKey(m, 'k')
	m, _ = networkKey(m, 'k')
	if m.machine != "personal" {
		t.Fatal("VM navigation diverged from visual order", m.machine)
	}
}

func TestNetworkHistoryFitsSmallTerminalAndWaitsForResizedPage(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode, m.showAll, m.machine = true, true, "vm-9"
	var machines []egress.MachineHistory
	for i := range 10 {
		machines = append(machines, egress.MachineHistory{Machine: fmt.Sprintf("vm-%d", i),
			Info: egress.Info{Profile: fmt.Sprintf("profile-%d", i), Agent: "pi"}, Warning: "audit warning"})
	}
	page := egress.HistoryPage{Machine: m.machine, Events: []egress.HistoryEvent{
		{Target: "example.com", Protocol: "dns", Decision: "allowed"},
		{Target: "203.0.113.1:443", Protocol: "tcp", Decision: "denied"},
	}, HasOlder: true, NextBefore: 20}
	next, _ := m.Update(networkLoaded{survey: egress.Survey{History: egress.HistoryView{Machines: machines, Page: page}}})
	next, _ = next.Update(tea.WindowSizeMsg{Width: 48, Height: 22})
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(networkModel)
	if m.before != 0 || !strings.Contains(m.View(), "Loading machine history…") {
		t.Fatal("stale cursor used after resize", m.before, m.View())
	}
	page.Events, page.NextBefore = page.Events[:1], 40
	next, _ = m.Update(networkLoaded{survey: egress.Survey{History: egress.HistoryView{Machines: machines, Page: page}}})
	m = next.(networkModel)
	view := ansi.Strip(m.View())
	for _, marker := range []string{"VMs 9–10 of 10", "Profile profile-9", "▸ VM pi · vm-9", "└─ ALLOWED DNS example.com", "Tab approvals · q quit"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("missing %q:\n%s", marker, view)
		}
	}
	if len(strings.Split(view, "\n")) > m.height {
		t.Fatal(view)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if next.(networkModel).before != 40 {
		t.Fatal("did not use the cursor of the resized page")
	}
}

func TestNetworkHistoryShowsDecisionAndCoverageWithoutANSIInjection(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.machine = "nook-run-ended"
	m.historyMode, m.showAll = true, true
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
