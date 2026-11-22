package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flobilosaurus/agentnook/internal/fleet"
)

// Groups are always-open, non-selectable headings. Only VM leaves can own the
// selection; the inspector follows that same VM, regardless of keyboard focus.
type fleetNode struct {
	key, parent, kind, label, path, profile string
	depth                                   int
	indices                                 []int
}

func fleetProject(item fleet.Instance) string {
	if item.Workspace.Repository != "" {
		return item.Workspace.Repository
	}
	return item.Project
}

func fleetNavigator(instances []fleet.Instance) []fleetNode {
	profiles := make(map[string]map[string][]int)
	for i, item := range instances {
		if profiles[item.Profile] == nil {
			profiles[item.Profile] = make(map[string][]int)
		}
		root := fleetProject(item)
		profiles[item.Profile][root] = append(profiles[item.Profile][root], i)
	}
	var nodes []fleetNode
	appendVMs := func(indices []int, parent string, depth int) {
		for _, i := range indices {
			item := instances[i]
			nodes = append(nodes, fleetNode{
				key: "v\x00" + item.Name, parent: parent, kind: "VM",
				label: strings.TrimPrefix(item.Name, "nook-run-"), path: item.Project,
				profile: item.Profile, depth: depth, indices: []int{i},
			})
		}
	}
	for _, profile := range fleetSortedKeys(profiles) {
		p := fleetNode{key: "p\x00" + profile, kind: "Profile", label: profile, profile: profile}
		groups := profiles[profile]
		for _, path := range fleetSortedKeys(groups) {
			p.indices = append(p.indices, groups[path]...)
		}
		sort.Ints(p.indices)
		nodes = append(nodes, p)
		labels := make(map[string]int)
		for root := range groups {
			labels[filepath.Base(root)]++
		}
		for _, root := range fleetSortedKeys(groups) {
			label := filepath.Base(root)
			if labels[label] > 1 {
				label += " · " + filepath.Dir(root)
			}
			r := fleetNode{key: "r\x00" + profile + "\x00" + root, parent: p.key, kind: "Project", label: label, path: root, profile: profile, depth: 1, indices: groups[root]}
			nodes = append(nodes, r)
			workspaces := make(map[string][]int)
			var main []int
			for _, i := range r.indices {
				if instances[i].Project == root {
					main = append(main, i)
				} else {
					workspaces[instances[i].Project] = append(workspaces[instances[i].Project], i)
				}
			}
			appendVMs(main, r.key, 2)
			for _, path := range fleetSortedKeys(workspaces) {
				item := instances[workspaces[path][0]]
				label, kind := filepath.Base(path), "Workspace"
				if path == item.Workspace.Checkout {
					kind = "Worktree"
					if item.Workspace.Branch != "" {
						label = item.Workspace.Branch
					}
				} else if item.Workspace.Checkout != "" {
					if rel, err := filepath.Rel(item.Workspace.Checkout, path); err == nil {
						label = rel + " (folder)"
					}
				}
				w := fleetNode{key: "w\x00" + profile + "\x00" + path, parent: r.key, kind: kind, label: label, path: path, profile: profile, depth: 2, indices: workspaces[path]}
				nodes = append(nodes, w)
				appendVMs(w.indices, w.key, 3)
			}
		}
	}
	return nodes
}

func fleetSortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func fleetCounts(instances []fleet.Instance, indices []int) string {
	counts := map[string]int{}
	for _, i := range indices {
		counts[instances[i].Activity.State()]++
	}
	parts := []string{fmt.Sprintf("%d VM", len(indices))}
	for _, state := range []string{"blocked", "unknown"} {
		if counts[state] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[state], state))
		}
	}
	return strings.Join(parts, " · ")
}

func (m fleetModel) currentNode() *fleetNode {
	if len(m.nodes) == 0 {
		return nil
	}
	return &m.nodes[m.navCursor]
}

func (m fleetModel) currentInstance() *fleet.Instance {
	if len(m.filtered) == 0 {
		return nil
	}
	return &m.snapshot.Instances[m.cursor]
}

// Preserve the VM across reorderings and Git enrichment. After exit choose
// another VM in its exact workspace, repository, profile, then the whole fleet.
// Headings are never a fallback selection, even when their last VM exits.
func (m *fleetModel) rebuildNavigator(previousVM, previousWorkspace string) {
	profile, repository := "", ""
	if n := m.currentNode(); n != nil {
		profile = n.profile
		for _, group := range m.nodes {
			if group.kind == "Project" && group.profile == profile {
				for _, i := range group.indices {
					if len(n.indices) == 1 && i == n.indices[0] {
						repository = group.path
					}
				}
			}
		}
	}
	m.nodes = fleetNavigator(m.snapshot.Instances)
	m.navCursor, m.cursor, m.filtered = 0, 0, nil
	best, score := -1, 5
	for index, n := range m.nodes {
		if n.kind != "VM" {
			continue
		}
		item := m.snapshot.Instances[n.indices[0]]
		rank := 4
		switch {
		case item.Name == previousVM:
			rank = 0
		case item.Profile == profile && item.Project == previousWorkspace:
			rank = 1
		case item.Profile == profile && fleetProject(item) == repository:
			rank = 2
		case item.Profile == profile:
			rank = 3
		}
		if rank < score {
			best, score = index, rank
		}
	}
	m.selectVMNode(best)
}

func (m *fleetModel) selectVMNode(index int) {
	if index < 0 || index >= len(m.nodes) || m.nodes[index].kind != "VM" {
		return
	}
	n := m.nodes[index]
	m.navCursor, m.cursor = index, n.indices[0]
	m.filtered = nil
	for i, item := range m.snapshot.Instances {
		if item.Profile == n.profile && item.Project == n.path {
			m.filtered = append(m.filtered, i)
		}
	}
}

func (m *fleetModel) moveVM(delta int) {
	for next := m.navCursor + delta; next >= 0 && next < len(m.nodes); next += delta {
		if m.nodes[next].kind == "VM" {
			m.selectVMNode(next)
			return
		}
	}
}
