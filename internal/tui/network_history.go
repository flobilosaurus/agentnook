package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

// Group profiles without changing the archive's newest-first order inside them.
func historyOrder(machines []egress.MachineHistory) []egress.MachineHistory {
	return slices.SortedStableFunc(slices.Values(machines), func(a, b egress.MachineHistory) int {
		return cmp.Compare(a.Info.Profile, b.Info.Profile)
	})
}

const historyTreeBudget = 6 // Profile/VM headings, including the range indicator.

func (m networkModel) pageLimit() int {
	const overhead = 5 + 1 + historyTreeBudget + 9 // Tab header, filter, tree, inspector/footer.
	return max(1, min(200, m.height-overhead))
}

func (m networkModel) hasCurrentHistoryPage() bool {
	page := m.survey.History.Page
	// Don't advance using the cursor of a page that no longer fits after resize.
	return page.Machine == m.machine && page.Before == m.before && len(page.Events) <= m.pageLimit()
}

func historyRowCost(a, b egress.MachineHistory) int {
	if a.Info.Profile != b.Info.Profile {
		return 2
	}
	return 1
}

func (m networkModel) historyEvents() []string {
	page := m.survey.History.Page
	if !m.hasCurrentHistoryPage() {
		return []string{"Loading machine history…"}
	}
	if len(page.Events) == 0 {
		return []string{"No recorded events (not proof of no traffic)."}
	}
	lines := make([]string, 0, len(page.Events))
	for _, event := range page.Events {
		lines = append(lines, fmt.Sprintf("%s %s %s · %s · %s", strings.ToUpper(fleetText(event.Decision)),
			strings.ToUpper(fleetText(event.Protocol)), fleetText(event.Target),
			event.Time.Local().Format(time.DateTime), fleetText(event.Reason)))
	}
	return lines
}

func (m networkModel) historyTree(machines []egress.MachineHistory, index int) []string {
	start, end := networkTreeWindow(machines, index, historyTreeBudget, 2, historyRowCost)
	var lines []string
	if start > 0 || end < len(machines) {
		start, end = networkTreeWindow(machines, index, historyTreeBudget-1, 2, historyRowCost)
		lines = append(lines, fmt.Sprintf("VMs %d–%d of %d", start+1, end, len(machines)))
	}
	for p := 0; p < end; {
		pEnd := p + 1
		for pEnd < len(machines) && machines[pEnd].Info.Profile == machines[p].Info.Profile {
			pEnd++
		}
		if pEnd > start {
			unit := "VMs"
			if pEnd-p == 1 {
				unit = "VM"
			}
			line, indent := networkProfileBranch(machines[p].Info.Profile, fmt.Sprintf("%d %s", pEnd-p, unit), pEnd >= end)
			lines = append(lines, line)
			for i := max(p, start); i < min(pEnd, end); i++ {
				meta := machines[i]
				branch, nested := networkBranch(i+1 == min(pEnd, end))
				name := fleetText(meta.Machine)
				if m.running[meta.Machine] {
					name = selected.Render("LIVE " + name)
				}
				marker := "  "
				if i == index {
					marker = "▸ "
				}
				lines = append(lines, fmt.Sprintf("%s%s%sVM %s · %s · %d events", indent, branch, marker,
					fleetText(unknown(meta.Info.Agent)), name, meta.Count))
				if i == index {
					events := m.historyEvents()
					for j, event := range events {
						leaf, _ := networkBranch(j+1 == len(events))
						lines = append(lines, indent+nested+leaf+event)
					}
				}
			}
		}
		p = pEnd
	}
	return lines
}

func (m networkModel) historyView() []string {
	heading := "Running VMs · f show all"
	if m.showAll {
		heading = "All VMs · f show running only"
	} else if m.runtimeErr != nil {
		heading = "VM status unavailable · f show all"
	}
	status := fleetText(m.status)
	if status == "" && m.runtimeErr != nil {
		status = "VM status unavailable: " + fleetText(m.runtimeErr.Error())
	}
	lines := []string{heading}
	machines := m.visibleMachines()
	index := m.machineIndex(machines)
	if len(machines) > 0 {
		lines = append(lines, m.historyTree(machines, index)...)
	}
	if index < 0 {
		message := "Select a VM with ↑↓/jk."
		switch {
		case !m.showAll && m.runtimeErr != nil:
			message = "Press f to browse stored history."
		case !m.showAll && m.running == nil:
			message = "Checking VM status…"
		case len(m.survey.History.Machines) == 0:
			message = "No VM history yet. History starts with new nook launches."
		case len(machines) == 0:
			message = "No running VMs with history."
		}
		return append(lines, message, "", "full: denials only; allowed traffic is NOT captured.", status, "Tab approvals · q close view")
	}
	meta := machines[index]
	coverage := "ask: engine decisions (TCP/DNS/UDP/ICMP when reported)"
	if meta.Coverage == "denials-only" {
		coverage = "full: DENIALS ONLY — allowed traffic is NOT captured"
	}
	end := "not finalized (running or abruptly interrupted)"
	if !meta.Ended.IsZero() {
		end = meta.Ended.Local().Format(time.RFC3339)
	}
	tail := []string{"", "Project: " + fleetText(meta.Info.Project), coverage, "End: " + end}
	if meta.Warning != "" {
		tail = append(tail, "Audit warning: "+fleetText(meta.Warning))
	}
	tail = append(tail, status, "↑↓/jk VM · PgUp/PgDn pages · Home latest", "Tab approvals · q quit", "ALLOWED = policy permit, NOT connection success.")
	return append(lines, tail...)
}
