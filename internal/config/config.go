// Package config stores versioned agent, profile and exact host-directory mappings.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"golang.org/x/sys/unix"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

const SchemaVersion = 1

type Profile struct {
	Name string `toml:"name"`
}

// Resources are independent of the agent installed in an image.
type Resources struct {
	CPUs    int `toml:"cpus"`
	Memory  int `toml:"memory_mib"`
	Storage int `toml:"storage_gib"`
}

// Options is a launch snapshot, including immutable image identity and resource overrides.
type Options struct {
	Image        string `toml:"image"`
	Agent        string `toml:"agent"`
	AgentVersion string `toml:"agent_version"`
	CPUs         int    `toml:"cpus"`
	Memory       int    `toml:"memory_mib"`
	Storage      int    `toml:"storage_gib"`
}

func (o Options) Resources() Resources { return Resources{o.CPUs, o.Memory, o.Storage} }
func (r Resources) Validate() error {
	if r.CPUs < 1 || r.Memory < 1 || r.Storage < 1 {
		return errors.New("cpus, mem (MiB), and storage (GiB) must be positive")
	}
	return nil
}

type Project struct {
	Profile string  `toml:"profile"`
	VM      Options `toml:"vm"`
	Mounts  []Mount `toml:"mounts,omitempty"`
}

type Workspace struct {
	Agent  string             `toml:"agent"`
	Agents map[string]Project `toml:"agents"`
}

type BuiltImage struct {
	Name         string  `toml:"name"`
	BaseImage    string  `toml:"base_image"`
	Architecture string  `toml:"architecture"`
	BuiltAt      string  `toml:"built_at"`
	VM           Options `toml:"vm"`
}

type Config struct {
	Version       int                  `toml:"version"`
	Resources     Resources            `toml:"resources"`
	DefaultImages map[string]string    `toml:"default_images"`
	Images        []BuiltImage         `toml:"images"`
	Profiles      []Profile            `toml:"profiles"`
	Projects      map[string]Workspace `toml:"projects"`
}

const BaseImage = "node:24-bookworm"

