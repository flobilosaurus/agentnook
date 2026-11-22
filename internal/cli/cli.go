// Package cli coordinates agent/profile selection, persisted launch options and smolvm.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/herdr"
	"github.com/flobilosaurus/agentnook/internal/images"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
	"github.com/flobilosaurus/agentnook/internal/tui"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

var Version = "dev"

const usage = `Usage: nook [-- AGENT_ARGS...]
       nook watch

Run your coding agent in a fresh local VM.
First run: choose an agent and profile; nook builds the image automatically.
Later runs: reuse this folder's saved setup without asking.

Options:
  --select                Change this folder's saved setup
  --mount HOST:GUEST[:ro]  Add directory shares; repeat to replace the saved list
  --clear-mounts          Remove saved extra directory shares
  --help-all              Show advanced agent, image and resource options
  -h, --help              Show help
  --version               Show launcher version

Examples:
  nook                    Set up once, then start with the saved choices
  nook -- --continue      Pass arguments to your saved agent
  nook watch              Watch all projects in this AGENTNOOK_HOME

Requires smolvm >= 1.33.0; Docker is not needed.
Agent logins stay in an isolated profile; host agent files are never imported.
`

const advancedUsage = `
Advanced options (not needed for normal starts):
  --agent NAME            Override the saved agent: pi, opencode or claude
  --profile NAME          Select or create a profile without prompting
  --image NAME            Select a managed image for this agent
  --select-image          Choose another built image without changing the profile
  --images                List managed builds without launching a VM
  --agent-version VERSION Exact version for initial automatic build; otherwise must match image
  --cpus N                Maximum vCPUs (default: 2)
  --mem MiB               Maximum guest memory (default: 2048)
  --storage GiB           Writable guest disk (default: 20)
  --dry-run               Print the launch plan as JSON without changes

Image management:
  nook images
  nook images build NAME --agent NAME [OPTIONS]
  nook images default NAME
  nook images remove NAME
  nook images --help

Agent, profile, image, resources and mounts are saved for the exact folder.
Each agent keeps its own saved selection; profiles can contain multiple agents.
Image defaults affect new selections, not an already configured folder.
The optional "run" subcommand and all existing launch flags remain supported.

Storage: ${AGENTNOOK_HOME:-~/.agentnook}/config.toml and profiles/<name>/<agent>/
Set AGENTNOOK_NONINTERACTIVE=1 to disable prompts; first launch needs --agent and --profile.
Interactive activity reports are automatic; unavailable reports show unknown.
AGENTNOOK_HERDR=0 disables only forwarding to Herdr.
Arguments after -- go to the selected agent unchanged and are not remembered.
Old configuration formats and --pi-* flags are not supported.
`

type App struct {
	Agents        agents.Catalog
	In            io.Reader
	Out, Err      io.Writer
	Prompter      tui.Prompter
	AgentPrompter tui.AgentPrompter
	ImagePrompter tui.ImagePrompter
	Runner        func(string, []string, vm.IO) int
	Interactive   func() bool
	LatestVersion func(string) (string, error)
	Watch         func(string, tui.FleetLoader) error
}

