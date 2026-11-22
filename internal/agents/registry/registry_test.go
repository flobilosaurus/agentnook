package registry

import (
	"strings"
	"testing"
)

func TestAgentRecipesAndEnvironment(t *testing.T) {
	for _, name := range All().Names() {
		adapter, err := All().Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		spec := adapter.Spec()
		recipe := adapter.InstallScript()
		for _, want := range []string{"export HOME=/tmp/agentnook-build", "npm install --global --allow-scripts=" + spec.Package + " " + spec.Package + "@\"${AGENT_VERSION}\"", "actual_version=\"$(" + name + " --version)\"", "version mismatch: expected %s, got %s", "git ripgrep fd-find ca-certificates"} {
			if !strings.Contains(recipe, want) {
				t.Fatalf("%s recipe missing %q", name, want)
			}
		}
		if strings.Contains(recipe, "COPY") || strings.Contains(recipe, "--ignore-scripts") || strings.Contains(recipe, "--dangerously-allow-all-scripts") || strings.Contains(recipe, "--allow-scripts=*") {
			t.Fatal("unsafe context or broken native package installation", recipe)
		}
		if name == "opencode" && !strings.Contains(recipe, `{"update":"disable"}`) {
			t.Fatal("OpenCode auto-update not disabled")
		}
		for _, env := range adapter.Environment() {
			if name != "pi" && strings.HasPrefix(env, "PI_") {
				t.Fatal("Pi environment leaked")
			}
		}
	}
}
func TestAgentAndVersionValidation(t *testing.T) {
	for _, name := range []string{"", "other", "PI", "../pi", "pi;echo nope"} {
		if _, err := All().Lookup(name); err == nil {
			t.Fatal("accepted", name)
		}
	}
	for _, tc := range []struct {
		agent, version string
		ok             bool
	}{{"pi", "1.4.0", true}, {"claude", "2.1.99", true}, {"opencode", "2.3.2", true}, {"opencode", "1.19.6", false}, {"opencode", "3.0.0", false}, {"pi", "latest", false}, {"pi", "1.4.0; touch /tmp/nope", false}, {"pi", "1.4.0\nRUN echo nope", false}} {
		if (All().ValidateVersion(tc.agent, tc.version) == nil) != tc.ok {
			t.Fatal(tc)
		}
	}
}
