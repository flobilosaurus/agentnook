package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Image caches contain read-only rootfs directories. Only make this test's own
// disposable directories writable; WalkDir deliberately does not follow symlinks.
func removeSandbox(root string) error {
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(root)
}

func TestRemoveReadOnlySandbox(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sandbox")
	child := filepath.Join(root, "read-only")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "file"), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(child, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := removeSandbox(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("sandbox not removed: %v", err)
	}
}

type realVMFixture struct {
	data, workspace string
	env             []string
}

func (f realVMFixture) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, launcher, args...)
	cmd.Dir, cmd.Env = f.workspace, f.env
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second
	return cmd
}

// Explicit opt-in: builds and runs local smolvm artifacts; no Docker is needed.
func realVMLauncher(t *testing.T) (realVMFixture, func(...string) string) {
	t.Helper()
	if os.Getenv("AGENTNOOK_VM_SMOKE") != "1" {
		t.Skip("set AGENTNOOK_VM_SMOKE=1 to run a real smolvm VM")
	}
	for _, binary := range []string{"smolvm"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatal(err)
		}
	}
	// Keep HOME short: macOS limits Unix socket paths to 103 bytes.
	root, err := os.MkdirTemp("/tmp", "sp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := removeSandbox(root); err != nil {
			t.Error(err)
		}
	})
	home, workspace, data := filepath.Join(root, "h"), filepath.Join(root, "w"), filepath.Join(root, "d")
	for _, dir := range []string{home, workspace} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture := realVMFixture{data: data, workspace: workspace, env: append(os.Environ(),
		"HOME="+home, "AGENTNOOK_HOME="+data, "AGENTNOOK_NONINTERACTIVE=1", "AGENTNOOK_HERDR=0",
		"SMOLVM_DATA_DIR="+filepath.Join(root, "v"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "c"),
		"XDG_CACHE_HOME="+filepath.Join(home, "k"),
		"XDG_DATA_HOME="+filepath.Join(home, "d"),
		"XDG_STATE_HOME="+filepath.Join(home, "s"))}
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		output, err := fixture.command(ctx, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("real VM: %v\n%s", err, output)
		}
		return string(output)
	}
	return fixture, run
}

// Each agent must build successfully and launch with explicit and saved selections.
// A real build also catches npm lifecycle-policy failures that CLI stubs cannot.
func realVMSmoke(t *testing.T, agent, version string) (realVMFixture, func(...string) string) {
	t.Helper()
	fixture, run := realVMLauncher(t)
	base := "node:24-bookworm-slim"
	if agent == "pi" {
		base = "node:24-bookworm" // Includes curl for the interactive network test.
	}
	run("images", "build", agent+"-smoke", "--agent", agent,
		"--agent-version", version, "--base-image", base,
		"--cpus", "2", "--mem", "2048", "--storage", "8", "--set-default")
	for _, args := range [][]string{
		{"--agent", agent, "--profile", "smoke", "--", "--version"},
		{"--", "--version"},
	} {
		if output := run(args...); !strings.Contains(output, version) {
			t.Fatalf("unexpected %s version (args %q): %s", agent, args, output)
		}
	}
	return fixture, run
}

func TestRealVM(t *testing.T) {
	fixture, run := realVMSmoke(t, "pi", "1.4.0")
	run("--", "--offline", "--list-models")
	auth, err := os.ReadFile(filepath.Join(fixture.data, "profiles", "smoke", "pi", ".pi", "agent", "auth.json"))
	if err != nil {
		t.Fatalf("Pi did not create auth.json: %v", err)
	}
	var credentials map[string]any
	if err := json.Unmarshal(auth, &credentials); err != nil || credentials == nil || len(credentials) != 0 {
		t.Fatalf("expected a new, empty credential store; parse error: %v", err)
	}
	t.Run("EnvironmentForwarding", fixture.testPiEnvironmentForwarding)
	t.Run("NetworkApproval", fixture.testPiNetworkApproval)
}
