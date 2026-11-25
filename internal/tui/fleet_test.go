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
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

func fleetUpdate(m fleetModel, msg tea.Msg) (fleetModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(fleetModel), cmd
}

func watchKey(value string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)} }

func fleetSnapshot() fleet.Snapshot {
	return fleet.Snapshot{Instances: []fleet.Instance{
		{Name: "run-a", Project: "/repo", Agent: "pi", Profile: "private", State: "running", Network: "ask", CPUs: 2, MemoryMiB: 2048,
			AgentVersion: "1.4.0", Image: "/images/dev.smolmachine", ImageName: "dev", BaseImage: "node:24-bookworm", Mounts: []config.Mount{
				{Host: "/repo", Guest: "/workspace"}, {Host: "/data/profiles/private/pi", Guest: "/root"}, {Host: "/docs", Guest: "/mnt/docs", ReadOnly: true},
			}},
		{Name: "run-c", Project: "/elsewhere", Agent: "claude", Profile: "work", State: "unreachable", Network: "full", CPUs: 4, MemoryMiB: 4096},
		{Name: "run-b", Project: "/repo", Agent: "pi", Profile: "private", State: "running"},
	}}
}

func loadedFleet(t *testing.T) fleetModel {
	t.Helper()
	load := func(context.Context) (fleet.Snapshot, error) { return fleetSnapshot(), nil }
	m := newFleetModel(context.Background(), "All projects · AGENTNOOK_HOME: /data", load)
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	m, cmd := fleetUpdate(m, m.Init()())
	if cmd == nil {
		t.Fatal("refresh not scheduled")
	}
	return m
}

func TestWatchListAndDetails(t *testing.T) {
	m := loadedFleet(t)
	view := ansi.Strip(m.View())
	for _, value := range []string{"nook watch", "All projects", "3 VMs · 2 projects", "Profile private", "Profile work", "repo · VM run-a", "Network: ask · 3 mounts", "Launch limits  2 vCPU", "Network mode  ask", "/docs → /mnt/docs [ro]"} {
		if !strings.Contains(view, value) {
			t.Errorf("missing %q in\n%s", value, view)
		}
	}
	if strings.Contains(view, "Navigator") || strings.Contains(view, "Tab focus") || strings.Count(view, selectionMarker) != 1 {
		t.Fatal(view)
	}
	for _, name := range []string{"run-b", "run-c"} {
		m, _ = fleetUpdate(m, watchKey("j"))
		if m.instance().Name != name {
			t.Fatal("not navigating visible order", m.instance())
		}
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Network mode  full") || !strings.Contains(view, "unreachable") || strings.Contains(view, "/mnt/docs") {
		t.Fatal(view)
	}
	m, _ = fleetUpdate(m, watchKey("k"))
	if !strings.Contains(ansi.Strip(m.View()), "Network mode  unknown") {
		t.Fatal("missing mode must not be guessed")
	}
}

func TestWatchSelectionRefreshAndFallback(t *testing.T) {
	m := loadedFleet(t)
	m, _ = fleetUpdate(m, watchKey("j"))
	data := fleetSnapshot()
	data.Instances[0], data.Instances[2] = data.Instances[2], data.Instances[0]
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: time.Now()})
	if m.instance().Name != "run-b" {
		t.Fatal("selection jumped on reorder")
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: fleet.Snapshot{Instances: data.Instances[1:]}, at: time.Now()})
	if m.instance().Name != "run-a" {
		t.Fatal("selection did not fall back within workspace")
	}
	m, _ = fleetUpdate(m, fleetResultMsg{at: time.Now()})
	m, _ = fleetUpdate(m, watchKey("j"))
	if m.instance() != nil || !strings.Contains(m.View(), "No running agentnook instances") {
		t.Fatal(m.View())
	}
}

func TestWatchErrorsAndTelemetry(t *testing.T) {
	m := loadedFleet(t)
	now := time.Now()
	data := fleetSnapshot()
	data.Instances[0].Activity = telemetry.Sample{Fresh: true, HeartbeatAt: now, Report: telemetry.Report{State: "working", UptimeSeconds: 754}}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: now})
	if view := m.View(); !strings.Contains(view, "working") || !strings.Contains(view, "12m34s") {
		t.Fatal(view)
	}
	m, _ = fleetUpdate(m, fleetResultMsg{err: errors.New("discovery failed"), at: now.Add(time.Minute)})
	if view := m.View(); !strings.Contains(view, "STALE") || !strings.Contains(view, "discovery failed") || !strings.Contains(view, "/mnt/docs") ||
		strings.Contains(view, "working") || strings.Contains(view, "12m34s") || !strings.Contains(view, "running") {
		t.Fatal(view)
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: now})
	if strings.Contains(m.View(), "STALE") || m.errText != "" {
		t.Fatal(m.View())
	}
	m = newFleetModel(context.Background(), "/repo", nil)
	m, _ = fleetUpdate(m, fleetResultMsg{err: errors.New("first poll failed")})
	if !strings.Contains(m.View(), "Discovery unavailable") || strings.Contains(m.View(), "No running") {
		t.Fatal(m.View())
	}
}

func assertTerminalBounds(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("height %d > %d:\n%s", len(lines), height, view)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > width {
			t.Fatalf("width %d > %d: %q", ansi.StringWidth(line), width, line)
		}
	}
}

func TestWatchResizeScrollAndSafeText(t *testing.T) {
	m := loadedFleet(t)
	data := fleetSnapshot()
	data.Instances[0].Image = "untrusted\x1b]52;c;hello\a\nimage"
	for i := range 30 {
		data.Instances[0].Mounts = append(data.Instances[0].Mounts, config.Mount{Host: fmt.Sprintf("/very long folder with spaces/資料/%d", i), Guest: fmt.Sprintf("/mnt/%d", i)})
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: data, at: time.Now()})
	for _, size := range [][2]int{{120, 45}, {80, 24}, {46, 14}, {20, 10}, {1, 1}} {
		m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		assertTerminalBounds(t, m.View(), size[0], size[1])
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = fleetUpdate(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.details.YOffset == 0 || !strings.Contains(m.View(), "untrusted?") || strings.Contains(m.View(), "\x1b]52") || strings.Contains(m.View(), "\a") {
		t.Fatal(m.View())
	}
	m, _ = fleetUpdate(m, watchKey("j"))
	if m.details.YOffset != 0 {
		t.Fatal("selection did not reset detail scroll")
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
