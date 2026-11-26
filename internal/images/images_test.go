package images

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

func testAgent(t *testing.T, name string) agents.Agent {
	t.Helper()
	a, err := registry.All().Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func testOptions() config.Options {
	o := config.Defaults()
	o.Agent = "pi"
	o.Image = config.BaseImage
	o.AgentVersion = "1.4.0"
	return o
}
func testWorkspace(profile string, o config.Options) config.Workspace {
	return config.Workspace{Agent: o.Agent, Agents: map[string]config.Project{o.Agent: {Profile: profile, VM: o}}}
}

func smolvmStub(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "smolvm.log")
	t.Setenv("AGENTNOOK_TEST_SMOLVM_LOG", log)
	for _, key := range []string{"CREATE_EXIT", "START_EXIT", "BUILD_EXIT", "STOP_EXIT", "PACK_EXIT", "VERIFY_EXIT", "DELETE_EXIT"} {
		t.Setenv("AGENTNOOK_TEST_"+key, "0")
	}
	t.Setenv("AGENTNOOK_TEST_EMPTY_ARTIFACT", "")
	script, err := os.ReadFile("../../test/e2e/testdata/build-smolvm.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "smolvm"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

func buildFixture(t *testing.T) (paths.Paths, BuildOptions) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := paths.Paths{Root: filepath.Join(root, "data")}
	options := testOptions()
	options.CPUs, options.Memory = 2, 2048
	return p, BuildOptions{Name: "work-v1", BaseImage: "node:24-bookworm", VM: options, Adapter: testAgent(t, "pi")}
}

func TestBuildRegistersArchiveAndDefaultWithoutProjectContext(t *testing.T) {
	log := smolvmStub(t)
	p, options := buildFixture(t)
	var output bytes.Buffer
	entry, err := Build(p, options, vm.IO{Out: &output, Err: &output}, true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Images) != 1 || !reflect.DeepEqual(c.Images[0], entry) || c.DefaultImages["pi"] != entry.Name {
		t.Fatalf("wrong catalog/default: %+v", c)
	}
	if entry.VM.CPUs != 2 || entry.VM.Memory != 2048 || entry.Architecture != runtime.GOARCH {
		t.Fatalf("wrong build metadata: %+v", entry)
	}
	if !Available(entry) {
		t.Fatal("exported image unavailable")
	}
	archive, err := os.Stat(entry.VM.Image)
	if err != nil || archive.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode: %v, %v", archive, err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"machine\ncreate\n", "--image\nnode:24-bookworm",
		"AGENT_VERSION=1.4.0", "machine\nexec\n",
		`actual_version="$(pi --version)"`, "pack\ncreate\n--from-vm", "machine\nrun\n--from", "machine\ndelete\n"} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("missing %q in %s", text, data)
		}
	}
	if strings.Contains(string(data), "--volume") || strings.Contains(string(data), "--ssh-agent") || strings.Contains(string(data), "--secret") {
		t.Fatal("build received project/profile mounts or secrets")
	}
	files, err := os.ReadDir(filepath.Dir(entry.VM.Image))
	if err != nil || len(files) != 1 || files[0].Name() != "image.smolmachine" {
		t.Fatal("build retained temporary runtime or context", files, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(entry.VM.Image), "context")); !os.IsNotExist(err) {
		t.Fatal("temporary context not cleaned up")
	}

	before, _ := os.ReadFile(p.ConfigFile())
	if _, err := Build(p, options, vm.IO{Out: &output, Err: &output}, true); err == nil {
		t.Fatal("duplicate name replaced immutable build")
	}
	after, _ := os.ReadFile(p.ConfigFile())
	calls, _ := os.ReadFile(log)
	if !bytes.Equal(before, after) || !bytes.Equal(calls, data) {
		t.Fatal("duplicate build changed config or invoked smolvm")
	}
}

