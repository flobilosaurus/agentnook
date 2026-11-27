// Package cli coordinates agent/profile selection, persisted launch options and smolvm.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/herdr"
	"github.com/flobilosaurus/agentnook/internal/images"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
	"github.com/flobilosaurus/agentnook/internal/tui"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

var Version = "dev"

const usage = `Usage: nook [LAUNCH_OPTIONS] [-- AGENT_ARGS...]
       nook <command> [args...]

Start an agent in a local VM. First run sets up this folder;
next runs reuse the saved setup. Use --select to change it.

Commands:
  nook images   List and manage agent images
  nook watch    Monitor VMs, network activity and images
  nook network  Review outbound approvals and audit history

Everything after -- is passed unchanged to your agent.
Example: nook -- --continue

Use --env to forward host variables and --mount to share directories.
Run nook --help-all for syntax and all launch options.
Run nook <command> --help for command details.
`

const launchUsage = `Usage: nook [LAUNCH_OPTIONS] [-- AGENT_ARGS...]
       nook run [LAUNCH_OPTIONS] [-- AGENT_ARGS...]

Agent, profile and image:
  --select                 Change this folder's saved setup interactively
  --agent NAME             Choose pi, opencode or claude
  --profile NAME           Select or create a profile without prompting
  --image NAME             Select a managed image for this agent
  --select-image           Choose a built image without changing the profile
  --agent-version VERSION  Pin the first automatic build; otherwise match the selected image
  --images                 List managed images instead of launching (alias for nook images)

Directory shares and environment:
  --mount HOST:GUEST[:ro]   Share a directory; add :ro for read-only access
  --clear-mounts           Clear saved extra directory shares
  --env NAME               Forward a variable exported on the host (name only)
  --clear-env              Clear saved environment forwards

  Repeat --mount or --env to supply multiple entries. Each flag replaces
  its saved list. Only env names are saved; values are read at each launch.

Network:
  --network ask|full       TCP approval mode (default: full)
  --allow-private CIDR     Replace saved private-network exceptions (repeatable; full only)
  --clear-allow-private    Clear saved private-network exceptions

  Host/LAN stays blocked by default in both modes. full skips TCP approvals;
  ask needs the Approvals tab in a separate nook watch terminal.

VM resources:
  --cpus N                 Maximum vCPUs (default: 2)
  --mem MiB                Maximum guest memory (default: 2048)
  --storage GiB           Writable guest disk (default: 20)

Other options:
  --dry-run                Print the launch plan as JSON without making changes
  --version                Show launcher version
  -h, --help               Show short help
  --help-all               Show this reference

Examples:
  nook --mount ../lib:/mnt/lib:ro
  nook --env MY_VAR -- --continue

Setup is saved for the exact folder; each agent keeps its own selection.
Image defaults affect new selections, not already configured folders.
Agent args go after --, pass through unchanged, and are not saved.
Requires smolvm >= 1.36.1; Docker is not needed. Agent logins stay in
isolated profiles; host agent files are not imported.

Storage: ${AGENTNOOK_HOME:-~/.agentnook}/config.toml and profiles/<name>/<agent>/
AGENTNOOK_NONINTERACTIVE=1 disables prompts; first launch needs --agent and --profile.
AGENTNOOK_HERDR=0 disables Herdr forwarding only.

See also: nook images --help, nook watch --help, nook network --help
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
	Watch         func(tui.WatchOptions) error
	Network       func(string, tui.NetworkLoader, tui.NetworkDecider) error
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
			fmt.Fprint(a.Out, launchUsage)
			return 0
		case "version", "--version":
			fmt.Fprintln(a.Out, "nook", Version)
			return 0
		case "images":
			return a.imageCommand(args[1:])
		case "watch":
			return a.watchCommand(args[1:])
		case "network":
			return a.networkCommand(args[1:])
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
	var envNames []string
	fs.Func("env", "exported host variable name", func(s string) error { envNames = append(envNames, s); return nil })
	clearEnv := fs.Bool("clear-env", false, "remove saved environment variable forwards")
	network := fs.String("network", "", "TCP approval mode: ask or full")
	var privateValues []string
	fs.Func("allow-private", "private CIDR exception (full only)", func(s string) error { privateValues = append(privateValues, s); return nil })
	clearPrivate := fs.Bool("clear-allow-private", false, "remove saved private CIDR exceptions")
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
		fmt.Fprint(a.Out, launchUsage)
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
	if *clearPrivate && set["allow-private"] {
		return a.fail(2, errors.New("--allow-private and --clear-allow-private cannot be combined"))
	}
	if *clearEnv && set["env"] {
		return a.fail(2, errors.New("--env and --clear-env cannot be combined"))
	}
	envNames, err := checkedEnvNames(envNames)
	if err != nil {
		return a.fail(2, err)
	}
	privateCIDRs, err := config.NormalizePrivateCIDRs(privateValues)
	if err != nil {
		return a.fail(2, err)
	}
	if set["network"] {
		if *network == "" {
			return a.fail(2, errors.New("--network requires ask or full"))
		}
		if err := config.ValidateNetwork(*network); err != nil {
			return a.fail(2, err)
		}
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
	savedEnv := selected.EnvNames
	switch {
	case set["env"]:
		selected.EnvNames = envNames
	case *clearEnv:
		selected.EnvNames = nil
	default:
		selected.EnvNames, err = checkedEnvNames(savedEnv)
		if err != nil {
			return a.fail(2, fmt.Errorf("saved environment: %w (export it or run nook --clear-env)", err))
		}
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
	if set["network"] {
		selected.Network = *network
	}
	if set["allow-private"] {
		selected.AllowPrivate = privateCIDRs
	} else if *clearPrivate {
		selected.AllowPrivate = nil
	}
	if err := selected.ValidateNetwork(); err != nil {
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
		if selected.Network == "ask" {
			binary, err = vm.FindMediated()
		} else {
			binary, err = vm.Find()
		}
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
	if len(managedImage.Tools) > 0 {
		if err := config.ValidateMounts(selected.Mounts, images.MiseDir); err != nil {
			return a.fail(2, err)
		}
	}
	vmArgs := vm.Args(project, p.AgentDir(selected.Profile, agent), selected.VM, adapter, a.Interactive(), pass, selected.Mounts...)
	launchArgs := append([]string{"machine", "run"}, envForwardArgs(selected.EnvNames)...)
	if selected.Network == "ask" {
		launchArgs = append(launchArgs, "--mediated-egress")
	}
	for _, cidr := range selected.AllowPrivate {
		launchArgs = append(launchArgs, "--allow-private", cidr)
	}
	vmArgs = append(launchArgs, vmArgs[2:]...)
	if *dryRun {
		data, err := json.MarshalIndent(append([]string{"smolvm"}, images.WithTools(vmArgs, managedImage)...), "", "  ")
		if err != nil {
			return a.fail(1, err)
		}
		fmt.Fprintln(a.Out, string(data))
		return 0
	}
	err = config.Update(p.ConfigFile(), func(current *config.Config) error {
		w := current.Projects[project]
		if !set["env"] && !*clearEnv && !slices.Equal(w.Agents[agent].EnvNames, savedEnv) {
			return errors.New("saved environment forwards changed; retry launch")
		}
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
	fmt.Fprintln(a.Err, "Host/LAN: blocked (including host loopback, private networks and cloud metadata)")
	for _, cidr := range selected.AllowPrivate {
		fmt.Fprintf(a.Err, "Private-network exception: %s (all ports and transports; cloud metadata stays blocked)\n", cidr)
	}
	for _, mount := range selected.Mounts {
		mode := "read-write"
		if mount.ReadOnly {
			mode = "read-only"
		}
		fmt.Fprintf(a.Err, "Mount: %s -> %s (%s)\n", mount.Host, mount.Guest, mode)
	}
	if len(selected.EnvNames) > 0 {
		fmt.Fprintf(a.Err, "Forwarded environment: %s\n", strings.Join(selected.EnvNames, ", "))
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
	vmArgs = images.WithTools(vmArgs, managedImage)
	streams := vm.IO{In: a.In, Out: a.Out, Err: a.Err}
	historyInfo := egress.Info{Project: project, Profile: selected.Profile, Agent: agent}
	if selected.Network == "ask" {
		broker, err := egress.Start(p.Root, egress.Info{Project: project, Profile: selected.Profile, Agent: agent})
		if err != nil {
			return a.fail(1, fmt.Errorf("network broker: %w", err))
		}
		defer broker.Close()
		streams.Mediation = &vm.Mediation{Address: broker.Address(), Token: broker.Token()}
		historyInfo.Session = broker.Session()
		fmt.Fprintln(a.Err, "Network: ask — open Approvals in nook watch in another terminal with the same AGENTNOOK_HOME; unanswered TCP connections are denied after 12s.")
	}
	mode := selected.Network
	if mode == "" {
		mode = "full"
	}
	streams.Observe = func(binary, machine string) (func(bool) error, error) {
		return egress.ObserveVM(binary, machine, p.Root, historyInfo, mode)
	}
	if mode == "full" {
		fmt.Fprintln(a.Err, "Network history: denials only; smolvm does not expose allowed traffic in full mode.")
	}
	return a.Runner(binary, vmArgs, streams)
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
