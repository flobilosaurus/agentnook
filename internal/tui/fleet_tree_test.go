package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

func hierarchySnapshot() fleet.Snapshot {
	var items []fleet.Instance
	add := func(name, profile, project, repository, checkout, branch string) {
		items = append(items, fleet.Instance{Name: name, Profile: profile, Project: project,
			Workspace: fleet.Workspace{Repository: repository, Checkout: checkout, Branch: branch},
			State:     "running", Agent: "pi", CPUs: 2, MemoryMiB: 2048,
			Activity: telemetry.Sample{Fresh: true, HeartbeatAt: time.Now(), Report: telemetry.Report{State: "idle"}},
		})
	}
	add("main", "privat", "/code/agentnook", "/code/agentnook", "/code/agentnook", "main")
	add("watch-a", "privat", "/worktrees/watch", "/code/agentnook", "/worktrees/watch", "feat/watch")
	add("watch-b", "privat", "/worktrees/watch", "/code/agentnook", "/worktrees/watch", "feat/watch")
	add("subfolder", "privat", "/worktrees/watch/pkg/ui", "/code/agentnook", "/worktrees/watch", "feat/watch")
	add("plain", "privat", "/notes", "", "", "")
	add("other-repo", "privat", "/experiments/agentnook", "/experiments/agentnook", "/experiments/agentnook", "main")
	add("work-main", "arbeit", "/code/agentnook", "/code/agentnook", "/code/agentnook", "main")
	add("login", "arbeit", "/outside/login", "/code/storefront", "/outside/login", "fix/login")
	items[1].Activity.Report.State = "blocked"
	return fleet.Snapshot{Instances: items}
}

func hierarchyModel(t *testing.T) fleetModel {
	t.Helper()
	m := newFleetModel(context.Background(), "All projects · /data", nil)
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 140, Height: 48})
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: hierarchySnapshot(), at: time.Now()})
	return m
}

func chooseVM(t *testing.T, m *fleetModel, name string) {
	t.Helper()
	for index, n := range m.nodes {
		if n.kind == "VM" && m.snapshot.Instances[n.indices[0]].Name == name {
			m.selectVMNode(index)
			m.rebuildDetails()
			return
		}
	}
	t.Fatalf("missing VM %s", name)
}

func instanceNames(m fleetModel) []string {
	var names []string
	for _, index := range m.filtered {
		names = append(names, m.snapshot.Instances[index].Name)
	}
	return names
}

func TestFleetNavigatorHierarchyAndExactWorkspaceLists(t *testing.T) {
	m := hierarchyModel(t)
	if !m.navigatorFocus || m.currentNode().kind != "VM" {
		t.Fatal("initial selection must be a VM")
	}
	seen := make(map[string]bool)
	labels := make(map[string]string)
	for _, n := range m.nodes {
		if n.kind == "VM" {
			name := m.snapshot.Instances[n.indices[0]].Name
			if seen[name] {
				t.Fatal("VM appears twice", name)
			}
			seen[name] = true
		}
		if n.kind == "Project" && n.profile == "privat" {
			labels[n.path] = n.label
		}
		if n.kind == "Workspace" && n.path == "/worktrees/watch/pkg/ui" && n.label != "pkg/ui (folder)" {
			t.Fatal("subfolder was presented as checkout", n)
		}
	}
	if len(seen) != len(m.snapshot.Instances) || labels["/code/agentnook"] == labels["/experiments/agentnook"] {
		t.Fatal("missing VM or indistinguishable repositories", seen, labels)
	}
	for _, tc := range []struct {
		name  string
		names []string
	}{
		{"main", []string{"main"}},
		{"watch-a", []string{"watch-a", "watch-b"}},
		{"subfolder", []string{"subfolder"}},
		{"plain", []string{"plain"}},
		{"work-main", []string{"work-main"}},
		{"login", []string{"login"}},
	} {
		chooseVM(t, &m, tc.name)
		if got := instanceNames(m); !reflect.DeepEqual(got, tc.names) {
			t.Fatalf("%s: %v", tc.name, got)
		}
	}
	key := m.currentNode().key
	for i, n := range m.nodes {
		if n.kind != "VM" {
			m.selectVMNode(i)
			if m.currentNode().key != key {
				t.Fatal("group accepted selection", n)
			}
		}
	}
}

