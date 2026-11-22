package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestThreeAgentsIndependentDefaultsAndSelections(t *testing.T) {
	s := newSandbox(t)
	var first []string
	for i, agent := range []string{"pi", "opencode", "claude"} {
		version := "2.3.2"
		if agent == "pi" {
			version = "1.4.0"
		}
		_, stderr, code := s.run(t, s.project, "", 0, "--agent", agent, "--profile", "work", "--agent-version", version, "--cpus", []string{"2", "3", "4"}[i], "--", "--continue")
		if code != 0 {
			t.Fatalf("%s: %d %s", agent, code, stderr)
		}
		argv := s.argv(t)
		if !reflect.DeepEqual(argv[len(argv)-3:], []string{"--", agent, "--continue"}) || !strings.HasSuffix(profileMount(argv), "/profiles/work/"+agent+":/root") {
			t.Fatal(argv)
		}
		if agent == "pi" {
			first = argv
		}
	}
	project, _ := config.NormalizeProjectPath(s.project)
	c, err := config.Load(filepath.Join(s.data, "config.toml"))
	if err != nil || len(c.Images) != 3 || len(c.DefaultImages) != 3 || len(c.Projects[project].Agents) != 3 || c.Projects[project].Agent != "claude" {
		t.Fatal(c, err)
	}
	_, stderr, code := s.run(t, s.project, "build-fail", 0, "--agent", "pi", "--", "--continue")
	if code != 0 || !reflect.DeepEqual(first, s.argv(t)) {
		t.Fatal(code, stderr, s.argv(t))
	}
	// An inactive agent's assignment still prevents removal.
	_, stderr, code = s.run(t, s.project, "", 0, "images", "remove", "claude-default")
	if code != 1 || !strings.Contains(stderr, "is still used by") {
		t.Fatal(code, stderr)
	}
	before, _ := os.ReadFile(filepath.Join(s.data, "config.toml"))
	_, stderr, code = s.run(t, s.project, "", 0, "--agent", "claude", "--image", "pi-default")
	after, _ := os.ReadFile(filepath.Join(s.data, "config.toml"))
	if code != 2 || !bytes.Equal(before, after) || !strings.Contains(stderr, "not claude") {
		t.Fatal(code, stderr)
	}
}
func TestOldConfigRejectedWithoutMigrationOrDeletion(t *testing.T) {
	s := newSandbox(t)
	if err := os.Mkdir(s.data, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.data, "config.toml")
	old := []byte("[defaults]\npi_version='1.4.0'\n")
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work")
	after, _ := os.ReadFile(path)
	if code != 1 || !strings.Contains(stderr, "not migrated") || !bytes.Equal(old, after) {
		t.Fatal(code, stderr)
	}
	if _, err := os.Stat(s.capture); !os.IsNotExist(err) {
		t.Fatal("old config launched VM")
	}
}
