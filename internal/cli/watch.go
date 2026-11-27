package cli

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

const watchUsage = `Usage: nook watch

Watch running agentnook instances across all projects in the current AGENTNOOK_HOME.
The current directory does not restrict discovery; other data homes stay excluded.
One list grouped by profile. VM rows show project, network mode and mount count.
Only VM rows are selectable. Details for the selected VM appear below the list.
Git grouping is optional and read-only; unavailable metadata falls back to folders.
Uses smolvm machine ls --json and --verbose; refreshes after each poll (1s).
Shows VM status, launch limits, network mode, forwarded variable names, and mounts.
Shows agent activity, runtime uptime and heartbeat from shared guest telemetry.
Missing or stale agent telemetry remains unknown; VM status stays separate.

Keys: up/down or j/k select VMs, skipping profile headings.
      PgUp/PgDn or Ctrl+U/Ctrl+D scroll details; Home/End jump within details.
      q, Esc or Ctrl+C exit watch only.
Requires terminal input and output. Never starts or explicitly stops a VM.
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
	binary, err := exec.LookPath("smolvm")
	if err != nil {
		return a.fail(127, errors.New("smolvm not found on PATH"))
	}
	source := fleet.Source{Binary: binary, Paths: p, Agents: a.Agents}
	scope := "All projects · AGENTNOOK_HOME: " + p.Root
	defer source.Close()
	watch := a.Watch
	if watch == nil {
		watch = func(scope string, load tui.FleetLoader) error {
			return tui.WatchFleet(scope, load, a.In, a.Out)
		}
	}
	if err := watch(scope, source.Load); err != nil {
		return a.fail(1, fmt.Errorf("watch: %w", err))
	}
	return 0
}
