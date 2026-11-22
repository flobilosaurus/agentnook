package e2e

import (
	"strings"
	"testing"
)

// A real build catches npm lifecycle-policy failures that CLI stubs cannot.
func TestRealOpenCodeVM(t *testing.T) {
	_, run := realVMLauncher(t)
	run("images", "build", "opencode-smoke", "--agent", "opencode",
		"--agent-version", "2.3.2", "--base-image", "node:24-bookworm-slim", "--set-default")
	for _, args := range [][]string{
		{"--agent", "opencode", "--profile", "smoke", "--", "--version"},
		{"--", "--version"},
	} {
		output := run(args...)
		if !strings.Contains(output, "2.3.2") {
			t.Fatalf("unexpected OpenCode version: %s", output)
		}
	}
}
