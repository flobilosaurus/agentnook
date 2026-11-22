package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/tui"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

func TestNetworkModeSavedPerAgentAndNotPartOfImage(t *testing.T) {
	f := setup(t)
	t.Setenv("AGENTNOOK_TEST_SMOLVM_VERSION", "smolvm 1.36.1")
	var token string
	f.app.Runner = func(_ string, args []string, streams vm.IO) int {
		f.runs++
		f.argv = append([]string(nil), args...)
		if !strings.Contains(strings.Join(args, " "), "--mediated-egress") || streams.Mediation == nil {
			t.Fatal("missing mediation", args)
		}
		if token != "" && token == streams.Mediation.Token {
			t.Fatal("token reused across launches")
		}
		token = streams.Mediation.Token
		if strings.Contains(strings.Join(args, " "), token) {
			t.Fatal("token in guest arguments")
		}
		s, err := egress.Load(f.data)
		if err != nil || s.Sessions != 1 {
			t.Fatal(s, err)
		}
		return 0
	}
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--network", "ask")
	if f.load(t).Projects[f.project].Agents["pi"].Network != "ask" {
		t.Fatal("network mode not saved")
	}
	f.run(t, 0) // Saved mode still launches a fresh broker.
	if s, err := egress.Load(f.data); err != nil || s.Sessions != 0 {
		t.Fatal("broker leaked", s, err)
	}
	f.app.Runner = func(_ string, args []string, streams vm.IO) int {
		if streams.Mediation != nil || strings.Contains(strings.Join(args, " "), "--mediated-egress") {
			t.Fatal("unexpected network policy", args)
		}
		return 0
	}
	f.run(t, 0, "--agent", "claude", "--profile", "work")
	w := f.load(t).Projects[f.project]
	if w.Agents["claude"].Network != "" || w.Agents["pi"].Network != "ask" {
		t.Fatal("policy leaked across agents", w)
	}
	f.run(t, 0, "--agent", "pi", "--network", "full")
	if f.load(t).Projects[f.project].Agents["pi"].Network != "full" {
		t.Fatal("full mode not saved")
	}
}

func TestNetworkDryRunReadOnlyAndVersionGate(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "network-image")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	t.Setenv("PATH", "")
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--image", entry.Name, "--network", "ask", "--dry-run")
	var args []string
	if err := json.Unmarshal(f.out.Bytes(), &args); err != nil || !strings.Contains(strings.Join(args, " "), "--mediated-egress") {
		t.Fatal(args, err)
	}
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if string(before) != string(after) || f.runs != 0 {
		t.Fatal("dry run changed state")
	}
	if _, err := os.Stat(filepath.Join(f.data, "network")); !os.IsNotExist(err) {
		t.Fatal("dry run started broker")
	}
	f = setup(t)
	t.Setenv("AGENTNOOK_TEST_SMOLVM_VERSION", "smolvm 1.35.4")
	f.run(t, 127, "--agent", "pi", "--profile", "work", "--network", "ask")
	if !strings.Contains(f.errOut.String(), "1.36.1") {
		t.Fatal(f.errOut.String())
	}
	assertNoData(t, f)
}

func TestNetworkInvalidModesAndBrokerFailure(t *testing.T) {
	for _, mode := range []string{"", "all", "ASK", "off"} {
		f := setup(t)
		f.run(t, 2, "--network", mode)
		assertNoData(t, f)
	}
	f := setup(t)
	t.Setenv("AGENTNOOK_TEST_SMOLVM_VERSION", "smolvm 1.36.1")
	if err := os.MkdirAll(f.data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(f.data, "network")); err != nil {
		t.Fatal(err)
	}
	f.run(t, 1, "--agent", "pi", "--profile", "work", "--network", "ask")
	if f.runs != 0 || !strings.Contains(f.errOut.String(), "network broker") {
		t.Fatal("failed open", f.runs, f.errOut.String())
	}
}

func TestNetworkCommandSeparateTerminalAndScope(t *testing.T) {
	f := setup(t)
	f.app.Interactive = func() bool { return false }
	f.run(t, 0, "network", "--help")
	f.run(t, 1, "network")
	f.run(t, 2, "network", "--all")
	assertNoData(t, f)
	f.app.Interactive = func() bool { return true }
	calls := 0
	f.app.Network = func(root string, load tui.NetworkLoader, decide tui.NetworkDecider) error {
		calls++
		if root != f.data || decide == nil {
			t.Fatal(root)
		}
		s, err := load()
		if err != nil || s.Sessions != 0 || len(s.Requests) != 0 {
			t.Fatal(s, err)
		}
		return nil
	}
	f.run(t, 0, "network")
	if calls != 1 {
		t.Fatal(calls)
	}
	assertNoData(t, f)
	f.app.Network = func(string, tui.NetworkLoader, tui.NetworkDecider) error { return errors.New("terminal failed") }
	f.run(t, 1, "network")
}
