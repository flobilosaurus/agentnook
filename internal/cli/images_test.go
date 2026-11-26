package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/tui"
)

type fakeImagePrompter struct {
	name    string
	err     error
	calls   int
	options config.Options
}

func (p *fakeImagePrompter) ChooseImage(_ string, options config.Options, _ []config.BuiltImage) (string, error) {
	p.calls++
	p.options = options
	return p.name, p.err
}

func addCatalogImage(t *testing.T, f *fixture, name string) config.BuiltImage {
	t.Helper()
	archive := filepath.Join(f.data, "images", name+"-test", "image.smolmachine")
	if err := os.MkdirAll(filepath.Dir(archive), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("test-only archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := testOptions()
	options.Image = archive
	options.CPUs, options.Memory = 2, 2048
	entry := config.BuiltImage{Name: name, BaseImage: "node:24-bookworm",
		Architecture: runtime.GOARCH, BuiltAt: "2026-11-17T10:00:00Z", VM: options}
	c := f.load(t)
	if err := c.AddImage(entry); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(filepath.Join(f.data, "config.toml"), c); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestCatalogImageSelectionAndPerDirectorySnapshots(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	f.run(t, 0, "--profile", "work", "--image", "WORK-V1", "--mem", "4096")
	want := entry.VM
	want.Memory = 4096
	got := f.load(t)
	if got.Projects[f.project].Agents["pi"].VM != want {
		t.Fatalf("wrong per-directory image preset: %+v", got.Projects[f.project].Agents["pi"])
	}
	for _, text := range []string{"Profile: work", "Image: work-v1", "Linux base: node:24-bookworm", "Pi: 1.4.0 (built)", "CPU: 2", "RAM: 4096 MiB"} {
		if !strings.Contains(f.errOut.String(), text) {
			t.Fatalf("missing %q in launch display: %s", text, &f.errOut)
		}
	}
	first := append([]string(nil), f.argv...)
	f.run(t, 0)
	if strings.Join(stableLaunchArgs(first), "\n") != strings.Join(stableLaunchArgs(f.argv), "\n") {
		t.Fatal("saved image options not reused")
	}

	// Different defaults do not change the remembered image or resource overrides.
	f.run(t, 0, "images", "default", "work-v1")
	f.run(t, 0)
	if f.load(t).Projects[f.project].Agents["pi"].VM != want {
		t.Fatal("new defaults changed existing project")
	}
	other := filepath.Join(f.root, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(other)
	f.run(t, 0, "--profile", "work")
	if f.load(t).Projects[other].Agents["pi"].VM != entry.VM {
		t.Fatal("new directory did not inherit catalog default")
	}
}

func TestReadOnlyImageListNeedsNeitherDockerNorVM(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	t.Setenv("PATH", "")
	for _, args := range [][]string{{"images"}, {"--images"}} {
		f.run(t, 0, args...)
		for _, text := range []string{entry.Name, entry.BaseImage, "2048", "ready"} {
			if !strings.Contains(f.out.String(), text) {
				t.Fatalf("missing %q: %s", text, &f.out)
			}
		}
	}
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 0 || f.prompt.calls != 0 {
		t.Fatal("listing changed config, prompted or launched VM")
	}
	if _, err := os.Stat(filepath.Join(f.data, "profiles")); !os.IsNotExist(err) {
		t.Fatal("listing created a profile")
	}
}

func TestImageListEmptyDoesNotCreateStorage(t *testing.T) {
	f := setup(t)
	t.Setenv("PATH", "")
	f.run(t, 0, "images")
	if !strings.Contains(f.out.String(), "No managed builds") {
		t.Fatal("no empty-catalog guidance")
	}
	assertNoData(t, f)
}

func TestLauncherChoiceCanSelectImageAndResourceOverrides(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	f.prompt.choice = tui.Choice{Profile: "work", Create: true, Image: entry.Name}
	f.run(t, 0, "--mem", "4096")
	want := entry.VM
	want.Memory = 4096
	if f.load(t).Projects[f.project].Agents["pi"].VM != want {
		t.Fatal("launcher image selection or explicit memory override lost")
	}
}

func TestSelectImageKeepsProfileAndCanCancelWithoutSaving(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	f.run(t, 0, "--profile", "work")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	picker := &fakeImagePrompter{err: tui.ErrCancelled}
	f.app.ImagePrompter = picker
	f.run(t, 130, "--select-image")
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 1 {
		t.Fatal("cancelled image picker saved or launched")
	}
	picker.err, picker.name = nil, entry.Name
	f.run(t, 0, "--select-image")
	got := f.load(t).Projects[f.project].Agents["pi"]
	if got.Profile != "work" || got.VM != entry.VM || f.prompt.calls != 0 || picker.calls != 2 {
		t.Fatalf("image picker changed profile or lost preset: %+v", got)
	}
}

func TestUnavailableManagedImagesCannotLaunchOrBecomeDefault(t *testing.T) {
	for _, failure := range []string{"missing", "empty", "symlink", "architecture"} {
		t.Run(failure, func(t *testing.T) {
			f := setup(t)
			entry := addCatalogImage(t, f, "work-v1")
			switch failure {
			case "missing":
				if err := os.Remove(entry.VM.Image); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.Truncate(entry.VM.Image, 0); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(entry.VM.Image); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(f.root, "target.smolmachine")
				if err := os.WriteFile(target, []byte("archive"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, entry.VM.Image); err != nil {
					t.Fatal(err)
				}
			case "architecture":
				c := f.load(t)
				c.Images[0].Architecture = "amd64"
				if runtime.GOARCH == "amd64" {
					c.Images[0].Architecture = "arm64"
				}
				if err := config.Save(filepath.Join(f.data, "config.toml"), c); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
			f.run(t, 2, "--profile", "work", "--image", entry.Name)
			f.run(t, 1, "images", "default", entry.Name)
			after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
			if !bytes.Equal(before, after) || f.runs != 0 || f.prompt.calls != 0 {
				t.Fatal("unavailable image changed config or launched")
			}
		})
	}
}

func TestDryRunCatalogImageDoesNotChangeConfig(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	f.run(t, 0, "--profile", "work", "--image", entry.Name, "--dry-run")
	var argv []string
	if err := json.Unmarshal(f.out.Bytes(), &argv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), entry.VM.Image) {
		t.Fatal("archive not resolved")
	}
	for _, arg := range argv {
		if arg == "--init" {
			t.Fatal("managed build was provisioned")
		}
	}
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 0 {
		t.Fatal("dry-run changed config or launched")
	}
}

func TestImageBuildRejectsInvalidToolBeforeVersionLookup(t *testing.T) {
	f := setup(t)
	for _, tool := range []string{"", "--help", "rg\nfd"} {
		f.run(t, 2, "images", "build", "bad", "--agent", "pi", "--tool="+tool)
		if !strings.Contains(f.errOut.String(), "invalid mise tool") {
			t.Fatalf("unexpected error for tool %q: %s", tool, &f.errOut)
		}
	}
	assertNoData(t, f)
}

func TestImageBuildRejectsInvalidMiseConfigBeforeVersionLookup(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(filepath.Join(f.project, "bad.toml"), []byte("[tools]\nrg = 14\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{
		{"--mise-config="},
		{"--mise-config", "missing.toml"},
		{"--mise-config", "bad.toml"},
		{"--mise-config", "first.toml", "--mise-config", "second.toml"},
	} {
		f.run(t, 2, append([]string{"images", "build", "bad", "--agent", "pi"}, flags...)...)
		if !strings.Contains(f.errOut.String(), "mise-config") {
			t.Fatalf("missing flag context for %v: %s", flags, &f.errOut)
		}
	}
	if f.resolves != 0 {
		t.Fatal("invalid mise config triggered version lookup")
	}
	assertNoData(t, f)
}

func TestImageCommandErrorsAndHelp(t *testing.T) {
	for _, args := range [][]string{
		{"images", "--help"}, {"images", "build", "--help"}, {"images", "build", "x", "--help"},
		{"images", "remove", "--help"}, {"images", "remove", "-h"},
	} {
		f := setup(t)
		f.run(t, 0, args...)
		assertNoData(t, f)
	}
	for _, args := range [][]string{
		{"images", "unknown"}, {"images", "build"}, {"images", "default"},
		{"images", "remove"}, {"images", "remove", "one", "two"}, {"images", "remove", "../escape"},
		{"images", "remove", "work", "--force"},
		{"images", "build", "../escape"}, {"images", "build", "work", "--cpus", "0"},
		{"images", "build", "work", "--agent-version", "latest"}, {"images", "build", "work", "extra"},
		{"--profile", "work", "--select-image", "--image", "work"},
		{"--images", "--profile", "work"}, {"--images", "--", "--print"},
	} {
		f := setup(t)
		f.run(t, 2, args...)
		assertNoData(t, f)
	}
	f := setup(t)
	f.run(t, 1, "images", "default", "missing")
	assertNoData(t, f)
	t.Setenv("AGENTNOOK_NONINTERACTIVE", "1")
	f.run(t, 1, "--profile", "work", "--select-image")
	assertNoData(t, f)
}
