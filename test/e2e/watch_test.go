package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWatchHelpAndNonterminalNeverLaunchOrWrite(t *testing.T) {
	s := newSandbox(t)
	out, stderr, code := s.run(t, s.project, "", 0, "watch", "--help")
	if code != 0 || stderr != "" || !strings.Contains(out, "Usage: nook watch\n") || strings.Contains(out, "--all") || !strings.Contains(out, "current AGENTNOOK_HOME") || !strings.Contains(out, "agent activity, runtime uptime and heartbeat") || !strings.Contains(out, "Missing or stale agent telemetry remains unknown") {
		t.Fatalf("help: %d %q %q", code, out, stderr)
	}
	for _, value := range []string{"One list grouped by profile", "only VM rows are selectable", "network mode and mount count", "details sit beside the list", "skipping profile headings", "Read-only", "1 VMs", "2 Approvals", "3 Audit", "4 Images"} {
		if !strings.Contains(out, value) {
			t.Fatalf("missing watch help %q: %s", value, out)
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

func TestWatchTerminalTabsWithoutRuntime(t *testing.T) {
	s := newSandbox(t)
	path := t.TempDir()
	start := func(args ...string) *testTerminal {
		cmd := exec.Command(launcher, args...)
		cmd.Dir = s.project
		cmd.Env = []string{"HOME=" + s.home, "AGENTNOOK_HOME=" + s.data, "PATH=" + path}
		return startTestTerminal(t, cmd)
	}
	const timeout = 8 * time.Second
	view := start("watch")
	view.expect(t, timeout, `1 VMs +2 Approvals +3 Audit +4 Images`)
	view.expect(t, timeout, `━{9}─`)
	view.expect(t, timeout, `Discovery unavailable`)
	view.send(t, "2")
	view.expect(t, timeout, `─{10}━{15}─`)
	view.expect(t, timeout, `No pending connections`)
	view.send(t, "3")
	view.expect(t, timeout, `Audit · read-only`)
	view.expect(t, timeout, `No recorded events`)
	view.send(t, "4")
	view.expect(t, timeout, `─{38}━{12}─`)
	view.expect(t, timeout, `No managed builds`)
	view.send(t, "q")
	view.wait(t)

	view = start("images", "status")
	view.expect(t, timeout, `─{38}━{12}─`)
	view.expect(t, timeout, `No managed builds`)
	view.send(t, "q")
	view.wait(t)
	if _, err := os.Stat(s.data); !os.IsNotExist(err) {
		t.Fatalf("watch changed data home: %v", err)
	}
}
