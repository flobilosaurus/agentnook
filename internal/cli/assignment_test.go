package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/tui"
)

func TestAgentSwitchKeepsIndependentSelections(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "pi", "--profile", "private", "--cpus", "3")
	pi := f.load(t).Projects[f.project].Agents["pi"]
	f.run(t, 0, "--agent", "opencode", "--profile", "work", "--cpus", "4")
	f.run(t, 0, "--agent", "claude", "--profile", "work", "--cpus", "5")
	c := f.load(t)
	if len(c.Images) != 3 || len(c.DefaultImages) != 3 || len(c.Projects[f.project].Agents) != 3 || c.Projects[f.project].Agent != "claude" {
		t.Fatal(c)
	}
	f.run(t, 0, "--agent", "pi")
	c = f.load(t)
	if !reflect.DeepEqual(c.Projects[f.project].Agents["pi"], pi) || c.Projects[f.project].Agents["claude"].VM.CPUs != 5 || c.Projects[f.project].Agent != "pi" {
		t.Fatal(c)
	}
	for _, pair := range [][2]string{{"private", "pi"}, {"work", "opencode"}, {"work", "claude"}} {
		dir := filepath.Join(f.data, "profiles", pair[0], pair[1])
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatal(dir, info, err)
		}
	}
}
func TestImageCannotCrossAgentBoundary(t *testing.T) {
	f := setup(t)
	e := addCatalogImage(t, f, "pi-dev")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	f.run(t, 2, "--agent", "claude", "--profile", "work", "--image", e.Name)
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 0 || f.resolves != 0 {
		t.Fatal("wrong-agent image changed state")
	}
}
func TestAgentPickerCancellationIsReadOnly(t *testing.T) {
	f := setup(t)
	f.agentPrompt.err = tui.ErrCancelled
	f.run(t, 130)
	assertNoData(t, f)
}
func TestDefaultImageChangesOnlyNewAgentSelections(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--cpus", "3")
	old := f.load(t).Projects[f.project].Agents["pi"]
	entry := addCatalogImage(t, f, "next")
	f.run(t, 0, "images", "default", entry.Name)
	f.run(t, 0)
	if !reflect.DeepEqual(f.load(t).Projects[f.project].Agents["pi"], old) {
		t.Fatal("default changed existing selection")
	}
	other := filepath.Join(f.root, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(other)
	f.run(t, 0, "--agent", "pi", "--profile", "work")
	if f.load(t).Projects[other].Agents["pi"].VM != entry.VM {
		t.Fatal("new selection ignored default")
	}
}
