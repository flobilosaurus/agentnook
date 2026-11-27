package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsEnvAssignmentsWithoutEchoingValues(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c := Empty()
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	w := testWorkspace("work", testOptions())
	p := w.Agents["pi"]
	p.EnvNames = []string{"NOOK_TEST_KEY"}
	w.Agents["pi"] = p
	c.Projects["/project"] = w
	if err := Save(file, c); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), "NOOK_TEST_KEY", "NOOK_TEST_KEY=test-secret-value", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil || !strings.Contains(err.Error(), "expected variable names") || strings.Contains(err.Error(), "test-secret-value") {
		t.Fatal("invalid env assignment was accepted or leaked into the diagnostic")
	}
}
