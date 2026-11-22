package registry

import (
	"encoding/json"
	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testAgent(t *testing.T, name string) agents.Agent {
	t.Helper()
	a, err := All().Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAgentSpecificTelemetryLaunches(t *testing.T) {
	for _, agent := range []string{"pi", "opencode", "claude"} {
		t.Run(agent, func(t *testing.T) {
			l, err := telemetry.Prepare(testAgent(t, agent), true, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			base := []string{"machine", "run", "--env", "HOME=/root", "--", "ignored", "--continue", "prompt with spaces"}
			args := l.Args(base)
			joined := strings.Join(args, "\n")
			if !reflect.DeepEqual(args[len(args)-2:], base[len(base)-2:]) {
				t.Fatal("agent args changed", args)
			}
			for i, arg := range args {
				if arg == "--env" && strings.Contains(args[i+1], l.RunID()) {
					t.Fatal("nonce in smolvm cache inputs")
				}
			}
			if !strings.Contains(joined, l.Dir()+":"+telemetry.GuestDir) {
				t.Fatal("missing share", args)
			}
			switch agent {
			case "pi":
				if !strings.Contains(joined, "pi\n--extension\n"+telemetry.GuestDir+"/status.ts") {
					t.Fatal(args)
				}
			case "opencode":
				if !strings.Contains(joined, ":"+"/etc/opencode/plugins/agentnook"+":ro") || strings.Contains(joined, "--extension") {
					t.Fatal(args)
				}
				if _, err := os.Stat(filepath.Join(l.Dir(), "opencode", "index.mjs")); err != nil {
					t.Fatal(err)
				}
			case "claude":
				if !strings.Contains(joined, "node\n"+telemetry.GuestDir+"/claude.mjs") || strings.Contains(joined, "--extension") {
					t.Fatal(args)
				}
				data, err := os.ReadFile(filepath.Join(l.Dir(), "claude-settings.json"))
				if err != nil {
					t.Fatal(err)
				}
				var settings struct {
					Hooks map[string][]struct {
						Hooks []struct{ Type, Command string }
					}
				}
				if err := json.Unmarshal(data, &settings); err != nil {
					t.Fatal(err)
				}
				for _, event := range []string{"SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd"} {
					hooks := settings.Hooks[event]
					if len(hooks) != 1 || len(hooks[0].Hooks) != 1 || hooks[0].Hooks[0].Command != "node "+telemetry.GuestDir+"/claude-hook.mjs" {
						t.Fatal(event, settings)
					}
				}
			}
		})
	}
}
func TestUtilitiesAndNoninteractiveModesHaveNoTelemetry(t *testing.T) {
	for _, tc := range []struct {
		agent string
		args  []string
	}{{"pi", []string{"--mode=rpc"}}, {"pi", []string{"--print=hello"}}, {"claude", []string{"--print"}}, {"claude", []string{"--settings={}"}}, {"claude", []string{"auth", "login"}}, {"claude", []string{"mcp", "list"}}, {"opencode", []string{"run", "hello"}}, {"opencode", []string{"auth", "login"}}, {"opencode", []string{"serve"}}, {"opencode", []string{"--version"}}} {
		l, err := telemetry.Prepare(testAgent(t, tc.agent), true, tc.args)
		if err != nil || l != nil {
			if l != nil {
				l.Close()
			}
			t.Fatal(tc, l, err)
		}
	}
	for _, agent := range []string{"pi", "opencode", "claude"} {
		l, err := telemetry.Prepare(testAgent(t, agent), false, nil)
		if err != nil || l != nil {
			t.Fatal(agent, l, err)
		}
	}
	if l, err := telemetry.Prepare(nil, true, nil); err == nil || l != nil {
		t.Fatal("unknown agent accepted")
	}
}
func TestGuestAdapters(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for guest adapter tests")
	}
	claude, err := telemetry.Prepare(testAgent(t, "claude"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer claude.Close()
	opencode, err := telemetry.Prepare(testAgent(t, "opencode"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer opencode.Close()
	cmd := exec.Command(node, "adapters_test.mjs", claude.Dir(), opencode.Dir())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapters: %v\n%s", err, output)
	}
}
