package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/fleet"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

const watchUsage = `Usage: nook watch

Watch running agentnook instances of this exact host directory and AGENTNOOK_HOME.
Uses smolvm machine ls --json and --verbose; refreshes after each poll (1s).
Shows VM status, resource limits, image metadata and all mounts without tabs.
Shows agent activity, runtime uptime and heartbeat from shared guest telemetry.
Missing or stale agent telemetry remains unknown; VM status stays separate.

Keys: up/down or j/k select; PgUp/PgDn or Ctrl+U/Ctrl+D scroll details;
      Home/End jump within details; q, Esc or Ctrl+C exit watch only.
Requires terminal input and output. Never starts or explicitly stops a VM.
`

func (a App) watchCommand(args []string) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(a.Out, watchUsage)
		return 0
	}
	if len(args) != 0 {
		return a.fail(2, errors.New("watch accepts no launch options or agent arguments; use nook watch --help"))
	}
	if !a.Interactive() {
		return a.fail(1, errors.New("watch needs terminal input and output"))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return a.fail(1, err)
	}
	project, err := config.NormalizeProjectPath(cwd)
	if err != nil {
		return a.fail(1, err)
	}
	p, err := paths.Resolve()
	if err != nil {
		return a.fail(1, err)
	}
	binary, err := exec.LookPath("smolvm")
	if err != nil {
		return a.fail(127, errors.New("smolvm not found on PATH"))
	}
	source := fleet.Source{Binary: binary, Project: project, Paths: p, Agents: a.Agents}
	defer source.Close()
	watch := a.Watch
	if watch == nil {
		watch = func(project string, load tui.FleetLoader) error {
			return tui.WatchFleet(project, load, a.In, a.Out)
		}
	}
	if err := watch(project, source.Load); err != nil {
		return a.fail(1, fmt.Errorf("watch: %w", err))
	}
	return 0
}
