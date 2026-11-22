package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

func mountSources(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	first, second := filepath.Join(f.root, "shared with spaces"), filepath.Join(f.root, "docs")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return first, second
}

func mountVolumes(args []string) []string {
	var volumes []string
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		// This helper compares persisted mounts, not private per-run telemetry.
		if args[i] == "--volume" && !strings.HasSuffix(args[i+1], ":/run/agentnook-telemetry") {
			volumes = append(volumes, args[i+1])
		}
	}
	return volumes
}

func TestExtraMountsPersistReplaceClearAndSurviveSelections(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	addCatalogImage(t, f, "work-v2")
	first, second := mountSources(t, f)
	f.run(t, 0, "--profile", "work", "--mount", "../shared with spaces:/mnt/shared", "--mount", second+":/workspace/docs:ro")
	want := []config.Mount{{Host: first, Guest: "/mnt/shared"}, {Host: second, Guest: "/workspace/docs", ReadOnly: true}}
	if !reflect.DeepEqual(f.load(t).Projects[f.project].Agents["pi"].Mounts, want) {
		t.Fatal("mounts not persisted/canonicalized")
	}
	volumes := mountVolumes(f.argv)
	if len(volumes) != 4 || volumes[2] != first+":/mnt/shared" || volumes[3] != second+":/workspace/docs:ro" {
		t.Fatalf("wrong VM mount arguments: %q", volumes)
	}
	for _, text := range []string{"Mount: " + first, "read-write", "Mount: " + second, "read-only"} {
		if !strings.Contains(f.errOut.String(), text) {
			t.Fatalf("missing mount display %q", text)
		}
	}
	f.run(t, 0)
	if !reflect.DeepEqual(mountVolumes(f.argv), volumes) {
		t.Fatal("saved mounts not reused")
	}
	f.run(t, 0, "--profile", "other", "--image", "work-v2")
	if !reflect.DeepEqual(f.load(t).Projects[f.project].Agents["pi"].Mounts, want) {
		t.Fatal("profile/image selection reset mounts")
	}
	f.prompt.choice = tui.Choice{Profile: "work", Image: entry.Name}
	f.run(t, 0, "--select")
	if !reflect.DeepEqual(f.load(t).Projects[f.project].Agents["pi"].Mounts, want) {
		t.Fatal("interactive selection reset mounts")
	}
	other := filepath.Join(f.root, "other project")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(other)
	f.run(t, 0, "--profile", "work")
	if len(f.load(t).Projects[other].Agents["pi"].Mounts) != 0 || len(mountVolumes(f.argv)) != 2 {
		t.Fatal("extra mounts leaked to another project sharing the profile")
	}
	t.Chdir(f.project)
	f.run(t, 0, "--mount", second+":/mnt/replacement:ro")
	if got := f.load(t).Projects[f.project].Agents["pi"].Mounts; len(got) != 1 || got[0].Guest != "/mnt/replacement" || !got[0].ReadOnly {
		t.Fatalf("mount list was appended instead of replaced: %+v", got)
	}
	f.run(t, 0, "--clear-mounts=false")
	if len(f.load(t).Projects[f.project].Agents["pi"].Mounts) != 1 {
		t.Fatal("explicit false cleared saved mounts")
	}
	f.run(t, 0, "--clear-mounts")
	f.run(t, 0)
	if len(f.load(t).Projects[f.project].Agents["pi"].Mounts) != 0 || len(mountVolumes(f.argv)) != 2 {
		t.Fatal("extra mounts not cleared persistently")
	}
}

func TestMountDryRunAndCancellationDoNotChangeSavedMounts(t *testing.T) {
	f := setup(t)
	addCatalogImage(t, f, "work-v1")
	first, second := mountSources(t, f)
	f.run(t, 0, "--profile", "work", "--mount", first+":/mnt/shared")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	for _, flags := range [][]string{{"--mount", second + ":/mnt/docs:ro"}, {"--clear-mounts"}} {
		f.run(t, 0, append([]string{"--dry-run"}, flags...)...)
		var args []string
		if err := json.Unmarshal(f.out.Bytes(), &args); err != nil {
			t.Fatal(err)
		}
		volumes := mountVolumes(args)
		if len(flags) == 2 && (len(volumes) != 3 || volumes[2] != second+":/mnt/docs:ro") {
			t.Fatalf("dry-run ignored new mount: %q", volumes)
		}
		if len(flags) == 1 && len(volumes) != 2 {
			t.Fatal("dry-run ignored clear-mounts")
		}
		after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
		if !bytes.Equal(before, after) || f.runs != 1 {
			t.Fatal("dry-run changed config or started a VM")
		}
	}
	f.prompt.err = tui.ErrCancelled
	f.run(t, 130, "--select", "--mount", second+":/mnt/docs:ro")
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 1 {
		t.Fatal("cancelled selection persisted mount changes")
	}
}

