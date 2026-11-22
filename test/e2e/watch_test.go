package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestWatchHelpAndNonterminalNeverLaunchOrWrite(t *testing.T) {
	s := newSandbox(t)
	out, stderr, code := s.run(t, s.project, "", 0, "watch", "--help")
	if code != 0 || stderr != "" || !strings.Contains(out, "Usage: nook watch") || !strings.Contains(out, "agent activity, runtime uptime and heartbeat") || !strings.Contains(out, "Missing or stale agent telemetry remains unknown") {
		t.Fatalf("help: %d %q %q", code, out, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0, "watch")
	if code != 1 || !strings.Contains(stderr, "watch needs terminal input and output") {
		t.Fatalf("nonterminal: %d %q", code, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0, "watch", "--profile", "work")
	if code != 2 || !strings.Contains(stderr, "watch accepts no launch options") {
		t.Fatalf("arguments: %d %q", code, stderr)
	}
	for _, path := range []string{s.data, s.capture, s.capture + ".build"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("watch created storage or ran smolvm: %s %v", path, err)
		}
	}
}
