package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

// An entirely unknown adapter proves orchestration does not secretly dispatch on
// the three built-in IDs. Its binary deliberately differs from its catalog ID.
type customAdapter struct{}

func (customAdapter) Spec() agents.Spec {
	return agents.Spec{Name: "custom", Label: "Custom Agent", Package: "test-only-package"}
}
func (customAdapter) ValidateVersion(version string) error { return agents.ValidateVersion(version) }
func (a customAdapter) InstallScript() string {
	spec := a.Spec()
	spec.Name = "custom-binary"
	return agents.InstallScript(spec, "")
}
func (customAdapter) Environment() []string { return []string{"CUSTOM_AGENT_STATE=/root/custom-state"} }
func (customAdapter) Command(pass []string) []string {
	return append([]string{"custom-binary", "--fixed-option"}, pass...)
}
func (customAdapter) Telemetry(bool, []string) *agents.TelemetryPlan { return nil }
func (customAdapter) ReservedPaths() []string                        { return []string{"/custom-reserved"} }

func TestLaunchUsesOnlyInjectedAdapterContract(t *testing.T) {
	f := setup(t)
	f.app.Agents = agents.Catalog{customAdapter{}}
	pass := []string{"--continue", "spaces ; $(echo nope)"}
	args := append([]string{"--agent", "custom", "--profile", "work", "--agent-version", "9.8.7", "--"}, pass...)
	f.run(t, 0, args...)
	want := append([]string{"--", "custom-binary", "--fixed-option"}, pass...)
	if !reflect.DeepEqual(f.argv[len(f.argv)-len(want):], want) {
		t.Fatal("adapter command was replaced", f.argv)
	}
	if !strings.Contains(strings.Join(f.argv, "\n"), "CUSTOM_AGENT_STATE=/root/custom-state") {
		t.Fatal("adapter environment missing", f.argv)
	}
	c := f.load(t)
	if c.Projects[f.project].Agent != "custom" || c.Images[0].VM.Agent != "custom" {
		t.Fatal(c)
	}
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	f.run(t, 2, "--mount", f.project+":/custom-reserved/child")
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 1 {
		t.Fatal("adapter-owned reserved path was mounted")
	}
}
