package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/images"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

const imagesUsage = `Usage:
  nook images                       List managed builds and launch presets
  nook images build NAME [OPTIONS]  Build an agent image and save a local .smolmachine artifact
  nook images default NAME          Set default image for its agent type
  nook images remove NAME           Delete an unused managed artifact and catalog entry

Build options:
  --agent NAME             pi, opencode or claude (required)
  --agent-version VERSION  Exact version (default: latest stable)
  --base-image IMAGE       Debian-based Node image (default: node:24-bookworm)
  --cpus N                 CPU launch preset (default: configured resources)
  --mem MiB                RAM launch preset
  --storage GiB            Disk launch preset
  --set-default            Use successful build as default for this agent

Builds and launches require smolvm >= 1.33.0; Docker is not needed.
Native Linux architecture only. Build VMs receive no project/profile mounts.
Build names are immutable; use a new name for each rebuild.
Remove refuses images used by any agent default or saved project/agent selection.
Profiles and smolvm caches stay untouched.
`

func (a App) imageCommand(args []string) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		fmt.Fprint(a.Out, imagesUsage)
		return 0
	}
	p, err := paths.Resolve()
	if err != nil {
		return a.fail(1, err)
	}
	c, err := config.Load(p.ConfigFile())
	if err != nil {
		return a.fail(1, fmt.Errorf("config: %w", err))
	}
	if len(args) == 0 {
		current := ""
		if cwd, err := os.Getwd(); err == nil {
			if project, err := config.NormalizeProjectPath(cwd); err == nil {
				w := c.Projects[project]
				current = w.Agents[w.Agent].VM.Image
			}
		}
		if err := images.List(a.Out, c, current); err != nil {
			return a.fail(1, err)
		}
		return 0
	}
	switch args[0] {
	case "build":
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprint(a.Out, imagesUsage)
			return 0
		}
		if len(args) < 2 {
			return a.fail(2, errors.New("usage: nook images build NAME --agent NAME [OPTIONS]"))
		}
		fs := flag.NewFlagSet("images build", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		fs.Usage = func() { fmt.Fprint(a.Out, imagesUsage) }
		agent := fs.String("agent", "", "agent type")
		version := fs.String("agent-version", "", "exact agent version")
		base := fs.String("base-image", config.BaseImage, "Debian-based Node base image")
		cpus := fs.Int("cpus", c.Resources.CPUs, "CPU launch preset")
		memory := fs.Int("mem", c.Resources.Memory, "RAM launch preset")
		storage := fs.Int("storage", c.Resources.Storage, "disk launch preset")
		setDefault := fs.Bool("set-default", false, "default build for this agent")
		if err := fs.Parse(args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if fs.NArg() != 0 {
			return a.fail(2, fmt.Errorf("unexpected build arguments: %q", fs.Args()))
		}
		adapter, err := a.Agents.Lookup(*agent)
		if err != nil {
			return a.fail(2, err)
		}
		if err := config.ValidateProfileName(args[1]); err != nil {
			return a.fail(2, err)
		}
		if err := config.ValidateImageReference(*base); err != nil {
			return a.fail(2, err)
		}
		if err := (config.Resources{CPUs: *cpus, Memory: *memory, Storage: *storage}).Validate(); err != nil {
			return a.fail(2, err)
		}
		if _, found := c.FindImage(args[1]); found {
			return a.fail(1, fmt.Errorf("image %q already exists; choose a new build name", args[1]))
		}
		if *version == "" {
			resolve := a.LatestVersion
			if resolve == nil {
				resolve = a.latestVersion
			}
			*version, err = resolve(*agent)
			if err != nil {
				return a.fail(1, err)
			}
		}
		if err := adapter.ValidateVersion(*version); err != nil {
			return a.fail(2, err)
		}
		o := config.Options{Image: *base, Agent: *agent, AgentVersion: *version, CPUs: *cpus, Memory: *memory, Storage: *storage}
		if err := o.Validate(); err != nil {
			return a.fail(2, err)
		}
		entry, err := images.Build(p, images.BuildOptions{Name: args[1], BaseImage: *base, VM: o, Adapter: adapter}, vm.IO{In: a.In, Out: a.Out, Err: a.Err}, *setDefault)
		if err != nil {
			return a.fail(1, err)
		}
		fmt.Fprintf(a.Out, "Built %s\n%s\nArtifact: %s\n", entry.Name, images.Summary(entry.VM, []config.BuiltImage{entry}, a.Agents), entry.VM.Image)
		return 0
	case "default":
		if len(args) != 2 {
			return a.fail(2, errors.New("usage: nook images default NAME"))
		}
		if entry, ok := c.FindImage(args[1]); !ok {
			return a.fail(1, fmt.Errorf("unknown managed image %q", args[1]))
		} else if err := usableImage(entry); err != nil {
			return a.fail(1, err)
		}
		err := config.Update(p.ConfigFile(), func(c *config.Config) error {
			entry, ok := c.FindImage(args[1])
			if !ok {
				return fmt.Errorf("unknown managed image %q", args[1])
			}
			if err := usableImage(entry); err != nil {
				return err
			}
			c.DefaultImages[entry.VM.Agent] = entry.Name
			return nil
		})
		if err != nil {
			return a.fail(1, err)
		}
		entry, _ := c.FindImage(args[1])
		fmt.Fprintf(a.Out, "Default image for %s: %s (new selections only)\n", entry.VM.Agent, entry.Name)
		return 0
	case "remove":
		if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprint(a.Out, imagesUsage)
			return 0
		}
		if len(args) != 2 {
			return a.fail(2, errors.New("usage: nook images remove NAME"))
		}
		if err := config.ValidateProfileName(args[1]); err != nil {
			return a.fail(2, err)
		}
		entry, err := images.Remove(p, args[1])
		if err != nil {
			return a.fail(1, err)
		}
		fmt.Fprintf(a.Out, "Removed image: %s\n", entry.Name)
		return 0
	default:
		return a.fail(2, fmt.Errorf("unknown images command %q; use nook images --help", args[0]))
	}
}

func usableImage(entry config.BuiltImage) error {
	if entry.Architecture != runtime.GOARCH {
		return fmt.Errorf("image %q targets %s, host requires %s", entry.Name, entry.Architecture, runtime.GOARCH)
	}
	if !images.Available(entry) {
		return fmt.Errorf("image %q artifact is missing, empty or not a regular file: %s", entry.Name, entry.VM.Image)
	}
	return nil
}
func selectBuiltImage(c config.Config, name, agent string) (config.Options, error) {
	entry, found := c.FindImage(name)
	if !found {
		entry, found = images.FindByReference(c.Images, name)
	}
	if !found {
		return config.Options{}, fmt.Errorf("unknown managed image %q; use a name from nook images", name)
	}
	if entry.VM.Agent != agent {
		return config.Options{}, fmt.Errorf("image %q contains %s, not %s", entry.Name, entry.VM.Agent, agent)
	}
	if err := usableImage(entry); err != nil {
		return config.Options{}, err
	}
	return entry.VM, nil
}
func defaultImageOptions(c config.Config, agent string) (config.Options, error) {
	if name := c.DefaultImages[agent]; name != "" {
		return selectBuiltImage(c, name, agent)
	}
	for _, entry := range images.ForAgent(c.Images, agent) {
		if usableImage(entry) == nil {
			return entry.VM, nil
		}
	}
	return config.Options{}, fmt.Errorf("no usable %s image; build one with nook images build NAME --agent %s --set-default", agent, agent)
}
