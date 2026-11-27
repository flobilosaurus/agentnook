package cli

import (
	"errors"
	"fmt"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/images"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

const watchUsage = `Usage: nook watch

Monitor all projects in the current AGENTNOOK_HOME. Other data homes stay excluded.

1 VMs        One list grouped by profile; only VM rows are selectable, skipping profile headings.
             Rows show project, network mode and mount count. On wide terminals,
             details sit beside the list; on narrow terminals they sit below.
             Shows agent activity, runtime uptime and heartbeat when available.
             Missing or stale agent telemetry remains unknown; VM status stays separate.
             PgUp/PgDn scroll details; Enter opens Audit for the selected VM.
2 Approvals  Pending TCP requests and recent decisions. Select a request before
             a (allow once), s (allow exact IP/port for this VM run), or d (deny).
             Unanswered requests are denied after 12 seconds.
3 Audit      Read-only history, including ended VMs. PgUp/PgDn page older/newer;
             f toggles running only; x clears a VM filter.
4 Images     Managed images, tools and saved project assignments. r refreshes.

Keys: 1–4, Tab/Shift+Tab or [/] switch tabs; up/down or j/k select rows.
      q, Esc or Ctrl+C exit watch only.
Requires terminal input and output. Approvals and Audit need at least 48×27.
Audit and Images remain available without smolvm. Watch never starts or stops a VM.
`

func (a App) watchCommand(args []string) int {
	help := false
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			help = true
		default:
			return a.fail(2, errors.New("watch accepts no launch options or agent arguments; use nook watch --help"))
		}
	}
	if help {
		fmt.Fprint(a.Out, watchUsage)
		return 0
	}
	if !a.Interactive() {
		return a.fail(1, errors.New("watch needs terminal input and output"))
	}
	p, err := paths.Resolve()
	if err != nil {
		return a.fail(1, err)
	}
	if err := a.runWatch(p, tui.WatchVMs, egress.HistoryQuery{}); err != nil {
		return a.fail(1, fmt.Errorf("watch: %w", err))
	}
	return 0
}

func (a App) runWatch(p paths.Paths, start tui.WatchTab, history egress.HistoryQuery) error {
	source := fleet.Source{Paths: p, Agents: a.Agents}
	defer source.Close()
	project := currentProjectPath()
	options := tui.WatchOptions{Root: p.Root, Start: start, History: history, LoadFleet: source.Load,
		LoadImages: func() (images.CatalogStatus, error) { return a.loadImageStatus(p, project) }}
	if a.Watch != nil {
		return a.Watch(options)
	}
	return tui.Watch(options, a.In, a.Out)
}
