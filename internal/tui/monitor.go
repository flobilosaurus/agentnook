package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const selectionMarker = "▸ "

var (
	monitorTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	monitorProfile  = lipgloss.NewStyle().Bold(true)
	monitorMuted    = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	monitorWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("221"))
	monitorSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117")).Background(lipgloss.Color("236"))
)

type monitorRow struct {
	profile, vm, vmTitle     string
	label, secondary, status string
	warning                  bool
}

func monitorText(value string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return '?'
		}
		return r
	}, value)
}

func monitorKnown(value string) string {
	if value == "" {
		return "unknown"
	}
	return monitorText(value)
}

func monitorMachine(project, name, agent string, width int) string {
	if width <= 0 {
		return ""
	}
	if project != "" {
		project = filepath.Base(project)
	}
	id := strings.TrimPrefix(name, "nook-run-")
	if len(id) > 10 && len(id) == ansi.StringWidth(id) {
		id = id[:10]
	}
	suffix := " · VM " + monitorKnown(id)
	if agent != "" {
		suffix += " · " + monitorText(agent)
	}
	room := width - ansi.StringWidth(suffix)
	if room < 1 {
		return ansi.Truncate(strings.TrimPrefix(suffix, " · "), width, "…")
	}
	return ansi.Truncate(monitorKnown(project), room, "…") + suffix
}

func monitorView(lines []string, width int) string {
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return strings.Join(lines, "\n")
}

const monitorColumnGap = " │ "

// Keep enough room for the full TCP destination in the details column,
// including bracketed IPv6 addresses. Stack panes in narrower terminals.
func watchColumns(width int) (left, right int) {
	if width < 80 {
		return width, width
	}
	available := width - ansi.StringWidth(monitorColumnGap)
	left = min(available/2, available-48)
	return left, available - left
}

func monitorColumns(left, right []string, leftWidth, rightWidth, height int) []string {
	rows := min(height, max(len(left), len(right)))
	lines := make([]string, 0, rows)
	divider := monitorMuted.Render(monitorColumnGap)
	for i := 0; i < rows; i++ {
		l, r := "", ""
		if i < len(left) {
			l = ansi.Truncate(left[i], leftWidth, "…")
		}
		if i < len(right) {
			r = ansi.Truncate(right[i], rightWidth, "…")
		}
		lines = append(lines, l+strings.Repeat(" ", leftWidth-ansi.StringWidth(l))+divider+r)
	}
	return lines
}

func monitorRowCost(previous *monitorRow, item monitorRow) int {
	cost := 1
	if item.secondary != "" {
		cost++
	}
	changeProfile := previous == nil || previous.profile != item.profile
	if changeProfile {
		cost++
	}
	if item.vmTitle != "" && (changeProfile || previous.vm != item.vm) {
		cost++
	}
	return cost
}

// Keep a contiguous window of selectable rows, including the selected row and
// its headings when possible. Headings never change the navigation order.
func monitorWindow(rows []monitorRow, selected, budget int) (start, end, used int) {
	if len(rows) == 0 || budget <= 0 {
		return 0, 0, 0
	}
	start = min(len(rows)-1, max(0, selected))
	end = start + 1
	used = monitorRowCost(nil, rows[start])
	for start > 0 {
		cost := monitorRowCost(nil, rows[start-1]) + monitorRowCost(&rows[start-1], rows[start]) - monitorRowCost(nil, rows[start])
		if used+cost > budget {
			break
		}
		start--
		used += cost
	}
	for end < len(rows) {
		cost := monitorRowCost(&rows[end-1], rows[end])
		if used+cost > budget {
			break
		}
		end++
		used += cost
	}
	return start, end, used
}

func monitorRowLine(item monitorRow, selected bool, width int) string {
	indent := "  "
	if item.vmTitle != "" {
		indent += "  "
	}
	marker := "  "
	if selected {
		marker = selectionMarker
	}
	prefix := indent + marker
	status := ansi.Truncate(item.status, width/3, "…")
	room := width - ansi.StringWidth(prefix+status) - 2
	label := ansi.Truncate(item.label, max(0, room), "…")
	padding := strings.Repeat(" ", max(0, room+2-ansi.StringWidth(label)))
	if selected {
		return monitorSelected.Render(prefix + label + padding + status)
	}
	if item.warning {
		status = monitorWarning.Render(status)
	} else {
		status = monitorMuted.Render(status)
	}
	return prefix + label + padding + status
}

func monitorList(rows []monitorRow, selected, width, budget int) []string {
	start, end, used := monitorWindow(rows, selected, budget)
	if start == end {
		return nil
	}
	var lines []string
	if (start > 0 || end < len(rows)) && budget > used {
		lines = append(lines, monitorMuted.Render(fmt.Sprintf("Rows %d–%d of %d", start+1, end, len(rows))))
	}
	for i := start; i < end; i++ {
		item := rows[i]
		changeProfile := i == start || item.profile != rows[i-1].profile
		if changeProfile {
			lines = append(lines, monitorProfile.Render("Profile "+monitorKnown(item.profile)))
		}
		if item.vmTitle != "" && (changeProfile || item.vm != rows[i-1].vm) {
			lines = append(lines, "  "+monitorMuted.Render(item.vmTitle))
		}
		lines = append(lines, monitorRowLine(item, i == selected, width))
		if item.secondary != "" {
			lines = append(lines, "    "+monitorMuted.Render(item.secondary))
		}
	}
	return lines[:min(budget, len(lines))]
}
