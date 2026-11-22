package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

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
