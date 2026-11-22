package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/tui"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

type fakePrompter struct {
	choice  tui.Choice
	err     error
	calls   int
	current string
}

func (p *fakePrompter) ChooseProfile(_ string, _ []config.Profile, current string) (tui.Choice, error) {
	p.calls++
	p.current = current
	return p.choice, p.err
}

type fakeAgentPrompter struct {
	name  string
	err   error
	calls int
}

func (p *fakeAgentPrompter) ChooseAgent(_, _ string) (string, error) { p.calls++; return p.name, p.err }

type fixture struct {
	app                  App
	prompt               *fakePrompter
	agentPrompt          *fakeAgentPrompter
	out, errOut          bytes.Buffer
	root, project, data  string
	argv                 []string
	runs, exit, resolves int
}

func setup(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{root: root, project: filepath.Join(root, "project with spaces"), data: filepath.Join(root, "data")}
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{f.project, bin} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_, source, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../test/e2e/testdata/build-smolvm.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "smolvm"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(f.project)
	t.Setenv("AGENTNOOK_HOME", f.data)
	t.Setenv("AGENTNOOK_NONINTERACTIVE", "")
	t.Setenv("PATH", bin)
	t.Setenv("AGENTNOOK_TEST_SMOLVM_LOG", filepath.Join(root, "smolvm.log"))
	t.Setenv("AGENTNOOK_TEST_BUILD_EXIT", "0")
	f.prompt = &fakePrompter{choice: tui.Choice{Profile: "work", Create: true}}
	f.agentPrompt = &fakeAgentPrompter{name: "pi"}
	f.app = App{In: strings.NewReader(""), Out: &f.out, Err: &f.errOut, Prompter: f.prompt, AgentPrompter: f.agentPrompt, Interactive: func() bool { return true },
		LatestVersion: func(agent string) (string, error) {
			f.resolves++
			if agent == "pi" {
				return "1.4.0", nil
			}
			return "2.3.2", nil
		},
		Runner: func(_ string, args []string, _ vm.IO) int {
			f.runs++
			f.argv = append([]string(nil), args...)
			return f.exit
		}}
	return f
}
func (f *fixture) load(t *testing.T) config.Config {
	t.Helper()
	c, err := config.Load(filepath.Join(f.data, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *fixture) run(t *testing.T, want int, args ...string) {
	t.Helper()
	f.out.Reset()
	f.errOut.Reset()
	if got := f.app.Run(args); got != want {
		t.Fatalf("args %q: exit %d, want %d; stderr=%s", args, got, want, f.errOut.String())
	}
}
func stableLaunchArgs(args []string) []string {
	result := append([]string(nil), args...)
	for i, arg := range result {
		if strings.HasSuffix(arg, ":/run/agentnook-telemetry") {
			result[i] = "<telemetry>:/run/agentnook-telemetry"
		} else if strings.HasPrefix(arg, "AGENTNOOK_REPORT_ID=") {
			result[i] = "AGENTNOOK_REPORT_ID=<run>"
		} else if strings.HasSuffix(arg, ":/etc/opencode/plugins/agentnook:ro") {
			result[i] = "<plugin>:/etc/opencode/plugins/agentnook:ro"
		}
	}
	return result
}
func assertNoData(t *testing.T, f *fixture) {
	t.Helper()
	if _, err := os.Stat(f.data); !os.IsNotExist(err) {
		t.Fatalf("storage created: %v", err)
	}
	if f.runs != 0 {
		t.Fatal("VM started")
	}
}
func testOptions() config.Options {
	o := config.Defaults()
	o.Agent = "pi"
	o.Image = config.BaseImage
	o.AgentVersion = "1.4.0"
	return o
}

func TestRememberAgentProfileAndResources(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--cpus", "3", "--mem", "4096", "--storage", "8")
	p := f.load(t).Projects[f.project]
	if p.Agent != "pi" || p.Agents["pi"].Profile != "work" || p.Agents["pi"].VM.Memory != 4096 {
		t.Fatal(p)
	}
	first := append([]string(nil), f.argv...)
	t.Setenv("AGENTNOOK_NONINTERACTIVE", "1")
	f.run(t, 0)
	if !reflect.DeepEqual(stableLaunchArgs(first), stableLaunchArgs(f.argv)) || f.prompt.calls != 0 || f.agentPrompt.calls != 0 || f.resolves != 1 {
		t.Fatal("remembered launch changed or prompted")
	}
	f.run(t, 0, "--mem", "2048")
	p = f.load(t).Projects[f.project]
	if p.Agents["pi"].VM.CPUs != 3 || p.Agents["pi"].VM.Memory != 2048 {
		t.Fatal(p)
	}
}
func TestInteractiveFirstLaunchAndSelect(t *testing.T) {
	f := setup(t)
	f.run(t, 0)
	if f.agentPrompt.calls != 1 || f.prompt.calls != 1 {
		t.Fatal("missing selection")
	}
	f.agentPrompt.name = "claude"
	f.prompt.choice = tui.Choice{Profile: "work"}
	f.run(t, 0, "--select")
	w := f.load(t).Projects[f.project]
	if w.Agent != "claude" || len(w.Agents) != 2 || f.agentPrompt.calls != 2 {
		t.Fatal(w)
	}
	f.prompt.err = tui.ErrCancelled
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	f.run(t, 130, "--select")
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 2 {
		t.Fatal("cancel wrote or launched")
	}
}
func TestNoninteractiveRequiresAgentAndProfile(t *testing.T) {
	for _, args := range [][]string{nil, {"--profile", "work"}, {"--agent", "claude"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := setup(t)
			t.Setenv("AGENTNOOK_NONINTERACTIVE", "1")
			f.run(t, 1, args...)
			assertNoData(t, f)
		})
	}
	f := setup(t)
	f.app.Interactive = func() bool { return false }
	f.run(t, 0, "--agent", "claude", "--profile", "headless", "--", "--print", "hello")
	if f.prompt.calls != 0 || f.agentPrompt.calls != 0 {
		t.Fatal("unexpected prompt")
	}
	for _, arg := range f.argv {
		if arg == "--tty" {
			t.Fatal("TTY on pipes")
		}
	}
}
func TestDryRunIsReadOnlyAndPreservesArguments(t *testing.T) {
	f := setup(t)
	entry := addCatalogImage(t, f, "work-v1")
	before, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	t.Setenv("PATH", "")
	pass := []string{"--print", "spaces ; $(echo nope)", "--select"}
	args := append([]string{"--agent", "pi", "--profile", "work", "--image", entry.Name, "--dry-run", "--"}, pass...)
	f.run(t, 0, args...)
	var argv []string
	if err := json.Unmarshal(f.out.Bytes(), &argv); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(argv[len(argv)-len(pass):], pass) {
		t.Fatal(argv)
	}
	after, _ := os.ReadFile(filepath.Join(f.data, "config.toml"))
	if !bytes.Equal(before, after) || f.runs != 0 {
		t.Fatal("dry-run wrote or ran")
	}
	if _, err := os.Stat(filepath.Join(f.data, "profiles")); !os.IsNotExist(err) {
		t.Fatal("profile created")
	}
}
func TestInvalidCLIAndRemovedFlags(t *testing.T) {
	for _, args := range [][]string{{"--agent", "nope"}, {"--agent", "../pi"}, {"--profile", "../bad"}, {"--pi-version", "1.4.0"}, {"--pi-preinstalled"}, {"--select", "--profile", "work"}, {"--select-image", "--image", "x"}, {"--mount", "x:y", "--clear-mounts"}, {"--cpus", "0"}, {"--mem", "-1"}, {"--agent-version", "latest"}, {"--print", "hello"}, {"hello"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) { f := setup(t); f.run(t, 2, args...); assertNoData(t, f) })
	}
}
func TestPromptErrorsAndExitStatus(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{{tui.ErrCancelled, 130}, {errors.New("terminal failed"), 1}} {
		f := setup(t)
		f.agentPrompt.err = tc.err
		f.run(t, tc.code)
		assertNoData(t, f)
	}
	f := setup(t)
	f.exit = 23
	f.run(t, 23, "--agent", "pi", "--profile", "work")
}
func TestHelpAndVersionReadOnly(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {"run", "--help"}, {"--version"}, {"version"}} {
		f := setup(t)
		f.run(t, 0, args...)
		assertNoData(t, f)
	}
}
