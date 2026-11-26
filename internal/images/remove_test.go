package images

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
)

func removalFixture(t *testing.T) (paths.Paths, config.BuiltImage) {
	t.Helper()
	p, options := buildFixture(t)
	archive := filepath.Join(p.Root, "images", options.Name+"-test", "image.smolmachine")
	if err := os.MkdirAll(filepath.Dir(archive), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("test-only archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	options.VM.Image = archive
	entry := config.BuiltImage{Name: options.Name, BaseImage: options.BaseImage,
		Architecture: runtime.GOARCH, BuiltAt: "2026-11-17T10:00:00Z", VM: options.VM}
	c := config.Empty()
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	c.Projects["/unrelated"] = testWorkspace("work", testOptions())
	if err := c.AddImage(entry); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(p.ConfigFile(), c); err != nil {
		t.Fatal(err)
	}
	return p, entry
}

func changeRemovalConfig(t *testing.T, p paths.Paths, change func(*config.Config)) {
	t.Helper()
	if err := config.Update(p.ConfigFile(), func(c *config.Config) error { change(c); return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveUnusedArchivePreservesDefaultsProjectsAndProfile(t *testing.T) {
	p, entry := removalFixture(t)
	piDir, err := p.EnsureAgentDir("work", "pi")
	if err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(piDir, "auth.json")
	if err := os.WriteFile(auth, []byte("test-only credentials"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := config.Load(p.ConfigFile())
	t.Setenv("PATH", "")
	removed, err := Remove(p, strings.ToUpper(entry.Name))
	if err != nil || !reflect.DeepEqual(removed, entry) {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	after, err := config.Load(p.ConfigFile())
	if err != nil || len(after.Images) != 0 || !reflect.DeepEqual(after.DefaultImages, before.DefaultImages) ||
		!reflect.DeepEqual(after.Projects, before.Projects) || !reflect.DeepEqual(after.Profiles, before.Profiles) {
		t.Fatalf("unrelated config changed: %+v %v", after, err)
	}
	if _, err := os.Stat(filepath.Dir(entry.VM.Image)); !os.IsNotExist(err) {
		t.Fatal("archive/build folder not removed")
	}
	data, err := os.ReadFile(auth)
	if err != nil || string(data) != "test-only credentials" {
		t.Fatal("profile credentials changed")
	}
}

func TestRemoveMissingEmptyAndForeignArchitectureArchives(t *testing.T) {
	for _, state := range []string{"missing archive", "missing build folder", "missing storage", "empty", "foreign architecture", "extra file"} {
		t.Run(state, func(t *testing.T) {
			p, entry := removalFixture(t)
			switch state {
			case "missing archive":
				if err := os.Remove(entry.VM.Image); err != nil {
					t.Fatal(err)
				}
			case "missing build folder":
				if err := os.RemoveAll(filepath.Dir(entry.VM.Image)); err != nil {
					t.Fatal(err)
				}
			case "missing storage":
				if err := os.RemoveAll(filepath.Join(p.Root, "images")); err != nil {
					t.Fatal(err)
				}
			case "empty":
				if err := os.Truncate(entry.VM.Image, 0); err != nil {
					t.Fatal(err)
				}
			case "foreign architecture":
				changeRemovalConfig(t, p, func(c *config.Config) { c.Images[0].Architecture = "amd64" })
			case "extra file":
				if err := os.WriteFile(filepath.Join(filepath.Dir(entry.VM.Image), "keep.txt"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Remove(p, entry.Name); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(entry.VM.Image); !os.IsNotExist(err) {
				t.Fatal("archive still present")
			}
			c, err := config.Load(p.ConfigFile())
			if err != nil || len(c.Images) != 0 {
				t.Fatalf("catalog not cleaned: %+v %v", c, err)
			}
			if state == "extra file" {
				data, err := os.ReadFile(filepath.Join(filepath.Dir(entry.VM.Image), "keep.txt"))
				if err != nil || string(data) != "keep" {
					t.Fatal("unrelated build-folder file deleted")
				}
			}
		})
	}
}

func TestRemoveRefusesAllStoredReferences(t *testing.T) {
	for _, reference := range []string{"defaults", "project", "both", "symlink alias", "hardlink alias", "catalog alias", "cleaned path"} {
		t.Run(reference, func(t *testing.T) {
			p, entry := removalFixture(t)
			alias := entry.VM.Image
			if reference == "symlink alias" || reference == "hardlink alias" {
				alias = filepath.Join(t.TempDir(), "alias.smolmachine")
				link := os.Symlink
				if reference == "hardlink alias" {
					link = os.Link
				}
				if err := link(entry.VM.Image, alias); err != nil {
					t.Fatal(err)
				}
			}
			if reference == "cleaned path" {
				alias = filepath.Dir(entry.VM.Image) + "/../" + filepath.Base(filepath.Dir(entry.VM.Image)) + "/image.smolmachine"
			}
			changeRemovalConfig(t, p, func(c *config.Config) {
				if reference == "defaults" || reference == "both" {
					c.DefaultImages[entry.VM.Agent] = entry.Name
				}
				if reference != "defaults" && reference != "catalog alias" {
					options := entry.VM
					options.Image = alias
					c.Projects["/using image"] = testWorkspace("work", options)
				}
				if reference == "catalog alias" {
					other := entry
					other.Name = "other"
					if err := c.AddImage(other); err != nil {
						t.Fatal(err)
					}
				}
			})
			before, _ := os.ReadFile(p.ConfigFile())
			_, err := Remove(p, entry.Name)
			if err == nil || !strings.Contains(err.Error(), "is still used by") {
				t.Fatalf("missing reference protection: %v", err)
			}
			if reference == "both" && (!strings.Contains(err.Error(), "default for pi") || !strings.Contains(err.Error(), "/using image")) {
				t.Fatalf("incomplete users list: %v", err)
			}
			after, _ := os.ReadFile(p.ConfigFile())
			if !bytes.Equal(before, after) || !Available(entry) {
				t.Fatal("blocked removal changed config/archive")
			}
		})
	}
}

func TestRemoveRefusesUnsafeStorage(t *testing.T) {
	for _, unsafe := range []string{"outside storage", "wrong filename", "wrong directory", "storage symlink", "build symlink", "archive symlink", "archive directory"} {
		t.Run(unsafe, func(t *testing.T) {
			p, entry := removalFixture(t)
			switch unsafe {
			case "outside storage", "wrong filename", "wrong directory":
				path := filepath.Join(t.TempDir(), "image.smolmachine")
				if unsafe == "wrong filename" {
					path = filepath.Join(filepath.Dir(entry.VM.Image), "other.smolmachine")
				} else if unsafe == "wrong directory" {
					path = filepath.Join(p.Root, "images", "unrelated", "image.smolmachine")
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Rename(entry.VM.Image, path); err != nil {
					t.Fatal(err)
				}
				entry.VM.Image = path
				changeRemovalConfig(t, p, func(c *config.Config) { c.Images[0] = entry })
			case "storage symlink", "build symlink":
				dir := filepath.Dir(entry.VM.Image)
				if unsafe == "storage symlink" {
					dir = filepath.Join(p.Root, "images")
				}
				target := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(dir, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, dir); err != nil {
					t.Fatal(err)
				}
			case "archive symlink":
				target := filepath.Join(t.TempDir(), "image.smolmachine")
				if err := os.Rename(entry.VM.Image, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, entry.VM.Image); err != nil {
					t.Fatal(err)
				}
			case "archive directory":
				if err := os.Remove(entry.VM.Image); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(entry.VM.Image, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(p.ConfigFile())
			if _, err := Remove(p, entry.Name); err == nil {
				t.Fatal("unsafe removal accepted")
			}
			after, _ := os.ReadFile(p.ConfigFile())
			if !bytes.Equal(before, after) {
				t.Fatal("unsafe removal changed config")
			}
			if _, err := os.Stat(entry.VM.Image); err != nil {
				t.Fatalf("unsafe removal touched archive/target: %v", err)
			}
		})
	}
}

func TestRemoveRestoresStagedArchiveOnConfigSaveFailure(t *testing.T) {
	p, entry := removalFixture(t)
	before, _ := os.ReadFile(p.ConfigFile())
	failure := errors.New("simulated config save failure")
	_, err := remove(p, entry.Name, func(path string, change func(*config.Config) error) error {
		return config.Update(path, func(c *config.Config) error {
			if err := change(c); err != nil {
				return err
			}
			if _, err := os.Stat(entry.VM.Image); !os.IsNotExist(err) {
				t.Fatal("archive was not staged before config save")
			}
			return failure
		})
	})
	after, _ := os.ReadFile(p.ConfigFile())
	if !errors.Is(err, failure) || !bytes.Equal(before, after) || !Available(entry) {
		t.Fatalf("rollback failed: %v", err)
	}
	files, err := os.ReadDir(filepath.Dir(entry.VM.Image))
	if err != nil || len(files) != 1 || files[0].Name() != "image.smolmachine" {
		t.Fatalf("staged archive leaked: %v %v", files, err)
	}
}

func TestRemoveRechecksReferencesUnderLock(t *testing.T) {
	p, entry := removalFixture(t)
	_, err := remove(p, entry.Name, func(path string, change func(*config.Config) error) error {
		// Simulate another launcher committing a reference after the initial read.
		changeRemovalConfig(t, p, func(c *config.Config) { c.DefaultImages[entry.VM.Agent] = entry.Name })
		return config.Update(path, change)
	})
	if err == nil || !strings.Contains(err.Error(), "default for pi") || !Available(entry) {
		t.Fatalf("stale reference check: %v", err)
	}
}

func TestRemoveUnknownDoesNotCreateStorage(t *testing.T) {
	p, _ := buildFixture(t)
	if _, err := Remove(p, "missing"); err == nil {
		t.Fatal("unknown image accepted")
	}
	if _, err := os.Stat(p.Root); !os.IsNotExist(err) {
		t.Fatal("unknown image created storage")
	}
}
