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

func networkEntry(id, target string) egress.Entry {
	return egress.Entry{Info: egress.Info{Session: "session", Project: "/project", Profile: "work", Agent: "agent"},
		Request: egress.Request{ID: id, Target: target, Deadline: time.Now().Add(10 * time.Second)}}
}

func networkKey(m networkModel, key rune) (networkModel, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
	return next.(networkModel), cmd
}

func TestNetworkRequiresExplicitStableSelection(t *testing.T) {
	var decided egress.Entry
	var choice string
	m := newNetworkModel("/home", nil, func(e egress.Entry, c string) error { decided, choice = e, c; return nil })
	m.historyMode = false
	a, b := networkEntry("a", "203.0.113.1:443"), networkEntry("b", "203.0.113.2:443")
	next, _ := m.Update(networkLoaded{survey: egress.Survey{Sessions: 1, Requests: []egress.Entry{a, b}}})
	m = next.(networkModel)
	if m.selection != "" {
		t.Fatal("initial automatic selection")
	}
	m, cmd := networkKey(m, 'a')
	if cmd != nil {
		t.Fatal("approval without selection")
	}
	m, _ = networkKey(m, 'j')
	if m.selection != a.Key() {
		t.Fatal(m.selection)
	}
	// An earlier group inserts a leaf before the selection.
	b.Info.Profile = "personal"
	next, _ = m.Update(networkLoaded{survey: egress.Survey{Requests: []egress.Entry{a, b}}})
	m = next.(networkModel)
	if m.selectedIndex() != 1 {
		t.Fatal("selection drifted")
	}
	m, cmd = networkKey(m, 's')
	if cmd == nil || !m.busy {
		t.Fatal("missing decision command")
	}
	result := cmd()
	if decided.Key() != a.Key() || choice != "session" {
		t.Fatal(decided, choice)
	}
	m, cmd = networkKey(m, 'a')
	if cmd != nil {
		t.Fatal("double approval while busy")
	}
	next, _ = m.Update(result)
	m = next.(networkModel)
	if m.busy || m.selection != "" {
		t.Fatal(m)
	}
	m, _ = networkKey(m, 'j')
	next, _ = m.Update(networkLoaded{survey: egress.Survey{Requests: []egress.Entry{a}}})
	m = next.(networkModel)
	m, cmd = networkKey(m, 'a')
	if m.selection != "" || cmd != nil {
		t.Fatal("expired selection transferred approval")
	}
}

func TestNetworkDecisionKeysAndErrors(t *testing.T) {
	for key, want := range map[rune]string{'a': "once", 's': "session", 'd': "deny"} {
		var choice string
		m := newNetworkModel("/home", nil, func(_ egress.Entry, c string) error { choice = c; return errors.New("expired") })
		m.historyMode = false
		entry := networkEntry("a", "203.0.113.1:443")
		m.survey.Requests, m.selection = []egress.Entry{entry}, entry.Key()
		m, cmd := networkKey(m, key)
		next, _ := m.Update(cmd())
		if choice != want || !strings.Contains(next.(networkModel).status, "expired") {
			t.Fatal(choice, want)
		}
	}
}

func TestNetworkExpiryBeforeRefreshDisarmsRequest(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode = false
	entry := networkEntry("a", "203.0.113.1:80")
	entry.Request.Deadline = time.Now().Add(-time.Second)
	m.survey.Requests, m.selection = []egress.Entry{entry, networkEntry("b", "203.0.113.2:80")}, entry.Key()
	m, cmd := networkKey(m, 'a')
	if cmd != nil || m.selection != "" || !strings.Contains(m.status, "expired") {
		t.Fatal("expired request stayed armed")
	}
}

func TestNetworkViewShowsHTTPHostBeforeSelection(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode = false
	entry := networkEntry("a", "203.0.113.1:80")
	entry.Request.HTTPHost = "github.com"
	m.survey.Requests = []egress.Entry{entry, networkEntry("b", "203.0.113.2:80")}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "github.com (HTTP Host)* · 203.0.113.1:80 ·") ||
		!strings.Contains(view, "203.0.113.2:80 ·") || strings.Contains(view, "TCP destination:") {
		t.Fatal(view)
	}
	m.selection = entry.Key()
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "TCP destination: 203.0.113.1:80") ||
		!strings.Contains(view, "HTTP Host (unverified): github.com") {
		t.Fatal(view)
	}
}

