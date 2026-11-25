package tui

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func approvalOrder(requests []egress.Entry) []egress.Entry {
	return slices.SortedFunc(slices.Values(requests), func(a, b egress.Entry) int {
		return cmp.Or(cmp.Compare(a.Info.Profile, b.Info.Profile), cmp.Compare(a.Info.Session, b.Info.Session),
			cmp.Compare(a.Request.Machine, b.Request.Machine), a.Request.Deadline.Compare(b.Request.Deadline), cmp.Compare(a.Request.ID, b.Request.ID))
	})
}

func approvalName(r egress.Request) (name, source string) {
	if r.HTTPHost != "" {
		return monitorText(r.HTTPHost), "HTTP Host"
	}
	if r.Hint != "" {
		return monitorText(r.Hint), "TLS SNI"
	}
	return "", ""
}

func (m networkModel) approvalLeaves(now time.Time) []networkLeaf {
	bySession := make(map[string]string)
	for _, meta := range m.timeline.Machines {
		if meta.Info.Session != "" {
			bySession[meta.Info.Session] = meta.Machine
		}
	}
	var leaves []networkLeaf
	for i := range m.requests {
		e := &m.requests[i]
		r := e.Request
		if !now.Before(r.Deadline) {
			continue
		}
		seconds := (r.Deadline.Sub(now) + time.Second - 1) / time.Second
		status := fmt.Sprintf("%ds", seconds)
		target := monitorText(r.Target)
		label := target
		if name, source := approvalName(r); name != "" {
			room := m.width - ansi.StringWidth("* · "+target+status) - 8
			if room > 0 {
				if hint := name + " (" + source + ")"; ansi.StringWidth(hint) <= room {
					name = hint
				}
				label = ansi.Truncate(name, room, "…") + "* · " + target
			}
		}
		name := r.Machine
		if vm := bySession[e.Info.Session]; vm != "" {
			name = vm
		}
		leaves = append(leaves, networkLeaf{id: networkKeyID{request: e.Key()}, request: e, row: monitorRow{
			profile: e.Info.Profile, vm: e.Info.Session + "/" + r.Machine,
			vmTitle: monitorMachine(e.Info.Project, name, e.Info.Agent, m.width-2),
			label:   label, status: status, warning: true,
		}})
	}
	return leaves
}

func (m networkModel) approvalDetails(e egress.Entry) []string {
	target := monitorText(e.Request.Target)
	lines := []string{monitorTitle.Render("TCP approval")}
	if ansi.StringWidth("TCP destination: "+target) <= m.width {
		lines = append(lines, "TCP destination: "+target)
	} else {
		lines = append(lines, "TCP destination:", target)
	}
	lines = append(lines, "Project: "+monitorKnown(e.Info.Project), "Profile: "+monitorKnown(e.Info.Profile)+" · VM launch: "+monitorText(e.Request.Machine))
	if name, source := approvalName(e.Request); name != "" {
		lines = append(lines, source+" (unverified): "+name)
	}
	return append(lines, monitorMuted.Render("Session: this VM run and exact IP/port only."))
}
