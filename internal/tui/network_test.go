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
	// Reorder requests: retain identity, not cursor index.
	next, _ = m.Update(networkLoaded{survey: egress.Survey{Requests: []egress.Entry{b, a}}})
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

func TestNetworkViewEscapesGuestHintsAndFitsWidth(t *testing.T) {
	m := newNetworkModel("/home", nil, nil)
	m.historyMode = false
	entry := networkEntry("a", "203.0.113.1:443")
	entry.Request.Hint = "evil\x1b[31m\nname"
	m.survey.Requests, m.selection = []egress.Entry{entry}, entry.Key()
	m.width = 60
	view := ansi.Strip(m.View())
	if strings.Contains(view, "\x1b") || strings.Contains(view, "\nname") || !strings.Contains(view, "unverified") {
		t.Fatal(view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal(line)
		}
	}
}
