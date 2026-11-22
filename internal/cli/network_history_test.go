package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func TestNetworkJSONReadsEndedVMWithoutRuntimeOrTerminal(t *testing.T) {
	f := setup(t)
	source := t.TempDir()
	machine := "nook-run-cli-history"
	finish, err := egress.ObserveDirectory(f.data, machine, "ask", egress.Info{Project: f.project, Agent: "pi", Profile: "work"}, source)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"timestampMs":1760000000000,"machineId":"abababababababababababababababab","transport":"tcp","destination":"203.0.113.1:443","action":"allow","reason":"broker_decision"}` + "\n"
	if err := os.WriteFile(filepath.Join(source, "egress-decisions.jsonl"), []byte(strings.Repeat(line, 3)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	f.app.Interactive = func() bool { return false }
	t.Setenv("PATH", "")
	f.run(t, 0, "network", "--json", "--machine", machine, "--limit", "1")
	var survey egress.Survey
	if err := json.Unmarshal(f.out.Bytes(), &survey); err != nil {
		t.Fatal(err)
	}
	if len(survey.History.Machines) != 1 || len(survey.History.Page.Events) != 1 || !survey.History.Page.HasOlder || survey.History.Machines[0].Ended.IsZero() {
		t.Fatal(survey)
	}
	cursor := survey.History.Page.NextBefore
	f.run(t, 0, "network", "--json", "--machine", machine, "--before", strconv.FormatInt(cursor, 10), "--limit", "1")
	if err := json.Unmarshal(f.out.Bytes(), &survey); err != nil || survey.History.Page.Events[0].Sequence != 2 {
		t.Fatal(survey, err)
	}
}

func TestNetworkJSONEmptyAndInvalidArgumentsAreReadOnly(t *testing.T) {
	for _, args := range [][]string{
		{"network", "--json"}, {"network", "--json", "--limit", "201"},
		{"network", "--json", "--before", "-1"}, {"network", "--json", "--before", "5"},
	} {
		f := setup(t)
		f.app.Interactive = func() bool { return false }
		want := 2
		if len(args) == 2 {
			want = 0
		}
		f.run(t, want, args...)
		assertNoData(t, f)
	}
}

func TestArchiveRemainsReadableWhenLiveRegistrationsAreUnavailable(t *testing.T) {
	f := setup(t)
	finish, err := egress.ObserveDirectory(f.data, "nook-run-stale-test", "full", egress.Info{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 257; i++ {
		if err := os.WriteFile(filepath.Join(f.data, "network", "stale-"+strconv.Itoa(i)+".json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.app.Interactive = func() bool { return false }
	f.run(t, 0, "network", "--json")
	var survey egress.Survey
	if err := json.Unmarshal(f.out.Bytes(), &survey); err != nil || len(survey.History.Machines) != 1 || survey.Warning == "" {
		t.Fatal(survey, err)
	}
}

func TestNetworkJSONDoesNotExposeBrokerTokens(t *testing.T) {
	f := setup(t)
	b, err := egress.Start(f.data, egress.Info{Project: f.project})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	files, _ := filepath.Glob(filepath.Join(f.data, "network", "*.json"))
	p, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var control map[string]string
	if err := json.Unmarshal(p, &control); err != nil {
		t.Fatal(err)
	}
	f.run(t, 0, "network", "--json")
	for _, token := range []string{b.Token(), control["token"]} {
		if token == "" || strings.Contains(f.out.String(), token) {
			t.Fatal("token exposed")
		}
	}
}
