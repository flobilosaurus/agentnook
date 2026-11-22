package registry

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
