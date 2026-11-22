package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateProtectionAndExceptionsThroughLauncherProcess(t *testing.T) {
	s := newSandbox(t)
	_, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work", "--agent-version", "1.8.2")
	if code != 0 || !strings.Contains(strings.Join(s.argv(t), " "), "--block-private") {
		t.Fatal(code, stderr)
	}
	builds, err := os.ReadFile(s.capture + ".build")
	if err != nil || strings.Contains(string(builds), "--block-private") || strings.Contains(string(builds), "--allow-private") {
		t.Fatal("build VM network changed", err)
	}
	_, stderr, code = s.run(t, s.project, "", 0, "--allow-private", "10.20.1.2/16")
	if code != 0 || value(s.argv(t), "--allow-private") != "10.20.0.0/16" {
		t.Fatal(code, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0)
	if code != 0 || value(s.argv(t), "--allow-private") != "10.20.0.0/16" {
		t.Fatal("saved exception not reused", code, stderr)
	}
	file := filepath.Join(s.data, "config.toml")
	before, _ := os.ReadFile(file)
	_, stderr, code = s.run(t, s.project, "", 0, "--network", "ask")
	after, _ := os.ReadFile(file)
	if code != 2 || string(before) != string(after) || !strings.Contains(stderr, "bypass approvals") {
		t.Fatal("ask silently accepted static private allow", code, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0, "--network", "ask", "--clear-allow-private")
	if code != 0 || value(s.argv(t), "--allow-private") != "" || !strings.Contains(strings.Join(s.argv(t), " "), "--block-private") {
		t.Fatal("protected ask failed", code, stderr)
	}
	files, err := os.ReadDir(filepath.Join(s.data, "network"))
	if err != nil || len(files) != 1 || files[0].Name() != "history" || !files[0].IsDir() {
		t.Fatal("broker registration leaked", files, err)
	}
}

func TestPrivateProtectionRequiresUpdatedRuntimeInFullMode(t *testing.T) {
	s := newSandbox(t)
	_, stderr, code := s.run(t, s.project, "old-runtime", 0, "--agent", "pi", "--profile", "work", "--network", "full")
	if code != 127 || !strings.Contains(stderr, "smolvm >= 1.36.1") {
		t.Fatal(code, stderr)
	}
	if _, err := os.Stat(s.data); !os.IsNotExist(err) {
		t.Fatal("unsupported protection changed storage", err)
	}
}
