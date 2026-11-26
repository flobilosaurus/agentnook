package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestImagesStatusInteractiveWithoutRuntime(t *testing.T) {
	s := newSandbox(t)
	for _, name := range []string{"first", "second"} {
		if _, stderr, code := s.run(t, s.project, "", 0, "images", "build", name, "--agent", "pi",
			"--agent-version", "1.4.0", "--tool", "rg@14.1.1"); code != 0 {
			t.Fatalf("build %s: %d %s", name, code, stderr)
		}
	}
	if _, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work", "--image", "first"); code != 0 {
		t.Fatalf("save project selection: %d %s", code, stderr)
	}
	file := filepath.Join(s.data, "config.toml")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcher, "images", "status")
	cmd.Dir = s.project
	cmd.Env = []string{"HOME=" + s.home, "AGENTNOOK_HOME=" + s.data, "PATH="}
	term := startTestTerminal(t, cmd)
	defer func() {
		if t.Failed() {
			t.Logf("terminal output: %q", term.text())
		}
	}()
	term.expect(t, 10*time.Second, `Images\s+│ Saved selection · project with spaces \[current folder\]`)
	term.expect(t, 5*time.Second, `▸   └ project with spaces\s+current`)
	term.expect(t, 5*time.Second, `Saved image: first \(present\)`)
	term.expect(t, 5*time.Second, `\? legend`)
	term.send(t, "?")
	term.expect(t, 5*time.Second, `N saved — Folder assignments, not running VMs`)
	term.send(t, "\x1b[D")
	term.expect(t, 5*time.Second, `rg@14\.1\.1`)
	term.send(t, "?\rj")
	term.expect(t, 5*time.Second, `Images\s+│ second`)
	term.send(t, "q")
	term.wait(t)
	if after, err := os.ReadFile(file); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("interactive status changed configuration: %v", err)
	}
}
