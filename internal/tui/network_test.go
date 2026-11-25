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

func networkEntry(id, target string) egress.Entry {
	return egress.Entry{Info: egress.Info{Session: "session", Profile: "work", Project: "/code/agentnook", Agent: "pi"},
		Request: egress.Request{ID: id, Machine: "engine-vm", Target: target, Deadline: time.Now().Add(time.Minute)}}
}

func networkUpdate(m networkModel, msg tea.Msg) (networkModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(networkModel), cmd
}

func networkKey(m networkModel, key string) (networkModel, tea.Cmd) {
	next, cmd := m.key(key)
	return next.(networkModel), cmd
}

func networkLive(m networkModel, entries ...egress.Entry) networkModel {
	m, _ = networkUpdate(m, networkLiveLoaded{survey: egress.Survey{Requests: entries}})
	return m
}

func TestNetworkSelectionCannotRetargetToAnotherRequest(t *testing.T) {
	var got egress.Entry
	var choice string
	m := newNetworkModel("/data", egress.HistoryQuery{}, func(entry egress.Entry, value string) error {
		got, choice = entry, value
		return nil
	})
	a := networkEntry("a", "203.0.113.1:80")
	b := networkEntry("b", "203.0.113.2:80")
	m = networkLive(m, a, b)
	if _, cmd := networkKey(m, "a"); cmd != nil {
		t.Fatal("automatically armed request")
	}
	m, _ = networkKey(m, "j")
	armed := m.selected.request
	m = networkLive(m, b, a)
	if m.selected.request != armed {
		t.Fatal("refresh retargeted request")
	}
	m = networkLive(m, b)
	if _, cmd := networkKey(m, "s"); cmd != nil || m.selected.request != "" {
		t.Fatal("selection transferred to another request")
	}
	m, _ = networkKey(m, "j")
	m, cmd := networkKey(m, "s")
	if cmd == nil || !m.busy || m.selected.request != "" {
		t.Fatal("decision not issued or selection remains armed")
	}
	m, _ = networkUpdate(m, cmd())
	if got.Key() != b.Key() || choice != "session" || !strings.Contains(m.View(), "Decision sent: session") {
		t.Fatal(got, choice, m.View())
	}
}

func TestNetworkApprovalGuardsAndErrors(t *testing.T) {
	for _, key := range []string{"a", "s", "d"} {
		t.Run(key, func(t *testing.T) {
			m := newNetworkModel("/data", egress.HistoryQuery{}, func(egress.Entry, string) error { return errors.New("broker stopped") })
			entry := networkEntry("a", "[abcd:abcd:abcd:abcd:abcd:abcd:abcd:abcd]:65535")
			entry.Info.Project = "/code/" + strings.Repeat("project", 12)
			m = networkLive(m, entry)
			m, _ = networkKey(m, "j")
			m.width, m.height = 48, 22
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "\n"+entry.Request.Target+"\n") || !strings.Contains(view, "VM engine-vm") {
				t.Fatal("exact destination and VM must fit", view)
			}
			narrow := m
			narrow.width = 47
			if _, cmd := networkKey(narrow, key); cmd != nil {
				t.Fatal("approved in undersized terminal")
			}
			expired := m
			expired.requests = append([]egress.Entry(nil), m.requests...)
			expired.requests[0].Request.Deadline = time.Now().Add(-time.Second)
			if result, cmd := networkKey(expired, key); cmd != nil || result.selected.request != "" || !strings.Contains(result.decision, "expired") {
				t.Fatal("approved expired request")
			}
			invalid := m
			invalid.requests = append([]egress.Entry(nil), m.requests...)
			invalid.requests[0].Request.Target = strings.Repeat("x", 49)
			if _, cmd := networkKey(invalid, key); cmd != nil {
				t.Fatal("approved a truncated destination")
			}
			m, cmd := networkKey(m, key)
			if cmd == nil {
				t.Fatal("valid decision not sent")
			}
			m, _ = networkUpdate(m, cmd())
			if !strings.Contains(m.View(), "broker stopped") || m.busy {
				t.Fatal(m.View())
			}
		})
	}
}

func TestNetworkApprovalGroupingAndVisibleSelection(t *testing.T) {
	m := newNetworkModel("/data", egress.HistoryQuery{}, nil)
	var requests []egress.Entry
	for i := range 15 {
		e := networkEntry(fmt.Sprint(i), fmt.Sprintf("203.0.113.%d:80", i+1))
		e.Info.Session = fmt.Sprintf("session-%02d", i)
		e.Info.Profile = fmt.Sprintf("profile-%02d", i)
		requests = append(requests, e)
	}
	requests[14].Request.HTTPHost = "github.com\x1b]52;x\a"
	m = networkLive(m, requests...)
	m.timeline = demoTimeline()
	m.timeline.Machines = append(m.timeline.Machines, egress.MachineHistory{Machine: "nook-run-identified", Info: requests[14].Info})
	m.selected = networkKeyID{request: requests[14].Key()}
	for _, size := range [][2]int{{120, 50}, {80, 24}, {48, 22}, {20, 10}, {1, 1}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		assertTerminalBounds(t, view, m.width, m.height)
		if m.width >= networkMinWidth {
			plain := ansi.Strip(view)
			for _, value := range []string{"Profile profile-14", "agentnook · VM identified", "▸ ", "TCP destination: 203.0.113.15:80", "HTTP Host (unverified): github.com"} {
				if !strings.Contains(plain, value) {
					t.Fatal("missing", value, plain)
				}
			}
		}
		if strings.Contains(view, "\x1b]52") || strings.Contains(view, "\a") {
			t.Fatal(view)
		}
	}
}

func TestNetworkPendingIsIndependentOfHistoryFailure(t *testing.T) {
	m := newNetworkModel("/data", egress.HistoryQuery{}, func(egress.Entry, string) error { return nil })
	m, _ = networkUpdate(m, networkHistoryLoaded{err: errors.New("unreadable archive")})
	m = networkLive(m, networkEntry("a", "203.0.113.1:80"))
	m, _ = networkKey(m, "j")
	if _, cmd := networkKey(m, "a"); cmd == nil || !strings.Contains(m.View(), "unreadable archive") {
		t.Fatal(m.View())
	}
}