// Defaults supplies resource presets, not an installed agent or image version.
func Defaults() Options { return Options{CPUs: 2, Memory: 2048, Storage: 20} }
func (c Config) DefaultOptions(agent string) Options {
	o := Defaults()
	o.Agent = agent
	o.CPUs, o.Memory, o.Storage = c.Resources.CPUs, c.Resources.Memory, c.Resources.Storage
	if e, ok := c.FindImage(c.DefaultImages[agent]); ok {
		o = e.VM
	}
	return o
}
func Empty() Config {
	return Config{Version: SchemaVersion, Resources: Defaults().Resources(), DefaultImages: make(map[string]string), Projects: make(map[string]Workspace)}
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidateProfileName(name string) error {
	if !namePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid profile name %q: use 1–64 letters, numbers, dots, underscores or dashes; start with a letter or number; no '..'", name)
	}
	return nil
}
func ValidateImageReference(image string) error {
	if strings.TrimSpace(image) != image || image == "" || image == "-" || strings.ContainsAny(image, "\x00\r\n") {
		return errors.New("image must be a non-empty image reference, not stdin ('-')")
	}
	return nil
}
func (o Options) Validate() error {
	if err := agents.ValidateName(o.Agent); err != nil {
		return err
	}
	if err := ValidateImageReference(o.Image); err != nil {
		return err
	}
	if err := agents.ValidateVersion(o.AgentVersion); err != nil {
		return err
	}
	return o.Resources().Validate()
}
func NormalizeProjectPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("project path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project %q is not a directory", path)
	}
	return filepath.Clean(real), nil
}
func (c Config) FindImage(name string) (BuiltImage, bool) {
	for _, entry := range c.Images {
		if strings.EqualFold(entry.Name, name) {
			return entry, true
		}
	}
	return BuiltImage{}, false
}
func (c *Config) AddImage(entry BuiltImage) error {
	if _, found := c.FindImage(entry.Name); found {
		return fmt.Errorf("image %q already exists; choose a new build name", entry.Name)
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	c.Images = append(c.Images, entry)
	sort.Slice(c.Images, func(i, j int) bool { return c.Images[i].Name < c.Images[j].Name })
	return nil
}
func (entry BuiltImage) Validate() error {
	if err := ValidateProfileName(entry.Name); err != nil {
		return fmt.Errorf("image name: %w", err)
	}
	if err := ValidateImageReference(entry.BaseImage); err != nil {
		return fmt.Errorf("image %q base: %w", entry.Name, err)
	}
	if entry.Architecture != "arm64" && entry.Architecture != "amd64" {
		return fmt.Errorf("image %q: architecture must be arm64 or amd64", entry.Name)
	}
	if _, err := time.Parse(time.RFC3339, entry.BuiltAt); err != nil {
		return fmt.Errorf("image %q: invalid build timestamp", entry.Name)
	}
	if !filepath.IsAbs(entry.VM.Image) || filepath.Clean(entry.VM.Image) != entry.VM.Image || !strings.HasSuffix(entry.VM.Image, ".smolmachine") {
		return fmt.Errorf("image %q: artifact must be a clean absolute .smolmachine path; old .tar images are not supported; back up config.toml, remove old image/default/project selections and rebuild with nook images build NAME --agent %s (keep profiles/ for existing logins)", entry.Name, entry.VM.Agent)
	}
	return entry.VM.Validate()
}
func (c Config) HasProfile(name string) bool {
	for _, p := range c.Profiles {
		if p.Name == name {
			return true
		}
	}
	return false
}
func (c *Config) AddProfile(name string) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	for _, p := range c.Profiles {
		if strings.EqualFold(p.Name, name) {
			return fmt.Errorf("profile %q already exists (profile names must also be unique ignoring case)", name)
		}
	}
	c.Profiles = append(c.Profiles, Profile{Name: name})
	sort.Slice(c.Profiles, func(i, j int) bool { return c.Profiles[i].Name < c.Profiles[j].Name })
	return nil
}
func (c Config) Validate() error {
	if c.Version != SchemaVersion {
		return fmt.Errorf("unsupported config version %d (expected %d); old configurations are not migrated", c.Version, SchemaVersion)
	}
	if err := c.Resources.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, e := range c.Images {
		if err := e.Validate(); err != nil {
			return err
		}
		key := strings.ToLower(e.Name)
		if seen[key] {
			return fmt.Errorf("duplicate image %q", e.Name)
		}
		seen[key] = true
	}
	for agent, name := range c.DefaultImages {
		if err := agents.ValidateName(agent); err != nil {
			return err
		}
		e, ok := c.FindImage(name)
		if !ok || e.VM.Agent != agent {
			return fmt.Errorf("default image %q does not contain agent %q", name, agent)
		}
	}
	seen = make(map[string]bool)
	for _, p := range c.Profiles {
		if err := ValidateProfileName(p.Name); err != nil {
			return err
		}
		key := strings.ToLower(p.Name)
		if seen[key] {
			return fmt.Errorf("duplicate profile %q", p.Name)
		}
		seen[key] = true
	}
	for path, workspace := range c.Projects {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("project key %q must be a clean absolute path", path)
		}
		if _, ok := workspace.Agents[workspace.Agent]; !ok {
			return fmt.Errorf("project %q has no selection for active agent %q", path, workspace.Agent)
		}
		for agent, project := range workspace.Agents {
			if err := agents.ValidateName(agent); err != nil {
				return err
			}
			if project.VM.Agent != agent {
				return fmt.Errorf("project %q: image agent differs from selection %q", path, agent)
			}
			if !c.HasProfile(project.Profile) {
				return fmt.Errorf("project %q refers to unknown profile %q", path, project.Profile)
			}
			if err := project.VM.Validate(); err != nil {
				return fmt.Errorf("project %q: %w", path, err)
			}
			if err := ValidateMounts(project.Mounts); err != nil {
				return fmt.Errorf("project %q: %w", path, err)
			}
		}
	}
	return nil
}
func Load(path string) (Config, error) {
	c := Empty()
	metadata, err := toml.DecodeFile(path, &c)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, err
	}
	if !metadata.IsDefined("version") {
		return Config{}, errors.New("config version missing; old configurations are not migrated (use a new AGENTNOOK_HOME)")
	}
	if unknown := metadata.Undecoded(); len(unknown) > 0 {
		return Config{}, fmt.Errorf("unknown config key %q", unknown[0])
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	sort.Slice(c.Profiles, func(i, j int) bool { return c.Profiles[i].Name < c.Profiles[j].Name })
	return c, nil
}
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := toml.NewEncoder(file).Encode(c); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
func Update(path string, change func(*Config) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	c, err := Load(path)
	if err != nil {
		return err
	}
	if err := change(&c); err != nil {
		return err
	}
	return Save(path, c)
}
