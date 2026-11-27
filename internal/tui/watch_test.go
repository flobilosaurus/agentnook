package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/images"
)

func updateWatch(m watchModel, msg tea.Msg) (watchModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(watchModel), cmd
}

func TestWatchTabsAndResponsivePanes(t *testing.T) {
	m := newWatchModel(context.Background(), WatchOptions{Root: "/data"})
	m, _ = updateWatch(m, fleetResultMsg{snapshot: fleetSnapshot(), at: time.Now()})
	m, _ = updateWatch(m, networkHistoryLoaded{archive: true, timeline: demoTimeline()})
	m, _ = updateWatch(m, networkLiveLoaded{survey: egress.Survey{Requests: []egress.Entry{networkEntry("a", "203.0.113.1:443")}}})
	m, _ = updateWatch(m, watchImagesLoaded{status: images.CatalogStatus{Images: []images.ImageStatus{{Name: "pi-dev", ArtifactState: "ready"}}}})
	wide := [][2]string{{"Instances", "repo · VM run-a"}, {"Pending requests", "Select a request"}, {"Recorded decisions", "DENIED · TCP"}, {"Images", "pi-dev"}}
	for _, size := range [][2]int{{110, 40}, {80, 27}, {60, 27}, {48, 27}, {48, 19}, {48, 18}, {1, 1}} {
		m, _ = updateWatch(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i, fragments := range wide {
			m, _ = updateWatch(m, watchKey(string(rune('1'+i))))
			view := ansi.Strip(m.View())
			assertTerminalBounds(t, view, size[0], size[1])
			if m.tab != WatchTab(i) || strings.Contains(view, "Launch]") {
				t.Fatal("wrong tab", m.tab, view)
			}
			if size[0] >= 80 {
				found := false
				for _, line := range strings.Split(view, "\n") {
					left, right, split := strings.Cut(line, monitorColumnGap)
					found = found || (split && strings.Contains(left, fragments[0]) && strings.Contains(right, fragments[1]))
				}
				if !found {
					t.Fatalf("tab %d is not split:\n%s", i, view)
				}
			} else if strings.Contains(view, monitorColumnGap) {
				t.Fatalf("tab %d should stack:\n%s", i, view)
			}
		}
	}
	m, _ = updateWatch(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.tab != WatchVMs {
		t.Fatal("Tab must wrap")
	}
	m, _ = updateWatch(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.tab != WatchImages {
		t.Fatal("Shift+Tab must wrap backward")
	}
}

func TestWatchTabUnderlineFitsAndTracksSelection(t *testing.T) {
	for _, width := range []int{48, 59, 60, 140} {
		for _, pending := range []int{0, 2, 100} {
			for i := range watchTabs {
				bar := ansi.Strip(renderWatchTabs(width, WatchTab(i), pending))
				assertTerminalBounds(t, bar, width, 2)
				labels, rail, ok := strings.Cut(bar, "\n")
				if !ok || ansi.StringWidth(rail) != width || !strings.Contains(labels, "4 Images") {
					t.Fatal(bar)
				}
				if !strings.Contains(labels, "Approvals"+watchTabBadge(pending)) {
					t.Fatal("wrong badge", bar)
				}
				pad := len(watchTabPadding(width))
				left := strings.Index(labels, watchTabs[i]) - 2 - pad
				length := len(watchTabs[i]) + 2 + 2*pad
				if WatchTab(i) == WatchApprovals {
					length += len(watchTabBadge(pending))
				}
				if strings.Index(rail, "━") != len(strings.Repeat("─", left)) || strings.Count(rail, "━") != length {
					t.Fatal("wrong underline", bar)
				}
			}
		}
	}
}

func TestWatchApprovalsStayExplicitAndAuditIsReadOnly(t *testing.T) {
	m := newWatchModel(context.Background(), WatchOptions{Start: WatchApprovals})
	requested := networkEntry("a", "[abcd:abcd:abcd:abcd:abcd:abcd:abcd:abcd]:65535")
	var got string
	m.approvals.decide = func(entry egress.Entry, choice string) error { got = entry.Key() + ":" + choice; return nil }
	m, _ = updateWatch(m, networkLiveLoaded{survey: egress.Survey{Requests: []egress.Entry{requested}}})
	if _, cmd := updateWatch(m, watchKey("j")); cmd != nil || m.approvals.selected.request != "" {
		t.Fatal("accepted selection before terminal size")
	}
	m, _ = updateWatch(m, tea.WindowSizeMsg{Width: 80, Height: 27})
	m, _ = updateWatch(m, watchKey("j"))
	if !strings.Contains(ansi.Strip(m.View()), requested.Request.Target) {
		t.Fatal("full destination is not visible", m.View())
	}
	m, _ = updateWatch(m, watchKey("3"))
	for _, key := range []string{"a", "s", "d"} {
		if _, cmd := updateWatch(m, watchKey(key)); cmd != nil {
			t.Fatal("audit emitted a decision", key)
		}
	}
	m, _ = updateWatch(m, watchKey("2"))
	if _, cmd := updateWatch(m, watchKey("a")); cmd != nil || m.approvals.selected.request != "" {
		t.Fatal("returning to Approvals rearmed a request")
	}
	m, _ = updateWatch(m, watchKey("j"))
	m, _ = updateWatch(m, tea.WindowSizeMsg{Width: 80, Height: 26})
	if _, cmd := updateWatch(m, watchKey("a")); cmd != nil {
		t.Fatal("decision emitted without enough rows")
	}
	m, _ = updateWatch(m, tea.WindowSizeMsg{Width: 80, Height: 27})
	_, cmd := updateWatch(m, watchKey("a"))
	if cmd == nil {
		t.Fatal("valid decision was not issued")
	}
	cmd()
	if got != requested.Key()+":once" {
		t.Fatal("wrong decision", got)
	}
}

func TestWatchAuditScopeAndIndependentPolls(t *testing.T) {
	m := newWatchModel(context.Background(), WatchOptions{})
	m, _ = updateWatch(m, fleetResultMsg{snapshot: fleetSnapshot(), at: time.Now()})
	m, _ = updateWatch(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.tab != WatchAudit || m.audit.historyQuery().Machine != "run-a" || m.approvals.historyQuery().Machine != "" {
		t.Fatal("VM audit filter escaped tab or targeted wrong VM")
	}
	m, _ = updateWatch(m, networkHistoryLoaded{archive: true, generation: m.audit.generation - 1, timeline: demoTimeline()})
	if m.audit.historyReady {
		t.Fatal("installed late audit result")
	}
	m, _ = updateWatch(m, networkHistoryLoaded{timeline: demoTimeline()})
	if m.audit.historyReady || !m.approvals.historyReady {
		t.Fatal("mixed up recent decisions and Audit")
	}
	m, _ = updateWatch(m, watchKey("x"))
	if m.audit.historyQuery().Machine != "" {
		t.Fatal("x did not clear VM filter")
	}
}

func TestWatchImagesLoadOnDemandAndRetry(t *testing.T) {
	loads := 0
	m := newWatchModel(context.Background(), WatchOptions{LoadImages: func() (images.CatalogStatus, error) {
		loads++
		if loads == 1 {
			return images.CatalogStatus{}, errors.New("catalog failed")
		}
		return images.CatalogStatus{Images: []images.ImageStatus{{Name: "ready-image"}}}, nil
	}})
	m, cmd := updateWatch(m, watchKey("4"))
	m, _ = updateWatch(m, cmd())
	if !strings.Contains(m.View(), "catalog failed") {
		t.Fatal(m.View())
	}
	m, cmd = updateWatch(m, watchKey("r"))
	m, _ = updateWatch(m, cmd())
	if loads != 2 || !strings.Contains(m.View(), "ready-image") {
		t.Fatal(m.View())
	}
}
