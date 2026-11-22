package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveManagedImageProtectsReferencesAndPreservesLogin(t *testing.T) {
	s := newSandbox(t)
	for _, name := range []string{"old", "keep"} {
		args := []string{"images", "build", name, "--agent", "pi", "--agent-version", "1.4.0"}
		if name == "old" {
			args = append(args, "--set-default")
		}
		if _, stderr, code := s.run(t, s.project, "", 0, args...); code != 0 {
			t.Fatalf("build %s: %d %s", name, code, stderr)
		}
	}
	_, stderr, code := s.run(t, s.project, "login", 0, "--agent", "pi", "--profile", "work")
	if code != 0 {
		t.Fatalf("launch: %d %s", code, stderr)
	}
	first := s.argv(t)
	archive := value(first, "--from")
	auth := filepath.Join(strings.TrimSuffix(profileMount(first), ":/root"), ".pi", "agent", "auth.json")
	beforeAuth, err := os.ReadFile(auth)
	if err != nil {
		t.Fatal(err)
	}
	beforeBuild, err := os.ReadFile(s.capture + ".build")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(s.data, "config.toml")
	beforeConfig, _ := os.ReadFile(configPath)
	_, stderr, code = s.run(t, s.project, "", 0, "images", "remove", "old")
	if code != 1 || !strings.Contains(stderr, "default for pi") || !strings.Contains(stderr, s.project) {
		t.Fatalf("missing reference protection: %d %s", code, stderr)
	}
	afterConfig, _ := os.ReadFile(configPath)
	if !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatal("blocked removal changed config")
	}
	if _, stderr, code := s.run(t, s.project, "", 0, "images", "default", "keep"); code != 0 {
		t.Fatalf("change default: %d %s", code, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0, "images", "remove", "old")
	if code != 1 || !strings.Contains(stderr, s.project) || strings.Contains(stderr, "default for pi") {
		t.Fatalf("saved project no longer protected: %d %s", code, stderr)
	}
	if _, stderr, code := s.run(t, s.project, "", 0, "--image", "keep"); code != 0 {
		t.Fatalf("change project image: %d %s", code, stderr)
	}
	beforeVM, err := os.ReadFile(s.capture)
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, code := s.run(t, s.project, "", 0, "images", "remove", "OLD")
	if code != 0 || !strings.Contains(out, "Removed image: old") {
		t.Fatalf("remove: %d %s %s", code, out, stderr)
	}
	if _, err := os.Stat(filepath.Dir(archive)); !os.IsNotExist(err) {
		t.Fatal("old archive/build folder not deleted")
	}
	afterAuth, _ := os.ReadFile(auth)
	afterBuild, _ := os.ReadFile(s.capture + ".build")
	afterVM, _ := os.ReadFile(s.capture)
	if !bytes.Equal(beforeAuth, afterAuth) || !bytes.Equal(beforeBuild, afterBuild) || !bytes.Equal(beforeVM, afterVM) {
		t.Fatal("removal changed login or invoked build/VM")
	}
	out, stderr, code = s.run(t, s.project, "", 0, "images")
	if code != 0 || strings.Contains(out, "old") || !strings.Contains(out, "keep") {
		t.Fatalf("wrong remaining catalog: %d %s %s", code, out, stderr)
	}
	if _, stderr, code := s.run(t, s.project, "", 0); code != 0 {
		t.Fatalf("remaining image cannot launch: %d %s", code, stderr)
	}
}
