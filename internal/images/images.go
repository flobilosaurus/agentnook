// Package images builds private local smolvm artifacts and presents the managed catalog.
package images

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

type BuildOptions struct {
	Name, BaseImage string
	VM              config.Options
	Adapter         agents.Agent
	Tools           []string
}

// Build registers nothing until provisioning, packing, verification and cleanup
// succeed. The build VM never receives project/profile mounts or host agent env.
func Build(p paths.Paths, options BuildOptions, streams vm.IO, setDefault bool) (result config.BuiltImage, retErr error) {
	if options.Adapter == nil || options.Adapter.Spec().Name != options.VM.Agent {
		return result, fmt.Errorf("build requires a matching agent adapter")
	}
	if err := options.Adapter.ValidateVersion(options.VM.AgentVersion); err != nil {
		return result, err
	}
	entry := config.BuiltImage{
		Name: options.Name, BaseImage: options.BaseImage, Architecture: runtime.GOARCH,
		BuiltAt: time.Now().UTC().Format(time.RFC3339), VM: options.VM, Tools: slices.Clone(options.Tools),
	}
	entry.VM.Image = filepath.Join(p.Root, "images", options.Name, "image.smolmachine")
	if err := entry.Validate(); err != nil {
		return result, err
	}
	c, err := config.Load(p.ConfigFile())
	if err != nil {
		return result, err
	}
	if _, found := c.FindImage(entry.Name); found {
		return result, fmt.Errorf("image %q already exists; choose a new build name", entry.Name)
	}
	binary, err := vm.Find()
	if err != nil {
		return result, err
	}
	for _, dir := range []string{p.Root, filepath.Join(p.Root, "images")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return result, err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return result, err
		}
		if !info.IsDir() {
			return result, fmt.Errorf("image storage %q must be a directory, not a symlink", dir)
		}
	}
	dir, err := os.MkdirTemp(filepath.Join(p.Root, "images"), entry.Name+"-")
	if err != nil {
		return result, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	entry.VM.Image = filepath.Join(dir, "image.smolmachine")
	name := "nook-build-" + strings.ToLower(rand.Text())
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	session := vm.NewSession()
	defer session.Close()
	// All build output goes to stderr, keeping CLI stdout machine-readable.
	buildIO := vm.IO{Out: streams.Err, Err: streams.Err}
	run := func(step string, args ...string) error {
		fmt.Fprintf(streams.Err, "nook: %s %q.\n", step, entry.Name)
		if code := session.Run(binary, args, buildIO); code != 0 {
			return fmt.Errorf("%s failed (exit %d); catalog unchanged", step, code)
		}
		return nil
	}
	cleaned := false
	cleanup := func() error {
		if code := vm.Run(binary, []string{"machine", "delete", "--name", name, "--force"}, buildIO); code != 0 {
			return fmt.Errorf("could not remove build VM %s; run smolvm machine delete --name %s --force", name, name)
		}
		cleaned = true
		return nil
	}
	defer func() {
		if !cleaned {
			retErr = errors.Join(retErr, cleanup())
		}
	}()
	if err := run("create build VM", "machine", "create", "--name", name,
		"--image", entry.BaseImage, "--net", "--user", "root", "--workdir", "/workspace",
		"--cpus", strconv.Itoa(entry.VM.CPUs), "--mem", strconv.Itoa(entry.VM.Memory),
		"--storage", strconv.Itoa(entry.VM.Storage), "--", "sleep", "infinity"); err != nil {
		return result, err
	}
	if err := run("start build VM", "machine", "start", "--name", name); err != nil {
		return result, err
	}
	if err := run("install agent", "machine", "exec", "--name", name, "--stream",
		"--env", "AGENT_VERSION="+entry.VM.AgentVersion, "--", "sh", "-eu", "-c", options.Adapter.InstallScript()); err != nil {
		return result, err
	}
	if len(entry.Tools) > 0 {
		args := []string{"machine", "exec", "--name", name, "--stream", "--", "/bin/sh", "-eu", "-c", installMise, "agentnook-mise"}
		if err := run("install mise tools", append(args, entry.Tools...)...); err != nil {
			return result, err
		}
	}
	if err := run("stop build VM", "machine", "stop", "--name", name); err != nil {
		return result, err
	}
	stub := filepath.Join(dir, "image")
	if err := run("pack image", "pack", "create", "--from-vm", name, "--output", stub,
		"--cpus", strconv.Itoa(entry.VM.CPUs), "--mem", strconv.Itoa(entry.VM.Memory)); err != nil {
		return result, err
	}
	// Only the sidecar is needed by the installed smolvm. Do not retain a second runtime.
	if err := os.Remove(stub); err != nil {
		return result, err
	}
	info, err := os.Lstat(entry.VM.Image)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return result, fmt.Errorf("smolvm produced an empty or non-regular artifact; catalog unchanged")
	}
	if err := os.Chmod(entry.VM.Image, 0o600); err != nil {
		return result, err
	}
	artifact, err := os.OpenFile(entry.VM.Image, os.O_RDWR, 0)
	if err != nil {
		return result, err
	}
	syncErr := artifact.Sync()
	closeErr := artifact.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return result, err
	}
	// A successful boot is not sufficient: assert installed state in a fresh VM.
	verify := agents.VersionCheck(options.Adapter.Spec())
	if len(entry.Tools) > 0 {
		verify += verifyMise
	}
	args := []string{"machine", "run", "--from", entry.VM.Image,
		"--env", "AGENT_VERSION=" + entry.VM.AgentVersion, "--", "/bin/sh", "-eu", "-c", verify}
	if err := run("verify packed image", WithTools(args, entry)...); err != nil {
		return result, err
	}
	if err := cleanup(); err != nil {
		return result, err
	}
	if err := config.Update(p.ConfigFile(), func(current *config.Config) error {
		if err := current.AddImage(entry); err != nil {
			return err
		}
		if setDefault {
			current.DefaultImages[entry.VM.Agent] = entry.Name
		}
		return nil
	}); err != nil {
		return result, err
	}
	keep = true
	return entry, nil
}

