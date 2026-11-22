package paths

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/flobilosaurus/agentnook/internal/config"
)

// ParseMount resolves user-supplied host directories once. Relative paths refer
// to the current project, not the profile or image storage directory.
func ParseMount(value, project string) (config.Mount, error) {
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" ||
		(len(parts) == 3 && parts[2] != "ro") {
		return config.Mount{}, fmt.Errorf("invalid mount %q: use HOST:GUEST[:ro]", value)
	}
	host := parts[0]
	if strings.ContainsAny(host, "\x00\r\n") || strings.ContainsAny(parts[1], "\x00\r\n") {
		return config.Mount{}, fmt.Errorf("invalid control character in mount %q", value)
	}
	if host == "~" || strings.HasPrefix(host, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return config.Mount{}, err
		}
		host = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(host, "~"), "/"))
	}
	if !filepath.IsAbs(host) {
		host = filepath.Join(project, host)
	}
	host, err := config.NormalizeProjectPath(host)
	if err != nil {
		return config.Mount{}, fmt.Errorf("mount source %q: %w", parts[0], err)
	}
	mount := config.Mount{Host: host, Guest: path.Clean(parts[1]), ReadOnly: len(parts) == 3}
	if err := config.ValidateMounts([]config.Mount{mount}); err != nil {
		return config.Mount{}, err
	}
	return mount, nil
}

// ValidateMounts checks only the active project's sources on every launch.
// Canonical stored paths must not have been replaced with symlink aliases.
func (p Paths) ValidateMounts(mounts []config.Mount, reservedPaths ...string) error {
	if err := config.ValidateMounts(mounts, reservedPaths...); err != nil {
		return err
	}
	for _, mount := range mounts {
		real, err := config.NormalizeProjectPath(mount.Host)
		if err != nil {
			return fmt.Errorf("mount source %q: %w", mount.Host, err)
		}
		if real != mount.Host {
			return fmt.Errorf("mount source %q changed to a symlink; set --mount again to confirm its target", mount.Host)
		}
		if err := p.ValidateProject(real); err != nil {
			return fmt.Errorf("mount source %q: %w", mount.Host, err)
		}
	}
	return nil
}