func TestBuildFailureDoesNotRegisterOrChangeDefaults(t *testing.T) {
	for _, failure := range []string{"create", "start", "build", "stop", "pack", "verify", "delete", "empty"} {
		t.Run(failure, func(t *testing.T) {
			smolvmStub(t)
			p, options := buildFixture(t)
			c := config.Empty()
			c.Resources.Memory = 4096
			if err := c.AddProfile("existing"); err != nil {
				t.Fatal(err)
			}
			c.Projects["/existing"] = testWorkspace("existing", testOptions())
			if err := config.Save(p.ConfigFile(), c); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(p.ConfigFile())
			if failure == "empty" {
				t.Setenv("AGENTNOOK_TEST_EMPTY_ARTIFACT", "1")
			} else {
				t.Setenv("AGENTNOOK_TEST_"+strings.ToUpper(failure)+"_EXIT", "7")
			}
			var output bytes.Buffer
			if _, err := Build(p, options, vm.IO{Out: &output, Err: &output}, true); err == nil {
				t.Fatal("failed build accepted")
			}
			after, _ := os.ReadFile(p.ConfigFile())
			if !bytes.Equal(before, after) {
				t.Fatal("failed build changed config")
			}
			entries, err := os.ReadDir(filepath.Join(p.Root, "images"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed build leaked archive/context: %v %v", entries, err)
			}
		})
	}
}

func TestBuildKeepsExistingMappingsAndDefaults(t *testing.T) {
	smolvmStub(t)
	p, options := buildFixture(t)
	c := config.Empty()
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	c.Projects["/project"] = testWorkspace("work", testOptions())
	if err := config.Save(p.ConfigFile(), c); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := Build(p, options, vm.IO{Out: &output, Err: &output}, false); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.DefaultImages, c.DefaultImages) || !reflect.DeepEqual(got.Projects, c.Projects) || !reflect.DeepEqual(got.Profiles, c.Profiles) {
		t.Fatalf("build changed unrelated config: %+v", got)
	}
}

func TestBuildInvalidOptionsAndMissingSmolvmWriteNothing(t *testing.T) {
	for _, change := range []func(*BuildOptions){
		func(o *BuildOptions) { o.Name = "../escape" },
		func(o *BuildOptions) { o.BaseImage = "" },
		func(o *BuildOptions) { o.VM.AgentVersion = "latest" },
		func(o *BuildOptions) { o.VM.CPUs = 0 },
		func(o *BuildOptions) {}, // Valid options, but no smolvm on PATH.
	} {
		p, options := buildFixture(t)
		t.Setenv("PATH", "")
		change(&options)
		if _, err := Build(p, options, vm.IO{}, false); err == nil {
			t.Fatal("invalid options or missing smolvm accepted")
		}
		if _, err := os.Stat(p.Root); !os.IsNotExist(err) {
			t.Fatal("failed validation created storage")
		}
	}
}

func TestBuildRejectsSymlinkStorage(t *testing.T) {
	smolvmStub(t)
	p, options := buildFixture(t)
	if err := os.Mkdir(p.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(p.Root, "images")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(p, options, vm.IO{}, false); err == nil {
		t.Fatal("symlink image store accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrote through symlink: %v %v", entries, err)
	}
}

func TestListAndSummary(t *testing.T) {
	smolvmStub(t)
	p, options := buildFixture(t)
	var output bytes.Buffer
	entry, err := Build(p, options, vm.IO{Out: &output, Err: &output}, true)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := config.Load(p.ConfigFile())
	output.Reset()
	if err := List(&output, c, entry.VM.Image); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"work-v1", "node:24-bookworm", "1.4.0", "2048", "ready", "DEFAULT", "CURRENT"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("missing %q: %s", text, &output)
		}
	}
	if err := os.Remove(entry.VM.Image); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := List(&output, c, ""); err != nil || !strings.Contains(output.String(), "missing") {
		t.Fatal("missing archive not indicated")
	}
	options.VM = entry.VM
	options.VM.Memory = 8192
	options.VM.AgentVersion = "9.9.9" // Does not change the version already baked into the archive.
	summary := Summary(options.VM, c.Images, registry.All())
	if !strings.Contains(summary, "Pi: 1.4.0 (built)") || !strings.Contains(summary, "RAM: 8192 MiB") {
		t.Fatalf("summary confuses preset/runtime values: %s", summary)
	}
	output.Reset()
	if err := List(&output, config.Empty(), ""); err != nil || !strings.Contains(output.String(), "No managed builds") {
		t.Fatal("missing empty state")
	}
}