func TestInvalidMountsFailBeforePromptBuildOrStorage(t *testing.T) {
	for _, name := range []string{"syntax", "missing", "file", "mode", "reserved", "overlap", "agentnook storage", "conflict"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			first, _ := mountSources(t, f)
			value := first + ":/mnt/shared"
			flags := []string{"--mount", value}
			switch name {
			case "syntax":
				flags[1] = first
			case "missing":
				flags[1] = filepath.Join(f.root, "missing") + ":/mnt/shared"
			case "file":
				file := filepath.Join(f.root, "file")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				flags[1] = file + ":/mnt/shared"
			case "mode":
				flags[1] += ":rw"
			case "reserved":
				flags[1] = first + ":/root/.pi/agent"
			case "overlap":
				flags = append(flags, "--mount", first+":/mnt/shared/child:ro")
			case "agentnook storage":
				flags[1] = f.root + ":/mnt/all:ro"
			case "conflict":
				flags = append(flags, "--clear-mounts")
			}
			f.run(t, 2, flags...)
			assertNoData(t, f)
			if f.prompt.calls != 0 || f.resolves != 0 {
				t.Fatal("invalid mount prompted or queried npm")
			}
		})
	}
}

func TestMountsSurviveAutomaticBuildAndInvalidImageFails(t *testing.T) {
	f := setup(t)
	first, _ := mountSources(t, f)
	f.run(t, 0, "--profile", "work", "--mount", first+":/mnt/shared:ro")
	c := f.load(t)
	want := c.Projects[f.project].Agents["pi"].Mounts
	if len(want) != 1 || len(c.Images) != 1 || f.resolves != 1 {
		t.Fatal("automatic default build lost extra mounts")
	}
	legacy := c.Projects[f.project].Agents["pi"]
	legacy.VM.Image = "ghcr.io/example/legacy:1"
	c.Projects[f.project].Agents["pi"] = legacy
	if err := config.Save(filepath.Join(f.data, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	f.run(t, 1)
	if !reflect.DeepEqual(f.load(t).Projects[f.project].Agents["pi"].Mounts, want) || f.resolves != 1 {
		t.Fatal("invalid image changed mounts or rebuilt")
	}
}

func TestMountSourcesAreRecheckedBeforeSaving(t *testing.T) {
	f := setup(t)
	addCatalogImage(t, f, "work-v1")
	first, _ := mountSources(t, f)
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	calls := 0
	f.app.Interactive = func() bool {
		calls++
		if calls != 2 {
			return true
		}
		if err := os.Remove(first); err != nil {
			t.Fatal(err)
		}
		return true
	}
	f.run(t, 1, "--profile", "work", "--mount", first+":/mnt/shared")
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 0 || f.load(t).HasProfile("work") {
		t.Fatal("source removed after initial validation was saved or launched")
	}
}

func TestMissingSavedMountCanBeClearedAndPiArgumentsStayUntouched(t *testing.T) {
	f := setup(t)
	addCatalogImage(t, f, "work-v1")
	first, _ := mountSources(t, f)
	f.run(t, 0, "--profile", "work", "--mount", first+":/mnt/shared")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	f.run(t, 2)
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 1 {
		t.Fatal("missing source changed config or started VM")
	}
	f.run(t, 0, "--clear-mounts", "--", "--mount", "pi-argument")
	if len(f.load(t).Projects[f.project].Agents["pi"].Mounts) != 0 || !reflect.DeepEqual(f.argv[len(f.argv)-2:], []string{"--mount", "pi-argument"}) {
		t.Fatal("clear failed or Pi mount argument was parsed by launcher")
	}
}
