package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFirstLaunchBuildsPinnedDefaultPerAgent(t *testing.T) {
	for _, agent := range []string{"pi", "opencode", "claude"} {
		t.Run(agent, func(t *testing.T) {
			f := setup(t)
			f.run(t, 0, "--agent", agent, "--profile", "work", "--cpus", "3", "--mem", "4096")
			c := f.load(t)
			e := c.Images[0]
			if len(c.Images) != 1 || e.Name != agent+"-default" || e.VM.Agent != agent || c.DefaultImages[agent] != e.Name {
				t.Fatal(c)
			}
			p := c.Projects[f.project].Agents[agent]
			if p.VM.CPUs != 3 || p.VM.Memory != 4096 || e.VM.CPUs != 2 || e.VM.Memory != 2048 {
				t.Fatal(p, e)
			}
			first := append([]string(nil), f.argv...)
			t.Setenv("AGENTNOOK_TEST_BUILD_EXIT", "7")
			f.run(t, 0)
			if !reflect.DeepEqual(stableLaunchArgs(first), stableLaunchArgs(f.argv)) || f.resolves != 1 {
				t.Fatal("remembered launch rebuilt or changed")
			}
		})
	}
}
func TestInitialVersionOverrideAndImageVersionMismatch(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "claude", "--profile", "work", "--agent-version", "2.1.99")
	if f.resolves != 0 || f.load(t).Images[0].VM.AgentVersion != "2.1.99" {
		t.Fatal("override ignored")
	}
	data, _ := os.ReadFile(os.Getenv("AGENTNOOK_TEST_SMOLVM_LOG"))
	if !strings.Contains(string(data), "AGENT_VERSION=2.1.99") {
		t.Fatal(string(data))
	}
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	f.run(t, 2, "--agent-version", "2.1.100")
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 1 {
		t.Fatal("mismatch changed state")
	}
}
func TestFailedBootstrapDoesNotRegisterProfileOrLaunch(t *testing.T) {
	for _, failure := range []string{"smolvm missing", "resolve", "build"} {
		t.Run(failure, func(t *testing.T) {
			f := setup(t)
			switch failure {
			case "smolvm missing":
				if err := os.Remove(filepath.Join(os.Getenv("PATH"), "smolvm")); err != nil {
					t.Fatal(err)
				}
			case "resolve":
				f.app.LatestVersion = func(string) (string, error) { return "", errors.New("registry unavailable") }
			case "build":
				t.Setenv("AGENTNOOK_TEST_BUILD_EXIT", "7")
			}
			want := 1
			if failure == "smolvm missing" {
				want = 127
			}
			f.run(t, want, "--agent", "opencode", "--profile", "work")
			c := f.load(t)
			if f.runs != 0 || len(c.Images) != 0 || len(c.Profiles) != 0 {
				t.Fatal(c)
			}
			if _, err := os.Stat(filepath.Join(f.data, "config.toml")); !os.IsNotExist(err) {
				t.Fatal("failed build saved config")
			}
		})
	}
}
func TestDryRunAndUnknownImagesDoNotBootstrap(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{[]string{"--dry-run"}, 1}, {[]string{"--image", "missing"}, 2}} {
		f := setup(t)
		args := append([]string{"--agent", "pi", "--profile", "work"}, tc.args...)
		f.run(t, tc.code, args...)
		assertNoData(t, f)
		if f.resolves != 0 {
			t.Fatal("queried registry")
		}
	}
}
