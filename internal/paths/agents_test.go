package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentHomesIsolateAndPersistAllState(t *testing.T) {
	p := Paths{Root: filepath.Join(t.TempDir(), "data")}
	for _, agent := range []string{"pi", "opencode", "claude"} {
		dir, err := p.EnsureAgentDir("work", agent)
		if err != nil {
			t.Fatal(err)
		}
		if dir != filepath.Join(p.Root, "profiles", "work", agent) {
			t.Fatal(dir)
		}
		state := filepath.Join(dir, "state")
		if err := os.WriteFile(state, []byte(agent), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.EnsureAgentDir("work", agent); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(state)
		if err != nil || string(got) != agent {
			t.Fatal("state lost or mixed", dir, err)
		}
	}
	claude := p.AgentDir("work", "claude")
	for _, name := range []string{".claude.json", ".claude/.credentials.json"} {
		file := filepath.Join(claude, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("test-only"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.EnsureAgentDir("work", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude, ".claude.json")); err != nil {
		t.Fatal("root-level Claude state lost", err)
	}
	if _, err := p.EnsureAgentDir("work", "../escape"); err == nil {
		t.Fatal("unknown/traversal agent accepted")
	}
}
