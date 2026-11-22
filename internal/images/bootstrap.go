package images

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

// LatestVersion resolves a stable npm release once. Builds always pin exact versions.
func LatestVersion(adapter agents.Agent) (string, error) {
	return latestVersion(&http.Client{Timeout: 30 * time.Second}, "https://registry.npmjs.org/"+url.PathEscape(adapter.Spec().Package)+"/latest", adapter)
}
func latestVersion(client *http.Client, endpoint string, adapter agents.Agent) (string, error) {
	agent := adapter.Spec().Name
	response, err := client.Get(endpoint)
	if err != nil {
		return "", fmt.Errorf("resolve latest %s version: %w", agent, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("resolve latest %s version: HTTP %d", agent, response.StatusCode)
	}
	var release struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&release); err != nil {
		return "", fmt.Errorf("decode %s release: %w", agent, err)
	}
	if err := adapter.ValidateVersion(release.Version); err != nil || strings.Contains(release.Version, "-") {
		return "", fmt.Errorf("npm returned invalid stable %s version %q", agent, release.Version)
	}
	return release.Version, nil
}

func ForAgent(entries []config.BuiltImage, agent string) []config.BuiltImage {
	var result []config.BuiltImage
	for _, e := range entries {
		if e.VM.Agent == agent {
			result = append(result, e)
		}
	}
	return result
}

// Bootstrap builds only when this agent has no catalog entries. Other agents' images
// never suppress provisioning. The lock serializes bootstrap, not ordinary launches.
func Bootstrap(p paths.Paths, streams vm.IO, adapter agents.Agent, version string, resolve func(string) (string, error)) (config.Config, error) {
	agent := adapter.Spec().Name
	c, err := config.Load(p.ConfigFile())
	if err != nil || len(ForAgent(c.Images, agent)) != 0 {
		return c, err
	}
	if _, err := vm.Find(); err != nil {
		return config.Config{}, err
	}
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return config.Config{}, err
	}
	lock, err := os.OpenFile(filepath.Join(p.Root, ".bootstrap.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return config.Config{}, err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return config.Config{}, err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	c, err = config.Load(p.ConfigFile())
	if err != nil || len(ForAgent(c.Images, agent)) != 0 {
		return c, err
	}
	name := agent + "-default"
	if _, found := c.FindImage(name); found {
		return config.Config{}, fmt.Errorf("bootstrap name %q is already used by another agent; build a named %s image", name, agent)
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	fmt.Fprintf(streams.Err, "nook: no %s image; building %q.\n", agent, name)
	if version == "" {
		version, err = resolve(agent)
		if err != nil {
			return config.Config{}, err
		}
	}
	o := c.DefaultOptions(agent)
	o.AgentVersion = version
	if _, err := Build(p, BuildOptions{Name: name, BaseImage: config.BaseImage, VM: o, Adapter: adapter}, streams, true); err != nil {
		return config.Config{}, fmt.Errorf("build initial %s image: %w", agent, err)
	}
	return config.Load(p.ConfigFile())
}
