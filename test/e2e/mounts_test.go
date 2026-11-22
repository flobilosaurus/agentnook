package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func extraVolumes(args []string) []string {
	var extra []string
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		if args[i] == "--volume" && !isFixedMount(args[i+1]) {
			extra = append(extra, args[i+1])
		}
	}
	return extra
}

func isFixedMount(value string) bool {
	return strings.HasSuffix(value, ":/workspace") || strings.HasSuffix(value, ":/root")
}

func TestMultipleDirectoryMountsPersistReplaceAndClear(t *testing.T) {
	s := newSandbox(t)
	root, err := filepath.EvalSymlinks(filepath.Dir(s.project))
	if err != nil {
		t.Fatal(err)
	}
	shared, docs := filepath.Join(root, "shared with spaces"), filepath.Join(root, "docs")
	for _, dir := range []string{shared, docs} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work", "--agent-version", "1.4.0",
		"--mount", "../shared with spaces:/mnt/shared", "--mount", docs+":/workspace/docs:ro")
	if code != 0 {
		t.Fatalf("mount launch: %d %s", code, stderr)
	}
	want := []string{shared + ":/mnt/shared", docs + ":/workspace/docs:ro"}
	if got := extraVolumes(s.argv(t)); !reflect.DeepEqual(got, want) {
		t.Fatalf("extra mounts=%q want=%q", got, want)
	}
	if _, stderr, code := s.run(t, s.project, "", 0); code != 0 {
		t.Fatalf("remembered launch: %d %s", code, stderr)
	}
	if !reflect.DeepEqual(extraVolumes(s.argv(t)), want) {
		t.Fatal("mounts not reused by subsequent process")
	}
	configPath := filepath.Join(s.data, "config.toml")
	beforeConfig, _ := os.ReadFile(configPath)
	beforeVM, _ := os.ReadFile(s.capture)
	out, stderr, code := s.run(t, s.project, "", 0, "--dry-run", "--mount", docs+":/mnt/preview:ro")
	if code != 0 {
		t.Fatalf("dry-run: %d %s", code, stderr)
	}
	var preview []string
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(extraVolumes(preview), []string{docs + ":/mnt/preview:ro"}) {
		t.Fatalf("wrong dry-run mount: %q", preview)
	}
	afterConfig, _ := os.ReadFile(configPath)
	afterVM, _ := os.ReadFile(s.capture)
	if !bytes.Equal(beforeConfig, afterConfig) || !bytes.Equal(beforeVM, afterVM) {
		t.Fatal("dry-run persisted mount changes or launched a VM")
	}
	other := filepath.Join(root, "other project")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := s.run(t, other, "", 0, "--agent", "pi", "--profile", "work"); code != 0 {
		t.Fatalf("other project: %d %s", code, stderr)
	}
	if len(extraVolumes(s.argv(t))) != 0 {
		t.Fatal("mounts leaked between projects sharing profile")
	}
	if _, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "another", "--mount", docs+":/mnt/replaced"); code != 0 {
		t.Fatalf("replace: %d %s", code, stderr)
	}
	if !reflect.DeepEqual(extraVolumes(s.argv(t)), []string{docs + ":/mnt/replaced"}) {
		t.Fatal("new flag appended instead of replacing saved mounts")
	}
	if _, stderr, code := s.run(t, s.project, "", 0, "--clear-mounts"); code != 0 {
		t.Fatalf("clear: %d %s", code, stderr)
	}
	if _, stderr, code := s.run(t, s.project, "", 0); code != 0 {
		t.Fatalf("remembered clear: %d %s", code, stderr)
	}
	c, err := config.Load(configPath)
	project, _ := config.NormalizeProjectPath(s.project)
	if err != nil || len(c.Projects[project].Agents["pi"].Mounts) != 0 || len(extraVolumes(s.argv(t))) != 0 {
		t.Fatalf("mounts not cleared persistently: %+v %v", c, err)
	}
}

func TestUnsafeMountIsRejectedWithoutBuildingOrWritingConfig(t *testing.T) {
	s := newSandbox(t)
	// A parent of AGENTNOOK_HOME must not be exposed, even read-only.
	source := filepath.Dir(s.project)
	_, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work", "--mount", source+":/mnt/all:ro")
	if code != 2 {
		t.Fatalf("unsafe mount: %d %s", code, stderr)
	}
	for _, path := range []string{s.data, s.capture, s.capture + ".build"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("invalid mount caused side effects at %s: %v", path, err)
		}
	}
}