func (a App) Run(args []string) int {
	if a.Agents == nil {
		a.Agents = registry.All()
	}
	if a.In == nil {
		a.In = os.Stdin
	}
	if a.Out == nil {
		a.Out = os.Stdout
	}
	if a.Err == nil {
		a.Err = os.Stderr
	}
	if a.Runner == nil {
		a.Runner = vm.Launch
	}
	if a.Interactive == nil {
		a.Interactive = func() bool {
			in, iok := a.In.(*os.File)
			out, ook := a.Out.(*os.File)
			return iok && ook && term.IsTerminal(in.Fd()) && term.IsTerminal(out.Fd())
		}
	}
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			fmt.Fprint(a.Out, usage)
			return 0
		case "--help-all":
			fmt.Fprint(a.Out, usage+advancedUsage)
			return 0
		case "version", "--version":
			fmt.Fprintln(a.Out, "nook", Version)
			return 0
		case "images":
			return a.imageCommand(args[1:])
		case "watch":
			return a.watchCommand(args[1:])
		case "run":
			args = args[1:]
		}
	}
	fs := flag.NewFlagSet("nook", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	fs.Usage = func() { fmt.Fprint(a.Out, usage) }
	agentFlag := fs.String("agent", "", "agent type")
	forceSelect := fs.Bool("select", false, "change saved setup")
	helpAll := fs.Bool("help-all", false, "show advanced options")
	selectImage := fs.Bool("select-image", false, "choose a managed image")
	listImages := fs.Bool("images", false, "list managed builds")
	profileFlag := fs.String("profile", "", "select or create a profile")
	imageFlag := fs.String("image", "", "managed image")
	versionFlag := fs.String("agent-version", "", "exact initial agent version")
	cpus := fs.Int("cpus", 0, "maximum vCPUs")
	memory := fs.Int("mem", 0, "memory MiB")
	storage := fs.Int("storage", 0, "disk GiB")
	var mountValues []string
	fs.Func("mount", "extra HOST:GUEST[:ro]", func(s string) error { mountValues = append(mountValues, s); return nil })
	clearMounts := fs.Bool("clear-mounts", false, "remove saved mounts")
	dryRun := fs.Bool("dry-run", false, "print launch plan only")
	version := fs.Bool("version", false, "launcher version")
	launcherArgs, pass := args, []string(nil)
	for i, arg := range args {
		if arg == "--" {
			launcherArgs, pass = args[:i], args[i+1:]
			break
		}
	}
	if err := fs.Parse(launcherArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *helpAll {
		fmt.Fprint(a.Out, usage+advancedUsage)
		return 0
	}
	if *version {
		fmt.Fprintln(a.Out, "nook", Version)
		return 0
	}
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if fs.NArg() > 0 {
		return a.fail(2, errors.New("agent arguments must follow --"))
	}
	if *listImages {
		if len(set) != 1 || len(pass) > 0 {
			return a.fail(2, errors.New("--images cannot be combined with launch options or agent arguments"))
		}
		return a.imageCommand(nil)
	}
	if *forceSelect && set["profile"] {
		return a.fail(2, errors.New("--select and --profile cannot be combined"))
	}
	if *selectImage && set["image"] {
		return a.fail(2, errors.New("--select-image and --image cannot be combined"))
	}
	if *clearMounts && set["mount"] {
		return a.fail(2, errors.New("--mount and --clear-mounts cannot be combined"))
	}
	if set["agent"] {
		if _, err := a.Agents.Lookup(*agentFlag); err != nil {
			return a.fail(2, err)
		}
	}
	if set["profile"] {
		if err := config.ValidateProfileName(*profileFlag); err != nil {
			return a.fail(2, err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return a.fail(1, err)
	}
	project, err := config.NormalizeProjectPath(cwd)
	if err != nil {
		return a.fail(1, err)
	}
	p, err := paths.Resolve()
	if err != nil {
		return a.fail(1, err)
	}
	if err := p.ValidateProject(project); err != nil {
		return a.fail(1, err)
	}
	cfg, err := config.Load(p.ConfigFile())
	if err != nil {
		return a.fail(1, fmt.Errorf("config: %w", err))
	}
	workspace := cfg.Projects[project]
	agent := workspace.Agent
	promptOK := os.Getenv("AGENTNOOK_NONINTERACTIVE") != "1" && a.Interactive()
	if set["agent"] {
		agent = *agentFlag
	} else if agent == "" || *forceSelect {
		if !promptOK {
			return a.fail(1, errors.New("agent/profile selection needs a terminal; use --agent NAME --profile NAME"))
		}
		prompter := a.AgentPrompter
		if prompter == nil {
			prompter = tui.BubblePrompter{In: a.In, Out: a.Out, Agents: a.Agents}
		}
		agent, err = prompter.ChooseAgent(project, agent)
		if err != nil {
			return a.selectionError(err)
		}
	}
	if _, err := a.Agents.Lookup(agent); err != nil {
		return a.fail(2, err)
	}
	if set["agent-version"] {
		if err := a.Agents.ValidateVersion(agent, *versionFlag); err != nil {
			return a.fail(2, err)
		}
	}
	adapter, _ := a.Agents.Lookup(agent)
	selected, mapped := workspace.Agents[agent]
	if !mapped {
		selected.VM = cfg.DefaultOptions(agent)
	}
	if set["mount"] {
		selected.Mounts = nil
		for _, value := range mountValues {
			mount, err := paths.ParseMount(value, project)
			if err != nil {
				return a.fail(2, err)
			}
			selected.Mounts = append(selected.Mounts, mount)
		}
	} else if *clearMounts {
		selected.Mounts = nil
	}
	if err := p.ValidateMounts(selected.Mounts, a.Agents.ReservedPaths()...); err != nil {
		return a.fail(2, err)
	}
	applyResources := func() {
		if set["cpus"] {
			selected.VM.CPUs = *cpus
		}
		if set["mem"] {
			selected.VM.Memory = *memory
		}
		if set["storage"] {
			selected.VM.Storage = *storage
		}
	}
	applyResources()
	if err := selected.VM.Resources().Validate(); err != nil {
		return a.fail(2, err)
	}
	if set["image"] {
		selected.VM, err = selectBuiltImage(cfg, *imageFlag, agent)
		if err != nil {
			return a.fail(2, err)
		}
		applyResources()
	}
	binary := "smolvm"
	if !*dryRun {
		binary, err = vm.Find()
		if err != nil {
			return a.fail(127, err)
		}
	}
	if *selectImage && !promptOK {
		return a.fail(1, errors.New("image selection needs a terminal; use --image NAME"))
	}
	create := false
	switch {
	case set["profile"]:
		selected.Profile = *profileFlag
		create = !cfg.HasProfile(selected.Profile)
	case !mapped || *forceSelect:
		if !promptOK {
			return a.fail(1, errors.New("profile selection needs a terminal; use --profile NAME"))
		}
		prompter := a.Prompter
		if prompter == nil {
			prompter = tui.BubblePrompter{In: a.In, Out: a.Out, Options: selected.VM, Images: images.ForAgent(cfg.Images, agent), Agents: a.Agents, Advanced: *forceSelect}
		}
		choice, e := prompter.ChooseProfile(project, cfg.Profiles, selected.Profile)
		if e != nil {
			return a.selectionError(e)
		}
		selected.Profile, create = choice.Profile, choice.Create
		if choice.Image != "" {
			selected.VM, err = selectBuiltImage(cfg, choice.Image, agent)
			if err != nil {
				return a.fail(1, err)
			}
			applyResources()
		}
	}
	if err := config.ValidateProfileName(selected.Profile); err != nil {
		return a.fail(1, err)
	}
	if create && cfg.HasProfile(selected.Profile) {
		return a.fail(1, fmt.Errorf("profile %q already exists", selected.Profile))
	}
	if !create && !cfg.HasProfile(selected.Profile) {
		return a.fail(1, fmt.Errorf("profile %q does not exist", selected.Profile))
	}
	if len(images.ForAgent(cfg.Images, agent)) == 0 {
		if *dryRun {
			return a.fail(1, fmt.Errorf("no %s image; run nook without --dry-run to build it", agent))
		}
		resolve := a.LatestVersion
		if resolve == nil {
			resolve = a.latestVersion
		}
		cfg, err = images.Bootstrap(p, vm.IO{In: a.In, Out: a.Err, Err: a.Err}, adapter, *versionFlag, resolve)
		if err != nil {
			return a.fail(1, err)
		}
	}
	if selected.VM.Image == "" {
		selected.VM, err = defaultImageOptions(cfg, agent)
		if err != nil {
			return a.fail(1, err)
		}
		applyResources()
	}
	if *selectImage {
		prompter := a.ImagePrompter
		if prompter == nil {
			prompter = tui.BubblePrompter{In: a.In, Out: a.Out, Agents: a.Agents}
		}
		name, e := prompter.ChooseImage(project, selected.VM, images.ForAgent(cfg.Images, agent))
		if e != nil {
			return a.selectionError(e)
		}
		if name != "" {
			selected.VM, err = selectBuiltImage(cfg, name, agent)
			if err != nil {
				return a.fail(1, err)
			}
			applyResources()
		}
	}
	if err := selected.VM.Validate(); err != nil {
		return a.fail(2, err)
	}
	if err := adapter.ValidateVersion(selected.VM.AgentVersion); err != nil {
		return a.fail(2, err)
	}
	managedImage, managed := images.FindByReference(cfg.Images, selected.VM.Image)
	if !managed || managedImage.VM.Agent != agent {
		return a.fail(1, errors.New("launch requires a managed image for the selected agent"))
	}
	// Version is image metadata, not a runtime install override.
	if selected.VM.AgentVersion != managedImage.VM.AgentVersion || (set["agent-version"] && *versionFlag != managedImage.VM.AgentVersion) {
		return a.fail(2, errors.New("agent version differs from image; build and select a new image with nook images build NAME --agent NAME --agent-version VERSION"))
	}
	if err := usableImage(managedImage); err != nil {
		return a.fail(1, err)
	}
	vmArgs := vm.Args(project, p.AgentDir(selected.Profile, agent), selected.VM, adapter, a.Interactive(), pass, selected.Mounts...)
	if *dryRun {
		data, err := json.MarshalIndent(append([]string{"smolvm"}, vmArgs...), "", "  ")
		if err != nil {
			return a.fail(1, err)
		}
		fmt.Fprintln(a.Out, string(data))
		return 0
	}
	err = config.Update(p.ConfigFile(), func(current *config.Config) error {
		if err := p.ValidateMounts(selected.Mounts, a.Agents.ReservedPaths()...); err != nil {
			return err
		}
		latest, found := current.FindImage(managedImage.Name)
		if !found || latest.VM != managedImage.VM {
			return fmt.Errorf("image %q was removed or replaced; select another image", managedImage.Name)
		}
		if err := usableImage(latest); err != nil {
			return err
		}
		if create && !current.HasProfile(selected.Profile) {
			if err := current.AddProfile(selected.Profile); err != nil {
				return err
			}
		}
		if !current.HasProfile(selected.Profile) {
			return fmt.Errorf("profile %q no longer exists", selected.Profile)
		}
		if _, err := p.EnsureAgentDir(selected.Profile, agent); err != nil {
			return err
		}
		w := current.Projects[project]
		if w.Agents == nil {
			w.Agents = make(map[string]config.Project)
		}
		w.Agent = agent
		w.Agents[agent] = selected
		current.Projects[project] = w
		return nil
	})
	if err != nil {
		return a.fail(1, fmt.Errorf("save profile: %w", err))
	}
	fmt.Fprintf(a.Err, "nook: %s\nAgent: %s\nProfile: %s\n%s\n", project, agent, selected.Profile, images.Summary(selected.VM, cfg.Images, a.Agents))
	for _, mount := range selected.Mounts {
		mode := "read-write"
		if mount.ReadOnly {
			mode = "read-only"
		}
		fmt.Fprintf(a.Err, "Mount: %s -> %s (%s)\n", mount.Host, mount.Guest, mode)
	}
	launch, err := telemetry.Prepare(adapter, a.Interactive(), pass)
	if err != nil {
		fmt.Fprintln(a.Err, "nook: activity reporting unavailable; starting agent without telemetry.")
	} else if launch != nil {
		defer launch.Close()
		vmArgs = launch.Args(vmArgs)
		bridge, err := herdr.Prepare(launch)
		if err != nil {
			fmt.Fprintln(a.Err, "nook: Herdr reporting unavailable; telemetry remains enabled.")
		} else if bridge != nil {
			defer bridge.Close()
			bridge.Start()
		}
	}
	return a.Runner(binary, vmArgs, vm.IO{In: a.In, Out: a.Out, Err: a.Err})
}
func (a App) latestVersion(name string) (string, error) {
	adapter, err := a.Agents.Lookup(name)
	if err != nil {
		return "", err
	}
	return images.LatestVersion(adapter)
}

func (a App) selectionError(err error) int {
	if errors.Is(err, tui.ErrCancelled) {
		return a.fail(130, err)
	}
	return a.fail(1, err)
}
func (a App) fail(code int, err error) int { fmt.Fprintln(a.Err, "nook:", err); return code }
