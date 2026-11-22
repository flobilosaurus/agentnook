package images

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
)

// Remove deletes an unused managed artifact and its catalog entry.
// The archive is staged until the config save succeeds, allowing rollback on error.
func Remove(p paths.Paths, name string) (config.BuiltImage, error) {
	return remove(p, name, config.Update)
}

func remove(p paths.Paths, name string, update func(string, func(*config.Config) error) error) (config.BuiltImage, error) {
	// Unknown names must not create storage or a config lock.
	c, err := config.Load(p.ConfigFile())
	if err != nil {
		return config.BuiltImage{}, err
	}
	if _, found := c.FindImage(name); !found {
		return config.BuiltImage{}, fmt.Errorf("unknown managed image %q", name)
	}
	var entry config.BuiltImage
	var archiveRoot *os.Root
	staged := ""
	err = update(p.ConfigFile(), func(current *config.Config) error {
		var found bool
		entry, found = current.FindImage(name)
		if !found {
			return fmt.Errorf("image %q no longer exists", name)
		}
		var users []string
		for agent, name := range current.DefaultImages {
			if e, ok := current.FindImage(name); ok && referencesArchive(e.VM.Image, entry.VM.Image) {
				users = append(users, "default for "+agent+" (nook images default NAME)")
			}
		}
		for project, workspace := range current.Projects {
			for agent, mapping := range workspace.Agents {
				if referencesArchive(mapping.VM.Image, entry.VM.Image) {
					users = append(users, fmt.Sprintf("project %q (%s)", project, agent))
				}
			}
		}
		for _, other := range current.Images {
			if other.Name != entry.Name && referencesArchive(other.VM.Image, entry.VM.Image) {
				users = append(users, fmt.Sprintf("managed image %q", other.Name))
			}
		}
		if len(users) != 0 {
			sort.Strings(users)
			return fmt.Errorf("image %q is still used by %s; select another image before removing it", entry.Name, strings.Join(users, ", "))
		}
		archiveRoot, staged, err = stageArchive(p, entry)
		if err != nil {
			return err
		}
		for i, image := range current.Images {
			if image.Name == entry.Name {
				current.Images = append(current.Images[:i], current.Images[i+1:]...)
				break
			}
		}
		return nil
	})
	if archiveRoot != nil {
		defer archiveRoot.Close()
	}
	if err != nil {
		if staged != "" {
			if restoreErr := archiveRoot.Rename(staged, "image.smolmachine"); restoreErr != nil {
				return config.BuiltImage{}, errors.Join(err, fmt.Errorf("restore archive from %s: %w", filepath.Join(filepath.Dir(entry.VM.Image), staged), restoreErr))
			}
		}
		return config.BuiltImage{}, err
	}
	if staged != "" {
		if err := archiveRoot.Remove(staged); err != nil {
			return entry, fmt.Errorf("image %q removed from catalog, but archive cleanup failed at %s: %w", entry.Name, filepath.Join(filepath.Dir(entry.VM.Image), staged), err)
		}
	}
	// Never recursively remove a build folder: preserve any unrelated files in it.
	// Keep cleanup confined too, even if a storage symlink changed meanwhile.
	if root, err := os.OpenRoot(p.Root); err == nil {
		_ = root.Remove(filepath.Join("images", filepath.Base(filepath.Dir(entry.VM.Image))))
		root.Close()
	}
	return entry, nil
}

func referencesArchive(reference, archive string) bool {
	if reference == archive || (filepath.IsAbs(reference) && filepath.Clean(reference) == archive) {
		return true
	}
	// Also protect manually configured symlink/hardlink aliases of an existing archive.
	left, leftErr := os.Stat(reference)
	right, rightErr := os.Stat(archive)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

// stageArchive accepts only the layout created by Build. Root operations prevent
// filesystem access outside managed storage, including through swapped symlinks.
func stageArchive(p paths.Paths, entry config.BuiltImage) (*os.Root, string, error) {
	storage := filepath.Join(p.Root, "images")
	relative, err := filepath.Rel(storage, entry.VM.Image)
	dir := filepath.Dir(relative)
	if err != nil || filepath.Base(relative) != "image.smolmachine" || filepath.Dir(dir) != "." ||
		!strings.HasPrefix(dir, entry.Name+"-") || dir == entry.Name+"-" {
		return nil, "", fmt.Errorf("refusing to remove image %q outside its managed build directory: %s", entry.Name, entry.VM.Image)
	}
	// Reject symlink directories rather than following even an in-tree alias.
	for _, directory := range []string{p.Root, storage} {
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		if err != nil {
			return nil, "", err
		}
		if !info.IsDir() {
			return nil, "", fmt.Errorf("image storage %q must be a directory, not a symlink", directory)
		}
	}
	base, err := os.OpenRoot(p.Root)
	if err != nil {
		return nil, "", err
	}
	defer base.Close()
	root, err := base.OpenRoot("images")
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	info, err := root.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("image build directory %q must be a directory, not a symlink", dir)
	}
	archive, err := root.OpenRoot(dir)
	if err != nil {
		return nil, "", err
	}
	info, err = archive.Lstat("image.smolmachine")
	if errors.Is(err, os.ErrNotExist) {
		return archive, "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		archive.Close()
		if err == nil {
			err = fmt.Errorf("image %q archive is not a regular file: %s", entry.Name, entry.VM.Image)
		}
		return nil, "", err
	}
	staged := ".remove-" + rand.Text() + ".smolmachine"
	if err := archive.Rename("image.smolmachine", staged); err != nil {
		archive.Close()
		return nil, "", err
	}
	return archive, staged, nil
}
