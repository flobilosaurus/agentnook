package config

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// Mount is a project-specific directory share, separate from image/VM presets.
type Mount struct {
	Host     string `toml:"host"`
	Guest    string `toml:"guest"`
	ReadOnly bool   `toml:"read_only"`
}

// ValidateMounts checks persisted syntax without accessing the filesystem: a
// missing source in another project must not prevent loading the whole config.
func ValidateMounts(mounts []Mount, reservedPaths ...string) error {
	reservedPaths = append([]string{"/root", "/run/agentnook-telemetry", "/var/run/agentnook-telemetry", "/run/agentnook-herdr", "/var/run/agentnook-herdr", "/proc", "/sys", "/dev"}, reservedPaths...)
	for i, mount := range mounts {
		if !filepath.IsAbs(mount.Host) || filepath.Clean(mount.Host) != mount.Host || invalidMountPath(mount.Host) {
			return fmt.Errorf("mount host %q must be a clean absolute path without colons or control characters", mount.Host)
		}
		if !path.IsAbs(mount.Guest) || path.Clean(mount.Guest) != mount.Guest || invalidMountPath(mount.Guest) {
			return fmt.Errorf("mount guest %q must be a clean absolute Linux path without colons or control characters", mount.Guest)
		}
		guest := guestMountPath(mount.Guest)
		// Workspace children are useful additional mounts, but its root must stay
		// intact. The profile and telemetry shares must not be covered or extended.
		if guestContains(guest, "/workspace") {
			return fmt.Errorf("mount target %q would hide the workspace mount", mount.Guest)
		}
		for _, reserved := range reservedPaths {
			if guestContains(guest, reserved) || guestContains(reserved, guest) {
				return fmt.Errorf("mount target %q overlaps reserved path %q", mount.Guest, reserved)
			}
		}
		for _, previous := range mounts[:i] {
			other := guestMountPath(previous.Guest)
			if guestContains(guest, other) || guestContains(other, guest) {
				return fmt.Errorf("mount targets %q and %q overlap", previous.Guest, mount.Guest)
			}
		}
	}
	return nil
}

func invalidMountPath(value string) bool {
	return strings.ContainsRune(value, ':') || strings.IndexFunc(value, unicode.IsControl) >= 0
}

func guestMountPath(value string) string {
	// Debian-based images use /var/run -> /run. Apply the same protection to
	// that alias, including duplicate user targets through either spelling.
	if value == "/var/run" || strings.HasPrefix(value, "/var/run/") {
		return "/run" + strings.TrimPrefix(value, "/var/run")
	}
	return value
}

func guestContains(parent, child string) bool {
	return parent == child || parent == "/" || strings.HasPrefix(child, parent+"/")
}
