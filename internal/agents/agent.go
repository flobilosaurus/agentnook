// Package agents defines the agent contract. Concrete adapters and their registry
// depend on this package, never the other way around.
package agents

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
)

const GuestHome = "/root"
const TelemetryDir = "/run/agentnook-telemetry"

type Spec struct{ Name, Label, Package string }

// Agent owns every agent-specific installation, launch and telemetry decision.
// Plans describe guest inputs; adapters never create host files or launch processes.
type Agent interface {
	Spec() Spec
	ValidateVersion(string) error
	InstallScript() string
	Environment() []string
	Command([]string) []string
	Telemetry(tty bool, pass []string) *TelemetryPlan
	ReservedPaths() []string
}

type TelemetryMount struct {
	Source   string // Relative to the private per-run telemetry directory.
	Guest    string
	ReadOnly bool
}
type TelemetryPlan struct {
	Files       map[string][]byte
	Directories []string
	Mounts      []TelemetryMount
	Command     []string // Workload prefix; launcher appends original agent arguments unchanged.
}

// Catalog is an injected collection, not a global registry or service locator.
type Catalog []Agent

func (c Catalog) Lookup(name string) (Agent, error) {
	for _, a := range c {
		if a.Spec().Name == name {
			return a, nil
		}
	}
	return nil, fmt.Errorf("unknown agent %q: choose %s", name, strings.Join(c.Names(), ", "))
}
func (c Catalog) Names() []string {
	names := make([]string, 0, len(c))
	for _, a := range c {
		names = append(names, a.Spec().Name)
	}
	return names
}
func (c Catalog) Label(name string) string {
	if a, err := c.Lookup(name); err == nil {
		return a.Spec().Label
	}
	return name
}
func (c Catalog) ValidateVersion(name, version string) error {
	a, err := c.Lookup(name)
	if err != nil {
		return err
	}
	return a.ValidateVersion(version)
}
func (c Catalog) ReservedPaths() []string {
	var paths []string
	for _, a := range c {
		paths = append(paths, a.ReservedPaths()...)
	}
	return paths
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid agent name %q", name)
	}
	return nil
}
func ValidateVersion(version string) error {
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("agent-version must be an exact version, e.g. 2.3.2")
	}
	return nil
}
func Command(binary string, pass []string) []string { return append([]string{binary}, pass...) }

// Interactive shares only flag scanning mechanics. Each adapter supplies its own policy.
func Interactive(tty bool, pass, flags, commands []string) bool {
	if !tty {
		return false
	}
	for _, arg := range pass {
		key, _, _ := strings.Cut(arg, "=")
		for _, flag := range append([]string{"--help", "-h", "--version", "-v"}, flags...) {
			if key == flag {
				return false
			}
		}
		for _, command := range commands {
			if arg == command {
				return false
			}
		}
	}
	return true
}

// InstallScript provisions a clean Node/Debian VM; AGENT_VERSION is passed as env,
// never interpolated into shell source. Only the selected package's lifecycle
// scripts are approved, never all dependency scripts. Build-only state stays outside /root.
func InstallScript(spec Spec, extra string) string {
	return `export HOME=/tmp/agentnook-build
export XDG_CONFIG_HOME="$HOME/.config" XDG_DATA_HOME="$HOME/.local/share"
export XDG_CACHE_HOME="$HOME/.cache" XDG_STATE_HOME="$HOME/.local/state"
export DISABLE_AUTOUPDATER=1 DEBIAN_FRONTEND=noninteractive
mkdir -p "$HOME" /workspace
apt-get update
apt-get install -y --no-install-recommends git ripgrep fd-find ca-certificates
rm -rf /var/lib/apt/lists/*
npm install --global --allow-scripts=` + spec.Package + ` ` + spec.Package + `@"${AGENT_VERSION}"
npm cache clean --force
` + versionCheck(spec) + extra + `rm -rf "$HOME"
`
}

// VersionCheck checks the installed binary without creating profile state.
func VersionCheck(spec Spec) string {
	return `export HOME=/tmp/agentnook-verify
mkdir -p "$HOME"
export DISABLE_AUTOUPDATER=1
` + versionCheck(spec)
}

func versionCheck(spec Spec) string {
	return `if ! actual_version="$(` + spec.Name + ` --version)"; then
  printf '%s\n' 'nook: ` + spec.Name + ` --version failed; agent installation is incomplete' >&2
  exit 1
fi
actual_version="${actual_version%% *}"
if [ "$actual_version" != "$AGENT_VERSION" ]; then
  printf 'nook: ` + spec.Name + ` version mismatch: expected %s, got %s\n' "$AGENT_VERSION" "$actual_version" >&2
  exit 1
fi
`
}

//go:embed report.mjs
var reportSource []byte

func ReportSource() []byte { return bytes.Clone(reportSource) }
