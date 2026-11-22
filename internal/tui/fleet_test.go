package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

func fleetUpdate(m fleetModel, msg tea.Msg) (fleetModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(fleetModel), cmd
}

func fleetKey(m fleetModel, value string) (fleetModel, tea.Cmd) {
	return fleetUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)})
}

func fleetSnapshot() fleet.Snapshot {
	return fleet.Snapshot{Instances: []fleet.Instance{
		{Name: "run-a", Project: "/repo", Agent: "pi", Profile: "work", State: "running", CPUs: 2, MemoryMiB: 2048, AgentVersion: "1.4.0", Image: "/images/dev.smolmachine", ImageName: "dev", BaseImage: "node:24-bookworm", CreatedAt: time.Unix(1731920000, 0), Mounts: []config.Mount{
			{Host: "/repo", Guest: "/workspace"}, {Host: "/data/profiles/work/pi", Guest: "/root"}, {Host: "/docs", Guest: "/mnt/docs", ReadOnly: true},
		}},
		{Name: "run-b", Project: "/z-other project", Agent: "claude", Profile: "work", State: "unreachable", CPUs: 4, MemoryMiB: 4096, Mounts: []config.Mount{{Host: "/repo", Guest: "/workspace"}}},
	}}
}

func loadedFleet(t *testing.T) fleetModel {
	t.Helper()
	load := func(context.Context) (fleet.Snapshot, error) { return fleetSnapshot(), nil }
	m := newFleetModel(context.Background(), "/repo", load)
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 50})
	m, cmd := fleetUpdate(m, m.Init()())
	if cmd == nil {
		t.Fatal("refresh not scheduled")
	}
	chooseVM(t, &m, "run-a")
	m.navigatorFocus = false // These tests exercise inspector selection.
	return m
}

func TestFleetAllDetailsAndMountsVisibleWithoutTabs(t *testing.T) {
	m := loadedFleet(t)
	view := ansi.Strip(m.View())
	for _, value := range []string{"agentnook fleet", "VM STATUS", "Navigator", "Instances", "Version¹", "running", "Machine", "node:24-bookworm", "2 vCPU / 2 GiB", "1.4.0", "Storage", "unknown", "Activity", "Runtime", "Heartbeat", "awaiting heartbeat progress", "Mounts · 3", "/repo → /workspace [rw]", "/data/profiles/work/pi → /root [rw]", "/docs → /mnt/docs [ro]", "Record age, not uptime"} {
		if !strings.Contains(view, value) {
			t.Errorf("missing %q in\n%s", value, view)
		}
	}
	if !strings.Contains(view, "Tab focus") {
		t.Fatal("focus navigation help missing", view)
	}
	m, _ = fleetKey(m, "j")
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "4 vCPU / 4 GiB") || !strings.Contains(view, "Mounts · 1") || strings.Contains(view, "/mnt/docs") || !strings.Contains(view, "VM unreachable") {
		t.Fatal("details did not follow selection", view)
	}
}

func TestFleetGlobalScopeAndProjectPaths(t *testing.T) {
	m := loadedFleet(t)
	m.scope = "All projects · AGENTNOOK_HOME: /data"
	for _, width := range []int{160, 120, 80, 46} {
		chooseVM(t, &m, "run-a")
		m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: width, Height: 50})
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "All projects") || !strings.Contains(view, "Project") || !strings.Contains(view, "/repo") {
			t.Fatalf("missing global scope or project at width %d:\n%s", width, view)
		}
		if width == 160 && (!strings.Contains(view, "Navigator") || !strings.Contains(view, "Instances")) {
			t.Fatal("wide view missing split panes", view)
		}
		m, _ = fleetKey(m, "j")
		if view := ansi.Strip(m.View()); !strings.Contains(view, "/z-other project") {
			t.Fatal("project did not follow selection", view)
		}
		for _, line := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(line) > width {
				t.Fatalf("global view overflows width %d: %q", width, line)
			}
		}
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 50})
	m, _ = fleetUpdate(m, fleetResultMsg{at: time.Now()})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "in this scope") || strings.Contains(view, "this exact directory") || strings.Contains(view, "Start nook here") {
		t.Fatal("global empty state implies local project", view)
	}
}

func TestFleetPiStatusUptimeHeartbeatAndStaleDiscovery(t *testing.T) {
	m := loadedFleet(t)
	now := time.Now()
	data := fleetSnapshot()
	data.Instances[0].Activity = telemetry.Sample{Fresh: true, HeartbeatAt: now, Report: telemetry.Report{State: "working", UptimeSeconds: 754}}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: now})
	for _, width := range []int{120, 80, 46} {
		m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: width, Height: 50})
		view := ansi.Strip(m.View())
		for _, value := range []string{"VM STATUS", "ACTIVITY", "UPTIME", "working", "12m34s"} {
			if !strings.Contains(view, value) {
				t.Fatalf("missing %q at width %d:\n%s", value, width, view)
			}
		}
		if !strings.Contains(fleetHeartbeat(m.snapshot.Instances[0], now), "host-observed") {
			t.Fatal("heartbeat details missing")
		}
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 50})
	m, _ = fleetUpdate(m, fleetResultMsg{err: errors.New("offline"), at: now.Add(6 * time.Second)})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "STALE") || strings.Contains(view, "working") || strings.Contains(view, "12m34s") || !strings.Contains(view, "running") {
		t.Fatal("stale discovery implies fresh Pi or hides VM", view)
	}
	if got := fleetUptime(fleet.Instance{Activity: telemetry.Sample{Fresh: true}}); got != "0s" {
		t.Fatal("zero uptime treated as missing", got)
	}
}

