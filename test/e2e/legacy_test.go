package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyArchivesRejectedWithoutChangingConfigOrLogin(t *testing.T) {
	s := newSandbox(t)
	if _, stderr, code := s.run(t, s.project, "login", 0, "--agent", "pi", "--profile", "work", "--agent-version", "1.4.0"); code != 0 {
		t.Fatal(code, stderr)
	}
	current := s.argv(t)
	artifact := value(current, "--from")
	legacy := strings.TrimSuffix(artifact, ".smolmachine") + ".tar"
	legacyData := []byte("untouched legacy image")
	if err := os.WriteFile(legacy, legacyData, 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(s.data, "config.toml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	old := bytes.ReplaceAll(content, []byte(artifact), []byte(legacy))
	if err := os.WriteFile(configPath, old, 0o600); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(strings.TrimSuffix(profileMount(current), ":/root"), ".pi", "agent", "auth.json")
	beforeAuth, _ := os.ReadFile(auth)
	beforeLaunch, _ := os.ReadFile(s.capture)
	beforeBuild, _ := os.ReadFile(s.capture + ".build")
	for _, args := range [][]string{nil, {"images", "build", "new", "--agent", "pi", "--agent-version", "1.4.0"}} {
		_, stderr, code := s.run(t, s.project, "", 0, args...)
		if code != 1 || !strings.Contains(stderr, "old .tar images are not supported") || !strings.Contains(stderr, "keep profiles/") {
			t.Fatal(code, stderr)
		}
		after, _ := os.ReadFile(configPath)
		afterAuth, _ := os.ReadFile(auth)
		afterLegacy, _ := os.ReadFile(legacy)
		afterLaunch, _ := os.ReadFile(s.capture)
		afterBuild, _ := os.ReadFile(s.capture + ".build")
		if !bytes.Equal(after, old) || !bytes.Equal(afterAuth, beforeAuth) || !bytes.Equal(afterLegacy, legacyData) || !bytes.Equal(afterLaunch, beforeLaunch) || !bytes.Equal(afterBuild, beforeBuild) {
			t.Fatal("legacy rejection changed state or started a machine")
		}
	}
}
