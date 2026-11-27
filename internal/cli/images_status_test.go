package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestImagesStatusShowsImageCompositionWithoutRuntime(t *testing.T) {
	f := setup(t)
	f.app.Interactive = func() bool { return false }
	dev := addCatalogImage(t, f, "dev")
	old := addCatalogImage(t, f, "old")
	if err := os.Remove(old.VM.Image); err != nil {
		t.Fatal(err)
	}
	c := f.load(t)
	c.Images[0].Tools = []string{"rg@latest", "aqua:sharkdp/fd@10.3.0"}
	c.DefaultImages[dev.VM.Agent] = "DEV"
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	c.Projects[f.project] = config.Workspace{Agent: dev.VM.Agent, Agents: map[string]config.Project{
		dev.VM.Agent: {Profile: "work", VM: dev.VM, EnvNames: []string{"NOOK_STATUS_TOKEN", "NOOK_STATUS_UNSET"}},
	}}
	file := filepath.Join(f.data, "config.toml")
	if err := config.Save(file, c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	t.Setenv("NOOK_STATUS_TOKEN", "test-env-value-must-not-appear")
	f.run(t, 0, "images", "status")
	if strings.Contains(f.out.String(), "test-env-value-must-not-appear") {
		t.Fatal("status printed an environment value")
	}
	for _, want := range []string{
		"Config: " + file, "Managed images: 2", "dev [default for pi] [current folder]",
		"Agent: Pi 1.4.0", "Linux base: node:24-bookworm (" + dev.Architecture + ")",
		"Built: " + dev.BuiltAt, "Artifact: " + dev.VM.Image + " (present)",
		"Launch preset: 2 CPU · 2048 MiB RAM · 20 GiB disk",
		"apt packages (versions not recorded): git, ripgrep, fd-find, ca-certificates",
		"Requested mise tools (resolved versions not recorded):\n    - rg@latest\n    - aqua:sharkdp/fd@10.3.0",
		"Artifact: " + old.VM.Image + " (missing or invalid)", "Requested mise tools: none recorded",
		"Saved selection: \"" + f.project + "\" (Pi / work)\n    Configured environment: NOOK_STATUS_TOKEN, NOOK_STATUS_UNSET",
	} {
		if !strings.Contains(f.out.String(), want) {
			t.Fatalf("missing %q in %s", want, &f.out)
		}
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) || f.runs != 0 || f.resolves != 0 || f.prompt.calls != 0 || f.errOut.Len() != 0 {
		t.Fatalf("status changed config, required runtime, or prompted: %v; stderr: %s", err, &f.errOut)
	}
	for _, name := range []string{"config.toml.lock", "profiles"} {
		if _, err := os.Stat(filepath.Join(f.data, name)); !os.IsNotExist(err) {
			t.Fatalf("status created %s: %v", name, err)
		}
	}
}

func TestImagesStatusEmptyHelpAndInvalidArgs(t *testing.T) {
	f := setup(t)
	f.app.Interactive = func() bool { return false }
	t.Setenv("PATH", "")
	f.run(t, 0, "images", "status")
	if !strings.Contains(f.out.String(), "No managed builds") {
		t.Fatal(f.out.String())
	}
	for _, args := range [][]string{{"images", "--help"}, {"images", "status", "--help"}, {"images", "status", "-h"}} {
		f.run(t, 0, args...)
		if !strings.Contains(f.out.String(), "nook images status") {
			t.Fatalf("missing command in help for %q: %s", args, &f.out)
		}
	}
	f.run(t, 2, "images", "status", "extra")
	if !strings.Contains(f.errOut.String(), "nook images status --help") {
		t.Fatal(f.errOut.String())
	}
	f.run(t, 2, "config", "status") // Old spelling is not an alias.
	assertNoData(t, f)
}
