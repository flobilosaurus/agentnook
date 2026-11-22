// Package paths owns agentnook's host-side storage. No host agent files are imported.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
)

type Paths struct {
	Root string
}

func Resolve() (Paths, error) {
	root := os.Getenv("AGENTNOOK_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		root = filepath.Join(home, ".agentnook")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, err
	}
	// Resolve existing ancestors too, even before the data root has been created.
	root, err = resolveFuturePath(abs)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Root: root}, nil
}

func resolveFuturePath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent, name := filepath.Dir(path), filepath.Base(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveFuturePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, name), nil
}

func (p Paths) ConfigFile() string { return filepath.Join(p.Root, "config.toml") }

func (p Paths) AgentDir(profile, agent string) string {
	return filepath.Join(p.Root, "profiles", profile, agent)
}

// ValidateProject prevents a workspace mount from exposing other agentnook profiles.
func (p Paths) ValidateProject(project string) error {
	for _, path := range []string{p.Root, project} {
		if strings.ContainsAny(path, ":\x00\r\n") {
			return fmt.Errorf("mount path %q contains an unsupported colon or control character", path)
		}
	}
	if contains(project, p.Root) || contains(p.Root, project) {
		return fmt.Errorf("workspace and AGENTNOOK_HOME must not overlap: %s and %s", project, p.Root)
	}
	return nil
}

func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// EnsureAgentDir creates an isolated HOME. Each agent owns its files and locking.
func (p Paths) EnsureAgentDir(profile, agent string) (string, error) {
	if err := agents.ValidateName(agent); err != nil {
		return "", err
	}
	if err := config.ValidateProfileName(profile); err != nil {
		return "", err
	}
	dirs := []string{
		p.Root,
		filepath.Join(p.Root, "profiles"),
		filepath.Join(p.Root, "profiles", profile),
		p.AgentDir(profile, agent),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("profile storage %q must be a directory, not a symlink", dir)
		}
	}
	return p.AgentDir(profile, agent), nil
}
