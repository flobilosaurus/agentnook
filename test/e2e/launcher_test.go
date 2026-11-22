package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var launcher, stubBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agentnook-e2e-*")
	if err != nil {
		panic(err)
	}
	launcher, stubBin = filepath.Join(dir, "nook"), filepath.Join(dir, "bin")
	if err := os.Mkdir(stubBin, 0o700); err != nil {
		panic(err)
	}
	for _, build := range []struct{ output, source string }{
		{launcher, "./cmd/nook"},
		{filepath.Join(stubBin, "smolvm"), "./test/e2e/testdata/smolvm.go"},
	} {
		cmd := exec.Command("go", "build", "-o", build.output, build.source)
		cmd.Dir = "../.."
		if output, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build: %v\n%s", err, output)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type sandbox struct {
	data, home, project, capture string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	root := t.TempDir()
	s := sandbox{
		data: filepath.Join(root, "data"), home: filepath.Join(root, "home"),
		project: filepath.Join(root, "project with spaces"), capture: filepath.Join(root, "args.json"),
	}
	for _, path := range []string{s.home, s.project} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func (s sandbox) run(t *testing.T, cwd, action string, exit int, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(launcher, args...)
	cmd.Dir = cwd
	env := map[string]string{
		"HOME": s.home, "AGENTNOOK_HOME": s.data, "AGENTNOOK_NONINTERACTIVE": "1",
		"PATH": stubBin, "AGENTNOOK_TEST_CAPTURE": s.capture,
		"AGENTNOOK_TEST_ACTION": action, "AGENTNOOK_TEST_EXIT": fmt.Sprint(exit),
	}
	// Deliberately minimal environment: no user agent/config directories or API keys.
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	code := 0
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if entries, err := os.ReadDir(s.capture + ".machines"); err == nil && len(entries) != 0 {
		t.Fatalf("temporary machines leaked: %v", entries)
	}
	return out.String(), errOut.String(), code
}

func (s sandbox) argv(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(s.capture)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func value(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func profileMount(args []string) string {
	for _, arg := range args {
		if strings.HasSuffix(arg, ":/root") {
			return arg
		}
	}
	return ""
}

func TestRememberedLaunchAndExitCode(t *testing.T) {
	s := newSandbox(t)
	pass := []string{"--print", "keep spaces; $(echo nope)", "--select"}
	args := append([]string{"run", "--agent", "pi", "--profile", "work", "--cpus", "2", "--mem", "2048", "--storage", "8", "--agent-version", "1.4.0", "--"}, pass...)
	if _, stderr, code := s.run(t, s.project, "", 0, args...); code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	first := s.argv(t)
	if !reflect.DeepEqual(first[len(first)-len(pass):], pass) {
		t.Fatalf("Pi args changed: %q", first)
	}
	for _, arg := range first {
		if arg == "--tty" {
			t.Fatal("TTY in piped mode")
		}
	}
	if value(first, "--workdir") != "/workspace" {
		t.Fatal("wrong guest workdir")
	}
	piDir := strings.TrimSuffix(profileMount(first), ":/root")
	entries, err := os.ReadDir(piDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("launcher created auth/settings: %v %v", entries, err)
	}

	if _, stderr, code := s.run(t, s.project, "", 23); code != 23 {
		t.Fatalf("exit code %d, stderr %s", code, stderr)
	}
	second := s.argv(t)
	for _, flag := range []string{"--from", "--cpus", "--mem", "--storage"} {
		if value(first, flag) != value(second, flag) {
			t.Fatalf("%s not remembered", flag)
		}
	}
	if profileMount(first) != profileMount(second) {
		t.Fatal("profile mount changed")
	}
	configData, err := os.ReadFile(filepath.Join(s.data, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(configData, []byte("echo nope")) {
		t.Fatal("Pi prompt persisted")
	}
}

func TestGlobalImageDefaultsAndPreinstalledMode(t *testing.T) {
	s := newSandbox(t)
	if _, stderr, code := s.run(t, s.project, "", 0, "images", "build", "work-v1", "--agent", "pi", "--agent-version", "1.4.0",
		"--cpus", "2", "--mem", "4096", "--set-default"); code != 0 {
		t.Fatalf("build global default: %d %s", code, stderr)
	}
	if _, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work"); code != 0 {
		t.Fatalf("%d: %s", code, stderr)
	}
	first := s.argv(t)
	if !filepath.IsAbs(value(first, "--from")) || value(first, "--cpus") != "2" ||
		value(first, "--mem") != "4096" || value(first, "--init") != "" {
		t.Fatalf("wrong default launch: %q", first)
	}
	if _, stderr, code := s.run(t, s.project, "", 0); code != 0 {
		t.Fatalf("%d: %s", code, stderr)
	}
	if !reflect.DeepEqual(first, s.argv(t)) {
		t.Fatal("preinstalled image options not remembered")
	}

	// Managed images never provision at launch; the removed flag is rejected.
	other := filepath.Join(filepath.Dir(s.project), "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := s.run(t, other, "", 0, "--agent", "pi", "--profile", "work",
		"--image", "work-v1", "--pi-preinstalled=false"); code != 2 {
		t.Fatalf("%d: %s", code, stderr)
	}
	if !reflect.DeepEqual(first, s.argv(t)) {
		t.Fatal("invalid flag launched VM")
	}
}

func TestSharedLoginAndProfileIsolation(t *testing.T) {
	s := newSandbox(t)
	if _, stderr, code := s.run(t, s.project, "login", 0, "--agent", "pi", "--profile", "work", "--agent-version", "1.4.0"); code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	firstMount := profileMount(s.argv(t))
	other := filepath.Join(filepath.Dir(s.project), "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := s.run(t, other, "read", 0, "--agent", "pi", "--profile", "work")
	if code != 0 || !strings.Contains(out, "test-only") {
		t.Fatalf("auth not shared: %d %q %q", code, out, stderr)
	}
	if firstMount != profileMount(s.argv(t)) {
		t.Fatal("same profile did not reuse directory")
	}
	if _, stderr, code := s.run(t, other, "", 0, "--agent", "pi", "--profile", "private"); code != 0 {
		t.Fatalf("%d %s", code, stderr)
	}
	secondMount := profileMount(s.argv(t))
	if firstMount == secondMount {
		t.Fatal("different profiles share storage")
	}
	auth := filepath.Join(strings.TrimSuffix(secondMount, ":/root"), ".pi", "agent", "auth.json")
	if _, err := os.Stat(auth); !os.IsNotExist(err) {
		t.Fatalf("credentials leaked into fresh profile: %v", err)
	}
	auth = filepath.Join(strings.TrimSuffix(firstMount, ":/root"), ".pi", "agent", "auth.json")
	if data, err := os.ReadFile(auth); err != nil || !bytes.Contains(data, []byte("test-only")) {
		t.Fatal("original credentials changed")
	}
}

func TestNoninteractiveAndDryRunHaveNoSideEffects(t *testing.T) {
	s := newSandbox(t)
	if _, stderr, code := s.run(t, s.project, "", 0); code != 1 || !strings.Contains(stderr, "--profile") {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if _, err := os.Stat(s.data); !os.IsNotExist(err) {
		t.Fatal("noninteractive failure created storage")
	}
	_, stderr, code := s.run(t, s.project, "", 0, "--dry-run", "--agent", "pi", "--profile", "work")
	if code != 1 || !strings.Contains(stderr, "without --dry-run") {
		t.Fatalf("empty-catalog dry-run: %d %s", code, stderr)
	}
	if _, err := os.Stat(s.data); !os.IsNotExist(err) {
		t.Fatal("dry-run created storage")
	}
	if _, err := os.Stat(s.capture); !os.IsNotExist(err) {
		t.Fatal("dry-run launched smolvm")
	}
}