func TestFleetNavigatorAlwaysOpenAndVMOnlyNavigation(t *testing.T) {
	m := hierarchyModel(t)
	before := append([]fleetNode(nil), m.nodes...)
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeySpace}, {Type: tea.KeyLeft}, {Type: tea.KeyRight}} {
		m, _ = fleetUpdate(m, key)
		if !reflect.DeepEqual(m.nodes, before) || m.currentNode().kind != "VM" {
			t.Fatal("navigation collapsed tree or selected group")
		}
	}
	var order []string
	for _, n := range m.nodes {
		if n.kind == "VM" {
			order = append(order, m.snapshot.Instances[n.indices[0]].Name)
		}
	}
	for _, focus := range []bool{true, false} {
		m.navigatorFocus = focus
		chooseVM(t, &m, order[0])
		for _, name := range order {
			if m.currentNode().kind != "VM" || m.currentInstance().Name != name {
				t.Fatalf("focus=%v: expected VM %s, got %+v", focus, name, m.currentNode())
			}
			m, _ = fleetKey(m, "j")
		}
		if m.currentInstance().Name != order[len(order)-1] {
			t.Fatal("bottom boundary moved selection")
		}
		for i := len(order) - 1; i >= 0; i-- {
			if m.currentNode().kind != "VM" || m.currentInstance().Name != order[i] {
				t.Fatal("up selected a heading or wrong VM")
			}
			m, _ = fleetKey(m, "k")
		}
		if m.currentInstance().Name != order[0] {
			t.Fatal("top boundary moved selection")
		}
	}
	chooseVM(t, &m, "watch-a")
	m.navigatorFocus = true
	m, _ = fleetUpdate(m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = fleetKey(m, "j")
	if m.navigatorFocus || m.currentInstance().Name != "watch-b" || m.currentNode().label != "watch-b" {
		t.Fatal("inspector and navigator selection diverged")
	}
	m, _ = fleetUpdate(m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if !m.navigatorFocus || m.currentInstance().Name != "watch-b" {
		t.Fatal("focus switch changed VM selection")
	}
	if view := ansi.Strip(m.View()); strings.Contains(view, "▾") || !strings.Contains(view, "Navigator") || !strings.Contains(view, "Instances") {
		t.Fatal("not an always-open navigator", view)
	}
}

func TestFleetHierarchyRefreshRemovalFallbackAndBranchRename(t *testing.T) {
	m := hierarchyModel(t)
	chooseVM(t, &m, "watch-b")
	key := m.currentNode().key
	snapshot := hierarchySnapshot()
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Workspace.Checkout == "/worktrees/watch" {
			snapshot.Instances[i].Workspace.Branch = "renamed"
		}
	}
	for left, right := 0, len(snapshot.Instances)-1; left < right; left, right = left+1, right-1 {
		snapshot.Instances[left], snapshot.Instances[right] = snapshot.Instances[right], snapshot.Instances[left]
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 140, Height: 24})
	m.details.SetYOffset(2)
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: snapshot, at: time.Now()})
	if m.currentInstance().Name != "watch-b" || m.currentNode().key != key || m.details.YOffset != 2 {
		t.Fatal("refresh or branch rename stole selection/scroll")
	}
	for _, n := range m.nodes {
		if n.kind == "Worktree" && n.path == "/worktrees/watch" && n.label != "renamed" {
			t.Fatal("branch label not updated")
		}
	}
	remove := func(names ...string) {
		t.Helper()
		var kept []fleet.Instance
		for _, item := range snapshot.Instances {
			found := false
			for _, name := range names {
				found = found || item.Name == name
			}
			if !found {
				kept = append(kept, item)
			}
		}
		snapshot.Instances = kept
		m, _ = fleetUpdate(m, fleetResultMsg{snapshot: snapshot, at: time.Now()})
		if m.currentNode() != nil && m.currentNode().kind != "VM" {
			t.Fatal("exit fell back to group instead of VM")
		}
	}
	remove("watch-b")
	if m.currentInstance().Name != "watch-a" || m.notice == "" || m.details.YOffset != 0 {
		t.Fatal("VM exit did not fall back within workspace")
	}
	remove("watch-a")
	if m.currentInstance().Name != "main" {
		t.Fatal("ended worktree did not fall back to repository VM", m.currentInstance())
	}
	remove("main", "subfolder")
	if m.currentInstance().Profile != "privat" {
		t.Fatal("ended repository did not fall back within profile")
	}
	remove("plain", "other-repo")
	if m.currentInstance().Profile != "arbeit" {
		t.Fatal("ended profile did not select remaining VM")
	}
}

