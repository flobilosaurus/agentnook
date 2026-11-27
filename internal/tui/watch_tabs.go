package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	watchTabAccent = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "117"})
	watchTabRule   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "240"})
)

func watchTabBadge(pending int) string {
	switch {
	case pending > 99:
		return " !99+"
	case pending > 0:
		return fmt.Sprintf(" !%d", pending)
	default:
		return ""
	}
}

func watchTabPadding(width int) string {
	if width >= 60 {
		return "  "
	}
	return " "
}

func renderWatchTabs(width int, active WatchTab, pending int) string {
	var labels, rule strings.Builder
	pad := watchTabPadding(width)
	for i, name := range watchTabs {
		if i > 0 {
			labels.WriteByte(' ')
			rule.WriteString(watchTabRule.Render("─"))
		}
		label := fmt.Sprintf("%d %s", i+1, name)
		badge := ""
		if WatchTab(i) == WatchApprovals {
			badge = watchTabBadge(pending)
		}
		length := ansi.StringWidth(label+badge) + len(pad)*2
		if WatchTab(i) == active {
			label = watchTabAccent.Bold(true).Render(label)
			rule.WriteString(watchTabAccent.Render(strings.Repeat("━", length)))
		} else {
			label = monitorMuted.Render(label)
			rule.WriteString(watchTabRule.Render(strings.Repeat("─", length)))
		}
		labels.WriteString(pad + label + monitorWarning.Render(badge) + pad)
	}
	if rest := width - ansi.StringWidth(rule.String()); rest > 0 {
		rule.WriteString(watchTabRule.Render(strings.Repeat("─", rest)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, strings.TrimRight(labels.String(), " "), rule.String())
}
