package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/images"
	"github.com/flobilosaurus/agentnook/internal/paths"
)

func TestRemoveImageCommandNeedsNoDockerOrVM(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "old")
	keep := addCatalogImage(t, f, "keep")
	t.Setenv("PATH", "")
	f.run(t, 0, "images", "remove", "OLD")
	if !strings.Contains(f.out.String(), "Removed image: old") || f.runs != 0 || f.prompt.calls != 0 {
		t.Fatal("remove prompted, launched or lacked confirmation")
	}
	c := f.load(t)
	if len(c.Images) != 1 || !reflect.DeepEqual(c.Images[0], keep) {
		t.Fatalf("wrong catalog after removal: %+v", c.Images)
	}
	if _, err := os.Stat(filepath.Dir(entry.VM.Image)); !os.IsNotExist(err) {
		t.Fatal("build archive/folder not removed")
	}
	f.run(t, 1, "images", "remove", "old")
	if !strings.Contains(f.errOut.String(), "unknown managed image") {
		t.Fatal("missing unknown-image error")
	}
}

func TestRemoveImageCommandReportsDefaultAndProjectBlockers(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "old")
	f.run(t, 0, "--profile", "work", "--image", entry.Name)
	f.run(t, 0, "images", "default", entry.Name)
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	t.Setenv("PATH", "")
	f.run(t, 1, "images", "remove", entry.Name)
	for _, text := range []string{"is still used by", "default for pi", f.project} {
		if !strings.Contains(f.errOut.String(), text) {
			t.Fatalf("missing blocker %q: %s", text, &f.errOut)
		}
	}
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || !images.Available(entry) || f.runs != 1 {
		t.Fatal("blocked removal changed config/archive or launched")
	}
}

func TestRemoveMissingArchiveAndUnknownImageCommand(t *testing.T) {
	f := setup(t)
	f.run(t, 1, "images", "remove", "missing")
	assertNoData(t, f)
	entry := addCatalogImage(t, f, "old")
	if err := os.Remove(entry.VM.Image); err != nil {
		t.Fatal(err)
	}
	f.run(t, 0, "images", "remove", entry.Name)
	if len(f.load(t).Images) != 0 {
		t.Fatal("missing archive retained catalog entry")
	}
}

func TestManagedArchiveAliasesAreProtectedDuringLaunch(t *testing.T) {
	for _, concurrentRemoval := range []bool{false, true} {
		t.Run(map[bool]string{false: "save alias", true: "concurrent removal"}[concurrentRemoval], func(t *testing.T) {
			f := setup(t)
			entry := addCatalogImage(t, f, "old")
			alias := filepath.Join(f.root, "alias.smolmachine")
			if err := os.Symlink(entry.VM.Image, alias); err != nil {
				t.Fatal(err)
			}
			want := 0
			if concurrentRemoval {
				want = 1
				calls := 0
				f.app.Interactive = func() bool {
					calls++
					if calls != 2 {
						return true
					}
					if _, err := images.Remove(paths.Paths{Root: f.data}, entry.Name); err != nil {
						t.Fatal(err)
					}
					return true
				}
			}
			f.run(t, want, "--profile", "work", "--image", alias)
			if concurrentRemoval {
				if f.runs != 0 || len(f.load(t).Projects) != 0 || !strings.Contains(f.errOut.String(), "was removed or replaced") {
					t.Fatalf("removed alias escaped launch guard: %s", &f.errOut)
				}
			} else if f.load(t).Projects[f.project].Agents["pi"].VM.Image != entry.VM.Image {
				t.Fatal("valid archive alias was not resolved to its catalog archive")
			}
		})
	}
}

func TestLaunchCannotSaveImageRemovedAfterSelection(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "old")
	removed := false
	calls := 0
	f.app.Interactive = func() bool {
		calls++
		if !removed && calls == 2 {
			removed = true
			if _, err := images.Remove(paths.Paths{Root: f.data}, entry.Name); err != nil {
				t.Fatal(err)
			}
		}
		return true
	}
	// The second terminal check occurs after image validation and before saving.
	f.run(t, 1, "--profile", "work", "--image", entry.Name)
	if !removed || f.runs != 0 || len(f.load(t).Projects) != 0 || f.load(t).HasProfile("work") ||
		!strings.Contains(f.errOut.String(), "was removed or replaced") {
		t.Fatalf("stale launch persisted or started: %s", &f.errOut)
	}
}