func TestFleetVMSelectionSurvivesNewGroupsAndGitFallback(t *testing.T) {
	m := hierarchyModel(t)
	chooseVM(t, &m, "watch-b")
	snapshot := hierarchySnapshot()
	newVM := snapshot.Instances[0]
	newVM.Name, newVM.Profile = "new-vm", "aaa-new-profile"
	snapshot.Instances = append([]fleet.Instance{newVM}, snapshot.Instances...)
	for i := range snapshot.Instances {
		if snapshot.Instances[i].Project == "/worktrees/watch" {
			snapshot.Instances[i].Workspace = fleet.Workspace{}
		}
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: snapshot, at: time.Now()})
	if m.currentNode().kind != "VM" || m.currentInstance().Name != "watch-b" || m.currentNode().key != "v\x00watch-b" {
		t.Fatal("new group or Git fallback stole VM selection", m.currentNode())
	}
	if got := instanceNames(m); !reflect.DeepEqual(got, []string{"watch-a", "watch-b"}) {
		t.Fatal("inspector lost workspace isolation after regrouping", got)
	}
}

func TestFleetHierarchySafeAndBoundedAcrossTerminalSizes(t *testing.T) {
	m := hierarchyModel(t)
	snapshot := hierarchySnapshot()
	snapshot.Instances[0].Workspace.Branch = "bad\x1b]52;c;secret\a\nbranch"
	snapshot.Instances[0].Workspace.Repository = "/code/untrusted\x1b[2J\a"
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: snapshot, at: time.Now()})
	for _, dimensions := range [][2]int{{180, 60}, {120, 35}, {102, 20}, {100, 20}, {80, 24}, {60, 20}, {46, 14}, {20, 10}} {
		m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: dimensions[0], Height: dimensions[1]})
		for steps := 0; steps < len(m.nodes)+2; steps++ {
			m, _ = fleetKey(m, "j")
			view := m.View()
			if strings.Contains(view, "\x1b]52") || strings.Contains(view, "\a") || strings.Contains(view, "\x1b[2J") {
				t.Fatal("untrusted metadata leaked terminal controls")
			}
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > dimensions[0] {
					t.Fatalf("%v: line overflow: %q", dimensions, line)
				}
			}
			if strings.Count(view, "\n")+1 > dimensions[1] {
				t.Fatalf("%v: height overflow", dimensions)
			}
		}
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 25})
	if !m.layout().split {
		t.Fatal("wide terminal must split")
	}
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 25})
	if m.layout().split {
		t.Fatal("narrow terminal must stack")
	}
}

func TestFleetNavigatorScrollAndEmptyRecovery(t *testing.T) {
	m := hierarchyModel(t)
	snapshot := hierarchySnapshot()
	for i := 0; i < 40; i++ {
		snapshot.Instances = append(snapshot.Instances, fleet.Instance{Name: fmt.Sprintf("extra-%02d", i), Profile: "privat", Project: fmt.Sprintf("/projects/project-%02d", i), State: "running"})
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: snapshot, at: time.Now()})
	m, _ = fleetUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for range len(m.nodes) {
		m, _ = fleetKey(m, "j")
	}
	if m.navOffset == 0 || m.navCursor < m.navOffset || m.navCursor >= m.navOffset+m.layout().navHeight {
		t.Fatal("selection not kept in navigator viewport")
	}
	for _, err := range []error{fmt.Errorf("offline"), nil} {
		m, _ = fleetUpdate(m, fleetResultMsg{err: err, at: time.Now()})
		for _, key := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyDown}, {Type: tea.KeyEnter}} {
			m, _ = fleetUpdate(m, key)
		}
	}
	if len(m.nodes) != 0 || len(m.filtered) != 0 {
		t.Fatal("empty snapshot retained scopes")
	}
	m, _ = fleetUpdate(m, fleetResultMsg{snapshot: hierarchySnapshot(), at: time.Now()})
	if m.currentNode() == nil || m.currentInstance() == nil {
		t.Fatal("did not recover from empty snapshot")
	}
}
