package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

func TestAgentVersionCheck(t *testing.T) {
	for _, tc := range []struct {
		name, agent, version, actual, versionExit, want string
	}{
		{name: "pi", agent: "pi", version: "1.4.0", actual: "1.4.0", versionExit: "0"},
		{name: "claude", agent: "claude", version: "2.1.99", actual: "2.1.99 (Claude Code)", versionExit: "0"},
		{name: "opencode", agent: "opencode", version: "2.3.2", actual: "opencode v2.3.2", versionExit: "0"},
		{name: "opencode bare version", agent: "opencode", version: "2.3.2", actual: "2.3.2", versionExit: "0"},
		{name: "wrong prefixed version", agent: "opencode", version: "2.3.2", actual: "opencode v2.3.1", versionExit: "0", want: "version mismatch: expected 2.3.2, got 2.3.1"},
		{name: "version substring", agent: "opencode", version: "2.3.2", actual: "opencode v2.3.20", versionExit: "0", want: "version mismatch: expected 2.3.2, got 2.3.20"},
		{name: "other agent prefix", agent: "opencode", version: "2.3.2", actual: "other v2.3.2", versionExit: "0", want: "version mismatch"},
		{name: "empty output", agent: "opencode", version: "2.3.2", versionExit: "0", want: "version mismatch"},
		{name: "binary failure", agent: "opencode", version: "2.3.2", actual: "opencode v2.3.2", versionExit: "1", want: "agent installation is incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := All().Lookup(tc.agent)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for name, script := range map[string]string{
				"mkdir":  "exit 0\n",
				tc.agent: "printf '%s\\n' \"$TEST_ACTUAL_VERSION\"\nexit \"$TEST_VERSION_EXIT\"\n",
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/sh", "-eu", "-c", agents.VersionCheck(adapter.Spec()))
			cmd.Env = []string{"PATH=" + dir, "AGENT_VERSION=" + tc.version, "TEST_ACTUAL_VERSION=" + tc.actual, "TEST_VERSION_EXIT=" + tc.versionExit}
			output, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("version check: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), tc.want) {
				t.Fatalf("want %q failure, got %v\n%s", tc.want, err, output)
			}
		})
	}
}

func TestOpenCodeInstallRunsOnlyApprovedPostinstallAndChecksVersion(t *testing.T) {
	adapter, err := All().Lookup("opencode")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, actual, versionExit, want string
		blockPostinstall                bool
	}{
		{name: "approved postinstall", actual: "2.3.2", versionExit: "0"},
		{name: "prefixed version", actual: "opencode v2.3.2", versionExit: "0"},
		{name: "wrong prefixed version", actual: "opencode v2.3.1", versionExit: "0", want: "version mismatch: expected 2.3.2, got 2.3.1"},
		{name: "binary failure with prefixed output", actual: "opencode v2.3.2", versionExit: "1", want: "agent installation is incomplete"},
		{name: "blocked postinstall", actual: "2.3.2", versionExit: "0", blockPostinstall: true, want: "agent installation is incomplete"},
		{name: "wrong version", actual: "2.3.1", versionExit: "0", want: "version mismatch: expected 2.3.2, got 2.3.1"},
		{name: "binary failure with version output", actual: "2.3.2", versionExit: "1", want: "agent installation is incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			scripts := map[string]string{
				"mkdir": "exit 0\n", "rm": "exit 0\n", "apt-get": "exit 0\n",
				"npm": `if [ "$1" = install ]; then
  approved=false
  for arg do
    case "$arg" in
      --allow-scripts=@opencode/cli) approved=true ;;
      --allow-scripts=*|--dangerously-allow-all-scripts|--ignore-scripts) exit 97 ;;
    esac
  done
  if [ "$approved" = true ]; then printf installed > "$TEST_MARKER"; fi
fi
exit 0
`,
				"opencode": `[ -f "$TEST_MARKER" ] || exit 1
printf '%s\n' "$TEST_ACTUAL_VERSION"
exit "$TEST_VERSION_EXIT"
`,
			}
			for name, script := range scripts {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			recipe := strings.ReplaceAll(adapter.InstallScript(), "/etc/opencode", "'"+dir+"'")
			if tc.blockPostinstall {
				recipe = strings.Replace(recipe, "--allow-scripts=@opencode/cli ", "", 1)
			}
			cmd := exec.Command("/bin/sh", "-eu", "-c", recipe)
			cmd.Env = []string{"PATH=" + dir, "AGENT_VERSION=2.3.2", "TEST_MARKER=" + filepath.Join(dir, "installed"), "TEST_ACTUAL_VERSION=" + tc.actual, "TEST_VERSION_EXIT=" + tc.versionExit}
			output, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("install: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), tc.want) {
				t.Fatalf("want %q failure, got %v\n%s", tc.want, err, output)
			}
		})
	}
}
