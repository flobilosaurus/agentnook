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

// Explicit opt-in: builds and runs local smolvm artifacts; no Docker is needed.
func realVMLauncher(t *testing.T) (string, func(...string) string) {
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
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, launcher, args...)
		cmd.Dir = workspace
		cmd.Env = append(os.Environ(),
			"HOME="+home, "AGENTNOOK_HOME="+data, "AGENTNOOK_NONINTERACTIVE=1",
			"XDG_CONFIG_HOME="+filepath.Join(home, "c"),
			"XDG_CACHE_HOME="+filepath.Join(home, "k"),
			"XDG_DATA_HOME="+filepath.Join(home, "d"),
			"XDG_STATE_HOME="+filepath.Join(home, "s"))
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = 15 * time.Second
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("real VM: %v\n%s", err, output)
		}
		return string(output)
	}
	return data, run
}

func TestRealVM(t *testing.T) {
	data, run := realVMLauncher(t)
	run("--agent", "pi", "--profile", "smoke", "--agent-version", "1.4.0", "--cpus", "2", "--mem", "2048", "--storage", "8",
		"--", "--offline", "--list-models")
	auth, err := os.ReadFile(filepath.Join(data, "profiles", "smoke", "pi", ".pi", "agent", "auth.json"))
	if err != nil {
		t.Fatalf("Pi did not create auth.json: %v", err)
	}
	var credentials map[string]any
	if err := json.Unmarshal(auth, &credentials); err != nil || credentials == nil || len(credentials) != 0 {
		t.Fatalf("expected a new, empty credential store; parse error: %v", err)
	}
	if output := run("--", "--version"); !strings.Contains(output, "1.4.0") {
		t.Fatalf("unexpected Pi version: %s", output)
	}
}
