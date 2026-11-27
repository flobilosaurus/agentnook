package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func (m networkModel) historyLeaves() []networkLeaf {
	records := slices.SortedStableFunc(slices.Values(m.timeline.Records), func(a, b egress.HistoryRecord) int {
		return cmp.Or(cmp.Compare(a.Machine.Info.Profile, b.Machine.Info.Profile), cmp.Compare(a.Machine.Machine, b.Machine.Machine))
	})
	leaves := make([]networkLeaf, len(records))
	for i := range records {
		record := &records[i]
		vm, event := record.Machine, record.Event
		state := ""
		if m.running[vm.Machine] {
			state = " · live"
		} else if !vm.Ended.IsZero() {
			state = " · ended"
		}
		title := monitorMachine(vm.Info.Project, vm.Machine, vm.Info.Agent, m.width-2-ansi.StringWidth(state)) + state
		leaves[i] = networkLeaf{id: networkKeyID{machine: vm.Machine, seq: event.Sequence}, event: record, row: monitorRow{
			profile: vm.Info.Profile, vm: vm.Machine, vmTitle: title,
			label:  monitorKnown(event.Target) + " · " + strings.ToUpper(monitorKnown(event.Protocol)) + " · " + event.Time.Local().Format("15:04:05"),
			status: strings.ToUpper(monitorKnown(event.Decision)),
		}}
	}
	return leaves
}

func (m networkModel) eventList(history []networkLeaf, index, budget int) []string {
	if len(history) > 0 {
		return monitorList(leafRows(history), index, m.width, budget)
	}
	if !m.historyReady {
		return []string{"Loading history…"}
	}
	if m.historyErr != nil {
		return []string{"History unavailable."}
	}
	return []string{"No recorded events; traffic may still occur."}
}

func (m networkModel) networkHeading() []string {
	var lines []string
	if m.archive {
		lines = append(lines, monitorTitle.Render("Audit · read-only"))
		filter := "All VMs · f running only"
		if m.machine != "" {
			filter = monitorText(m.machine)
		} else if m.runningOnly {
			filter = "Running VMs · f show all"
		}
		lines = append(lines, fmt.Sprintf("%d events · %s", len(m.timeline.Records), filter))
	} else {
		pending := fmt.Sprintf("%d pending", len(m.approvalLeaves(time.Now())))
		if len(m.requests) > 0 {
			pending = monitorWarning.Render(pending)
		}
		lines = append(lines, pending+fmt.Sprintf(" · %d VM histories", len(m.timeline.Machines)))
	}
	if m.archive && m.runtimeErr != nil {
		lines = append(lines, monitorWarning.Render("Live VM status unavailable. Stored events remain readable."))
	} else if !m.archive && m.liveProblem != "" {
		lines = append(lines, monitorWarning.Render("Live approvals unavailable: "+monitorText(m.liveProblem)))
	}
	if m.historyErr != nil {
		lines = append(lines, monitorWarning.Render("History: "+monitorText(m.historyErr.Error())))
	} else if m.archive && m.machine == "" {
		warnings := 0
		for _, vm := range m.timeline.Machines {
			if vm.Warning != "" && (!m.runningOnly || m.running[vm.Machine]) {
				warnings++
			}
		}
		if warnings > 0 {
			lines = append(lines, monitorWarning.Render(fmt.Sprintf("%d VM histories have audit warnings", warnings)))
		}
	}
	return lines
}

func (m networkModel) networkDetails(pending, history []networkLeaf, pi, hi int) []string {
	if pi >= 0 {
		return m.approvalDetails(*pending[pi].request)
	}
	if hi >= 0 {
		return m.eventDetails(*history[hi].event)
	}
	if m.archive && m.machine != "" && m.historyReady && m.historyErr == nil {
		for _, vm := range m.timeline.Machines {
			if vm.Machine == m.machine {
				return historySummary(vm)
			}
		}
	}
	if !m.archive {
		if m.decision != "" {
			return []string{monitorMuted.Render(monitorText(m.decision))}
		}
		return []string{monitorMuted.Render("Select a request or event.")}
	}
	return nil
}

func historySummary(vm egress.MachineHistory) []string {
	lines := []string{"Project: " + monitorKnown(vm.Info.Project), "Profile: " + monitorKnown(vm.Info.Profile)}
	if vm.Coverage == "denials-only" {
		lines = append(lines, "Network: full · only denials are recorded")
	}
	if vm.Warning != "" {
		lines = append(lines, monitorWarning.Render("Audit warning: "+monitorText(vm.Warning)))
	}
	return lines
}

func (m networkModel) eventDetails(record egress.HistoryRecord) []string {
	vm, event := record.Machine, record.Event
	lines := []string{monitorTitle.Render(strings.ToUpper(monitorKnown(event.Decision)) + " · " + strings.ToUpper(monitorKnown(event.Protocol))),
		"Destination: " + monitorKnown(event.Target),
		"Project: " + monitorKnown(vm.Info.Project),
		"Profile: " + monitorKnown(vm.Info.Profile) + " · " + monitorKnown(vm.Machine),
		"Reason: " + monitorKnown(event.Reason) + " · " + event.Time.Local().Format(time.DateTime),
	}
	if vm.Coverage == "denials-only" {
		lines = append(lines, "Network: full · only denials are recorded")
	} else if event.Decision == "allowed" {
		lines = append(lines, "Allowed by policy; connection may still fail.")
	}
	if vm.Warning != "" {
		lines = append(lines, monitorWarning.Render("Audit warning: "+monitorText(vm.Warning)))
	}
	return lines
}

func (m networkModel) networkFooter(requestSelected bool) []string {
	var hints []string
	if m.archive {
		hints = append(hints, "PgUp older · PgDn newer · Home latest")
		if m.machine != "" {
			hints = append(hints, "↑↓/jk select · x show all VMs")
		} else {
			hints = append(hints, "↑↓/jk select · f toggle running only")
		}
	} else {
		if requestSelected {
			hints = append(hints, "a allow once · s allow for VM run · d deny")
		}
		hints = append(hints, "* Unverified name · 12s timeout denies TCP", "↑↓/jk select request or recent decision")
	}
	for i := range hints {
		hints[i] = monitorMuted.Render(ansi.Truncate(hints[i], m.width, "…"))
	}
	return hints
}