func Available(entry config.BuiltImage) bool {
	info, err := os.Lstat(entry.VM.Image)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func FindByReference(entries []config.BuiltImage, reference string) (config.BuiltImage, bool) {
	for _, entry := range entries {
		if referencesArchive(reference, entry.VM.Image) {
			return entry, true
		}
	}
	return config.BuiltImage{}, false
}

func Summary(options config.Options, entries []config.BuiltImage, catalog agents.Catalog) string {
	name, base, version := options.Image, "unknown", "unknown"
	var tools string
	if entry, found := FindByReference(entries, options.Image); found {
		name, base, version = entry.Name, entry.BaseImage, entry.VM.AgentVersion+" (built)"
		if len(entry.Tools) > 0 {
			tools = "\nTools (mise): " + strings.Join(entry.Tools, ", ")
		}
	}
	return fmt.Sprintf("Image: %s\nLinux base: %s\n%s: %s · CPU: %d · RAM: %d MiB%s", name, base, catalog.Label(options.Agent), version, options.CPUs, options.Memory, tools)
}

// List is read-only; it never pulls images or starts a VM.
func List(out io.Writer, c config.Config, current string) error {
	if len(c.Images) == 0 {
		_, err := fmt.Fprintln(out, "No managed builds. Build one with: nook images build NAME")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tAGENT\tVERSION\tLINUX BASE\tCPU\tRAM MiB\tARCH\tLOCAL\tDEFAULT\tCURRENT\tMISE TOOLS")
	for _, entry := range c.Images {
		status := "missing"
		if Available(entry) {
			status = "ready"
		}
		mark := func(value bool) string {
			if value {
				return "*"
			}
			return "-"
		}
		tools := "-"
		if len(entry.Tools) > 0 {
			tools = strings.Join(entry.Tools, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\n",
			entry.Name, entry.VM.Agent, entry.VM.AgentVersion, entry.BaseImage, entry.VM.CPUs, entry.VM.Memory,
			entry.Architecture, status, mark(strings.EqualFold(c.DefaultImages[entry.VM.Agent], entry.Name)), mark(current == entry.VM.Image), tools)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "CPU/RAM are launch presets; per-directory overrides may differ.")
	return err
}
