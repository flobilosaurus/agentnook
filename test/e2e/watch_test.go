package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestWatchHelpAndNonterminalNeverLaunchOrWrite(t *testing.T) {
	s := newSandbox(t)
	out, stderr, code := s.run(t, s.project, "", 0, "watch", "--help")
	if code != 0 || stderr != "" || !strings.Contains(out, "Usage: nook watch\n") || strings.Contains(out, "--all") || !strings.Contains(out, "current AGENTNOOK_HOME") || !strings.Contains(out, "agent activity, runtime uptime and heartbeat") || !strings.Contains(out, "Missing or stale agent telemetry remains unknown") {
		t.Fatalf("help: %d %q %q", code, out, stderr)
	}
	for _, value := range []string{"Always-open navigator", "Only VM rows are selectable", "skipping all group headings", "Tab/Shift+Tab", "No collapsing", "read-only"} {
		if !strings.Contains(out, value) {
			t.Fatalf("missing navigator help %q: %s", value, out)
		}
	}
	_, stderr, code = s.run(t, s.project, "", 0, "watch")
	if code != 1 || !strings.Contains(stderr, "watch needs terminal input and output") {
		t.Fatalf("nonterminal: %d %q", code, stderr)
	}
	for _, args := range [][]string{{"watch", "--all"}, {"watch", "--all", "--help"}, {"watch", "--profile", "work"}} {
		_, stderr, code = s.run(t, s.project, "", 0, args...)
		if code != 2 || !strings.Contains(stderr, "watch accepts no launch options") {
			t.Fatalf("arguments %v: %d %q", args, code, stderr)
		}
	}
	for _, path := range []string{s.data, s.capture, s.capture + ".build"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("watch created storage or ran smolvm: %s %v", path, err)
		}
	}
}
