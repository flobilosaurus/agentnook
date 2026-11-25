package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func TestNetworkOverviewAndGroupedHistoryInTerminal(t *testing.T) {
	s := newSandbox(t)
	for _, profile := range []string{"work", "personal"} {
		_, stderr, code := s.run(t, s.project, "network-denial", 0, "--agent", "pi", "--profile", profile, "--agent-version", "1.4.0")
		if code != 0 {
			t.Fatal(code, stderr)
		}
	}
	startView := func(args ...string) *testTerminal {
		cmd := exec.Command(launcher, append([]string{"network"}, args...)...)
		cmd.Dir = s.project
		cmd.Env = []string{"HOME=" + s.home, "AGENTNOOK_HOME=" + s.data, "PATH=" + stubBin, "AGENTNOOK_TEST_CAPTURE=" + s.capture}
		return startTestTerminal(t, cmd)
	}
	view := startView()
	view.expect(t, 8*time.Second, `Pending requests`)
	view.expect(t, 5*time.Second, `Recent decisions`)
	view.expect(t, 5*time.Second, `private\.example · DNS`)
	view.send(t, "h")
	view.expect(t, 5*time.Second, `Network History · read only`)
	view.expect(t, 5*time.Second, `4 events · All VMs`)
	view.expect(t, 5*time.Second, `Profile personal`)
	view.expect(t, 5*time.Second, `Profile work`)
	view.expect(t, 5*time.Second, ` · VM [a-z0-9]{10} · pi · ended`)
	view.expect(t, 5*time.Second, `▸ private\.example · DNS.*DENIED`)
	view.send(t, "j")
	view.expect(t, 5*time.Second, `▸ 100\.96\.0\.1:1455 · TCP.*DENIED`)
	view.send(t, "q")
	view.wait(t)

	archive, err := egress.ReadHistory(s.data, egress.HistoryQuery{})
	if err != nil || len(archive.Machines) != 2 {
		t.Fatal(archive.Machines, err)
	}
	view = startView("--machine", archive.Machines[0].Machine)
	view.expect(t, 8*time.Second, `Network History · read only`)
	view.expect(t, 5*time.Second, `2 events · `+archive.Machines[0].Machine)
	view.send(t, "q")
	view.wait(t)
}

func TestNetworkHistorySurvivesMachineDeletionAndShowsFinalStopEvents(t *testing.T) {
	s := newSandbox(t)
	_, stderr, code := s.run(t, s.project, "network-denial", 0, "--agent", "pi", "--profile", "work", "--agent-version", "1.4.0")
	if code != 0 {
		t.Fatal(code, stderr)
	}
	out, stderr, code := s.run(t, s.project, "", 0, "network", "--json", "--limit", "1")
	if code != 0 {
		t.Fatal(code, stderr)
	}
	var survey egress.Survey
	if err := json.Unmarshal([]byte(out), &survey); err != nil {
		t.Fatal(err)
	}
	if len(survey.History.Machines) != 1 || survey.History.Machines[0].Ended.IsZero() || survey.History.Machines[0].Count != 2 || survey.History.Machines[0].Coverage != "denials-only" {
		t.Fatal(survey)
	}
	page := survey.History.Page
	if len(page.Events) != 1 || page.Events[0].Protocol != "dns" || page.Events[0].Decision != "denied" {
		t.Fatal(page)
	}
	out, stderr, code = s.run(t, s.project, "", 0, "network", "--json", "--machine", page.Machine, "--before", strconv.FormatInt(page.NextBefore, 10), "--limit", "1")
	if code != 0 || json.Unmarshal([]byte(out), &survey) != nil || survey.History.Page.Events[0].Protocol != "tcp" {
		t.Fatal(code, stderr, out)
	}
	for _, file := range []string{"machine.json", "events.jsonl"} {
		info, err := os.Stat(filepath.Join(s.data, "network", "history", page.Machine, file))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal(file, err)
		}
	}
}
