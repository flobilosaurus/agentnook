package cli

import (
	"fmt"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/images"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

const imagesStatusUsage = `Usage:
  nook images status   Show managed images, configured tools and saved project paths

Shows configured environment variable names in saved project selections (no values).
Reads the local catalog without starting a VM or changing configuration.
Terminal input and output enable the interactive view; otherwise prints text.
Keys: up/down or j/k select a row, Enter toggles an image branch,
      Left returns from a path to its image, PgUp/PgDn scroll details,
      ? toggles the footer legend, q exits.
`

func (a App) imagesStatusCommand(args []string) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(a.Out, imagesStatusUsage)
		return 0
	}
	if len(args) != 0 {
		return a.fail(2, fmt.Errorf("images status accepts no arguments; use nook images status --help"))
	}
	p, err := paths.Resolve()
	if err != nil {
		return a.fail(1, err)
	}
	c, err := config.Load(p.ConfigFile())
	if err != nil {
		return a.fail(1, fmt.Errorf("config: %w", err))
	}
	status := images.DescribeStatus(c, p.ConfigFile(), currentProjectPath(), a.Agents)
	if a.Interactive() {
		err = tui.ShowConfigStatus(status, a.In, a.Out)
	} else {
		err = status.WriteText(a.Out)
	}
	if err != nil {
		return a.fail(1, fmt.Errorf("images status: %w", err))
	}
	return 0
}
