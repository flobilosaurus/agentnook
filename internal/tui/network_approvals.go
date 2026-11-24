package tui

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

type approvalVM struct{ session, machine string }

func approvalVMOf(e egress.Entry) approvalVM {
	return approvalVM{e.Info.Session, e.Request.Machine}
}

// Use the same leaf order for rendering and navigation. Comparing raw identity
// fields keeps distinct launches separate even if their labels look alike.
func approvalOrder(requests []egress.Entry) []egress.Entry {
	return slices.SortedFunc(slices.Values(requests), func(a, b egress.Entry) int {
		x, y := approvalVMOf(a), approvalVMOf(b)
		return cmp.Or(cmp.Compare(a.Info.Profile, b.Info.Profile), cmp.Compare(x.machine, y.machine),
			cmp.Compare(x.session, y.session), a.Request.Deadline.Compare(b.Request.Deadline), cmp.Compare(a.Request.ID, b.Request.ID))
	})
}

func approvalRowCost(a, b egress.Entry) int {
	if a.Info.Profile != b.Info.Profile {
		return 3 // profile, VM, request
	}
	if approvalVMOf(a) != approvalVMOf(b) {
		return 2
	}
	return 1
}

// Fit a contiguous set of leaves, keeping the selected leaf and its ancestors
// visible. A leaf at the top of the window always costs three rows.
func approvalWindow(requests []egress.Entry, index, budget int) (start, end int) {
	return networkTreeWindow(requests, index, budget, 3, approvalRowCost)
}

func approvalName(r egress.Request) (name, source string) {
	if r.HTTPHost != "" {
		return fleetText(r.HTTPHost), "HTTP Host"
	}
	if r.Hint != "" {
		return fleetText(r.Hint), "TLS SNI"
	}
	return "", ""
}

func approvalRequestLine(r egress.Request, prefix string, chosen bool, width int, now time.Time) string {
	marker := "  "
	if chosen {
		marker = "▸ "
	}
	prefix += marker
	remaining := max(0, r.Deadline.Sub(now))
	seconds := (remaining + time.Second - 1) / time.Second
	timeText := fmt.Sprintf("%ds", seconds)
	target := fleetText(r.Target)
	room := width - ansi.StringWidth(prefix+" · "+timeText)
	label := ansi.Truncate(target, room, "…")
	if name, source := approvalName(r); name != "" {
		nameRoom := room - ansi.StringWidth("* · "+target)
		if nameRoom > 0 {
			if hint := name + " (" + source + ")"; ansi.StringWidth(hint) <= nameRoom {
				name = hint
			}
			label = ansi.Truncate(name, nameRoom, "…") + "* · " + target
		}
	}
	if remaining > 0 && seconds <= 4 {
		timeText = errorStyle.Render(timeText)
	}
	return prefix + label + " · " + timeText
}

func approvalTree(requests []egress.Entry, index, width, start, end int, now time.Time) []string {
	var lines []string
	for p := 0; p < end; {
		pEnd := p + 1
		for pEnd < len(requests) && requests[pEnd].Info.Profile == requests[p].Info.Profile {
			pEnd++
		}
		if pEnd <= start {
			p = pEnd
			continue
		}
		line, indent := networkProfileBranch(requests[p].Info.Profile, fmt.Sprintf("%d pending", pEnd-p), pEnd >= end)
		lines = append(lines, line)
		for vm := p; vm < min(pEnd, end); {
			vmEnd := vm + 1
			for vmEnd < pEnd && approvalVMOf(requests[vmEnd]) == approvalVMOf(requests[vm]) {
				vmEnd++
			}
			if vmEnd > start {
				vmBranch, vmIndent := networkBranch(vmEnd >= min(pEnd, end))
				e := requests[vm]
				lines = append(lines, fmt.Sprintf("%s%sVM %s · %s · %d pending", indent, vmBranch,
					fleetText(e.Info.Agent), fleetText(e.Request.Machine), vmEnd-vm))
				for i := max(vm, start); i < min(vmEnd, end); i++ {
					leaf, _ := networkBranch(i+1 == min(vmEnd, end))
					lines = append(lines, approvalRequestLine(requests[i].Request, indent+vmIndent+leaf, i == index, width, now))
				}
			}
			vm = vmEnd
		}
		p = pEnd
	}
	return lines
}

func (m networkModel) approvalView(height int) []string {
	requests, index := m.survey.Requests, m.selectedIndex()
	details := []string{"", "No request selected. ↑↓/jk selects a request."}
	if index >= 0 {
		e := requests[index]
		target := fleetText(e.Request.Target)
		details = []string{"", "Project: " + fleetText(e.Info.Project), "VM launch: " + fleetText(e.Request.Machine)}
		if ansi.StringWidth("TCP destination: "+target) > m.width {
			details = append(details, "TCP destination:", target)
		} else {
			details = append(details, "TCP destination: "+target)
		}
		if name, source := approvalName(e.Request); name != "" {
			details = append(details, source+" (unverified): "+name)
		}
		details = append(details, "Session approval: this VM + exact IP/port only.")
	}
	footer := []string{"", fleetText(m.status), "a allow once · s allow IP/port for VM session", "d deny · Tab history · q quit",
		"* Unverified name · 12s timeout denies TCP"}
	if len(requests) == 0 {
		return append([]string{"No pending connections. Tab shows history."}, footer...)
	}
	budget := height - len(details) - len(footer)
	start, end := approvalWindow(requests, index, budget)
	var lines []string
	if start > 0 || end < len(requests) {
		start, end = approvalWindow(requests, index, budget-1)
		lines = append(lines, fmt.Sprintf("Requests %d–%d of %d", start+1, end, len(requests)))
	}
	lines = append(lines, approvalTree(requests, index, m.width, start, end, time.Now())...)
	lines = append(lines, details...)
	return append(lines, footer...)
}