func TestNetworkApprovalsGroupByProfileAndVMLaunch(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode = false
	makeEntry := func(id, target, profile, session, vm string, seconds int) egress.Entry {
		e := networkEntry(id, target)
		e.Info.Profile, e.Info.Session, e.Request.Machine = profile, session, vm
		e.Request.Deadline = time.Now().Add(time.Duration(seconds) * time.Second)
		return e
	}
	a := makeEntry("a", "203.0.113.1:80", "personal", "one", "vm-1", 10)
	b := makeEntry("b", "203.0.113.2:443", "personal", "one", "vm-1", 6)
	b.Request.Hint = "tls.example"
	c := makeEntry("a", "203.0.113.3:80", "personal", "two", "vm-1", 10)
	d := makeEntry("d", "203.0.113.4:80", "personal", "two", "vm-2", 6)
	e := makeEntry("e", "203.0.113.5:80", "work", "three", "vm-3", 1)
	raw := []egress.Entry{e, a, c, d, b}
	next, _ := m.Update(networkLoaded{survey: egress.Survey{Requests: raw}})
	m = next.(networkModel)
	view := ansi.Strip(m.View())
	want := []string{
		"├─ Profile personal · 4 pending",
		"│  ├─ VM agent · vm-1 · 2 pending",
		"│  │  ├─   tls.example (TLS SNI)* · 203.0.113.2:443",
		"│  │  └─   203.0.113.1:80",
		"│  ├─ VM agent · vm-1 · 1 pending",
		"│  │  └─   203.0.113.3:80",
		"│  └─ VM agent · vm-2 · 1 pending",
		"│     └─   203.0.113.4:80",
		"└─ Profile work · 1 pending",
		"   └─ VM agent · vm-3 · 1 pending",
		"      └─   203.0.113.5:80",
	}
	remaining := view
	for _, row := range want {
		_, rest, ok := strings.Cut(remaining, row)
		if !ok {
			t.Fatalf("missing ordered row %q:\n%s", row, view)
		}
		remaining = rest
	}
	if raw[0].Key() != e.Key() || m.selection != "" {
		t.Fatal("input mutated or request automatically selected")
	}
	for _, request := range []egress.Entry{b, a, c, d, e} {
		m, _ = networkKey(m, 'j')
		if m.selection != request.Key() || !strings.Contains(ansi.Strip(m.View()), "▸ ") {
			t.Fatalf("j selected %q, want %q", m.selection, request.Key())
		}
	}
	m, _ = networkKey(m, 'k')
	if m.selection != d.Key() {
		t.Fatal("k did not skip across group headings")
	}
}

func TestNetworkApprovalWindowKeepsSelectedRequestAndAncestors(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode, m.width, m.height = false, 48, 22
	var requests []egress.Entry
	address := "[ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff]:65535"
	for i, profile := range []string{"first", "second", "third", "last"} {
		e := networkEntry(profile, address)
		e.Info.Profile, e.Request.Machine = profile, "vm-"+profile
		e.Request.HTTPHost = strings.Repeat("long-", 35) + "example"
		requests = append(requests, e)
		if i == 3 {
			m.selection = e.Key()
		}
	}
	next, _ := m.Update(networkLoaded{survey: egress.Survey{Requests: requests}})
	m = next.(networkModel)
	view := ansi.Strip(m.View())
	for _, marker := range []string{"Requests", "Profile last", "VM agent · vm-last", "▸ ", "TCP destination:\n" + address, "d deny · Tab history · q quit"} {
		if !strings.Contains(view, marker) {
			t.Fatalf("missing %q:\n%s", marker, view)
		}
	}
	if lines := strings.Split(view, "\n"); len(lines) > m.height {
		t.Fatalf("view has %d rows, terminal has %d", len(lines), m.height)
	}
}

func TestNetworkViewEscapesGuestHintsAndFitsWidth(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode = false
	entry := networkEntry("a", "203.0.113.1:443")
	entry.Request.Hint = "evil\x1b[31m\nname"
	entry.Request.HTTPHost = "evil\x1b[31m\nhost"
	m.survey.Requests, m.selection = []egress.Entry{entry}, entry.Key()
	m.width = 60
	view := ansi.Strip(m.View())
	if strings.Contains(view, "\x1b") || strings.Contains(view, "\nname") || strings.Contains(view, "\nhost") || !strings.Contains(view, "unverified") {
		t.Fatal(view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal(line)
		}
	}
}