func TestFleetSelectionSurvivesRefreshAndRemoval(t *testing.T) {
	m := loadedFleet(t)
	m, _ = fleetKey(m, "j")
	if m.cursor != 1 {
		t.Fatal("selection did not move")
	}
	data := fleetSnapshot()
	data.Instances[0], data.Instances[1] = data.Instances[1], data.Instances[0]
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: time.Now()})
	if m.cursor != 0 || m.snapshot.Instances[m.cursor].Name != "run-b" {
		t.Fatal("selection jumped on reorder")
	}
	data.Instances = data.Instances[1:]
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: time.Now()})
	if m.cursor != 0 || m.snapshot.Instances[m.cursor].Name != "run-a" {
		t.Fatal("removed selection not clamped")
	}
	m, _ = fleetUpdate(m, fleetResultMsg{at: time.Now()})
	m, _ = fleetKey(m, "j")
	if m.cursor != 0 || !strings.Contains(m.View(), "No running agentnook instances") {
		t.Fatal("empty transition failed")
	}
}

func TestFleetErrorsRetainSnapshotThenRecover(t *testing.T) {
	m := loadedFleet(t)
	m, _ = fleetUpdate(m, fleetResultMsg{err: errors.New("discovery failed")})
	if view := m.View(); !strings.Contains(view, "STALE") || !strings.Contains(view, "discovery failed") || !strings.Contains(view, "/mnt/docs") {
		t.Fatal("error hid previous snapshot", view)
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: fleetSnapshot(), at: time.Now()})
	if strings.Contains(m.View(), "STALE") || m.errText != "" {
		t.Fatal("failed to recover")
	}
	m = newFleetModel(context.Background(), "/repo", nil)
	m, _ = fleetUpdate(m, fleetResultMsg{err: errors.New("first poll failed")})
	if !strings.Contains(m.View(), "Cannot discover") || strings.Contains(m.View(), "No running") {
		t.Fatal("initial error presented as empty")
	}
}

func TestFleetResizeScrollAndSafeText(t *testing.T) {
	m := loadedFleet(t)
	data := fleetSnapshot()
	data.Instances[0].Image = "untrusted\x1b]52;c;hello\a\nimage"
	for i := 0; i < 30; i++ {
		data.Instances[0].Mounts = append(data.Instances[0].Mounts, config.Mount{Host: fmt.Sprintf("/very long folder with spaces/資料/%d", i), Guest: fmt.Sprintf("/mnt/%d", i)})
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: time.Now()})
	for _, size := range [][2]int{{120, 45}, {80, 24}, {60, 20}, {46, 14}, {20, 10}} {
		m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View()
		if strings.Contains(view, "\x1b]52") || strings.Contains(view, "\a") {
			t.Fatal("terminal control leaked")
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) > size[0] {
				t.Fatalf("line overflow for %v (%d): %q", size, lipgloss.Width(line), line)
			}
		}
		if lines := strings.Count(view, "\n") + 1; lines > size[1] {
			t.Fatalf("height overflow for %v: %d", size, lines)
		}
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = fleetUpdate(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.details.YOffset == 0 {
		t.Fatal("details did not scroll")
	}
	m, _ = fleetKey(m, "j")
	if m.details.YOffset != 0 {
		t.Fatal("selection did not reset detail scroll")
	}
}

func TestFleetQuitAndPolling(t *testing.T) {
	m := loadedFleet(t)
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
		_, cmd := fleetUpdate(m, key)
		if cmd == nil {
			t.Fatal("quit missing")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("not a watch-only quit")
		}
	}
	_, cmd := fleetUpdate(m, fleetRefreshMsg{})
	if cmd == nil {
		t.Fatal("no discovery command")
	}
	if result := cmd().(fleetResultMsg); result.err != nil || len(result.snapshot.Instances) != 2 {
		t.Fatal("discovery command failed", result)
	}
}

func TestWatchFleetCancelsInFlightDiscovery(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	started, cancelled := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (fleet.Snapshot, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return fleet.Snapshot{}, ctx.Err()
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- WatchFleet("/repo", load, in, &out) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("watch failed before discovery: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("discovery never started")
	}
	go func() { _, _ = writer.Write([]byte("q")) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quit waited for blocked discovery")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("in-flight discovery was not cancelled")
	}
}
