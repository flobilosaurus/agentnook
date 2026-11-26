package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstLaunchAutomaticallyBuildsAndReusesDefault(t *testing.T) {
	s := newSandbox(t)
	// Pin the release to keep process tests offline; npm resolution is unit-tested.
	_, stderr, code := s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work", "--agent-version", "1.8.2")
	if code != 0 {
		t.Fatalf("first launch: %d %s", code, stderr)
	}
	for _, text := range []string{"Image: pi-default", "Pi: 1.8.2 (built)", "CPU: 2", "RAM: 2048 MiB"} {
		if !strings.Contains(stderr, text) {
			t.Fatalf("missing %q: %s", text, stderr)
		}
	}
	first := s.argv(t)
	archive := value(first, "--from")
	if !filepath.IsAbs(archive) || !strings.HasSuffix(archive, ".smolmachine") ||
		value(first, "--cpus") != "2" || value(first, "--mem") != "2048" || value(first, "--init") != "" {
		t.Fatalf("wrong first launch: %q", first)
	}
	if err := os.Remove(s.capture + ".build"); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = s.run(t, s.project, "build-fail", 0)
	if code != 0 || value(s.argv(t), "--from") != archive {
		t.Fatalf("remembered launch rebuilt or changed image: %d %s", code, stderr)
	}
	other := filepath.Join(filepath.Dir(s.project), "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = s.run(t, other, "build-fail", 0, "--agent", "pi", "--profile", "work")
	if code != 0 || value(s.argv(t), "--from") != archive || value(s.argv(t), "--mem") != "2048" {
		t.Fatalf("new directory did not reuse default: %d %s", code, stderr)
	}
	if _, err := os.Stat(s.capture + ".build"); !os.IsNotExist(err) {
		t.Fatal("later launches invoked another build")
	}
	out, stderr, code := s.run(t, other, "", 0, "images")
	if code != 0 || !strings.Contains(out, "default") || !strings.Contains(out, "1.8.2") {
		t.Fatalf("default absent from catalog: %d %s %s", code, out, stderr)
	}
}

func TestManagedBuildListLaunchAndDefault(t *testing.T) {
	s := newSandbox(t)
	if err := os.WriteFile(filepath.Join(s.project, "build tools.toml"), []byte(`[tools]
rg = "14.1.1"
[tasks.build]
run = "unexpected-task-command"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code := s.run(t, s.project, "", 0, "images", "build", "work-v1", "--agent", "pi", "--agent-version", "1.4.0",
		"--cpus", "2", "--mem", "2048", "--storage", "8", "--set-default", "--tool", "aqua:sharkdp/fd@10.3.0", "--mise-config", "./build tools.toml")
	if code != 0 || !strings.Contains(out, "Built work-v1") {
		t.Fatalf("build: %d %q %q", code, out, stderr)
	}
	data, err := os.ReadFile(s.capture + ".build")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"build tools.toml", "unexpected-task-command"} {
		if bytes.Contains(data, []byte(value)) {
			t.Fatalf("mise file path or task leaked into build: %s", data)
		}
	}
	var calls [][]string
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var args []string
		if err := json.Unmarshal(line, &args); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, args)
	}
	var create, install, tools, pack, verify []string
	for _, args := range calls {
		switch args[0] + " " + args[1] {
		case "machine create":
			create = args
		case "machine exec":
			if value(args, "--env") != "" {
				install = args
			} else {
				tools = args
			}
		case "pack create":
			pack = args
		case "machine run":
			verify = args
		}
		for _, arg := range args {
			if arg == "--volume" || arg == "--ssh-agent" || arg == "--secret-env" {
				t.Fatal("build exposed host data", args)
			}
		}
	}
	if value(create, "--image") != "node:24-bookworm" || value(create, "--cpus") != "2" ||
		value(install, "--env") != "AGENT_VERSION=1.4.0" || !strings.Contains(strings.Join(install, " "), `actual_version="$(pi --version)"`) ||
		value(pack, "--from-vm") == "" || !strings.HasSuffix(value(verify, "--from"), ".smolmachine") {
		t.Fatalf("incorrect local build sequence: %q", calls)
	}
	if len(tools) < 3 || strings.Join(tools[len(tools)-3:], " ") != "agentnook-mise rg@14.1.1 aqua:sharkdp/fd@10.3.0" ||
		!strings.Contains(strings.Join(verify, " "), "/opt/agentnook/mise/exec") {
		t.Fatalf("tools not passed as build arguments or verified in pack: %q", calls)
	}
	cfg, err := os.ReadFile(filepath.Join(s.data, "config.toml"))
	if err != nil || !bytes.Contains(cfg, []byte(`tools = ["rg@14.1.1", "aqua:sharkdp/fd@10.3.0"]`)) {
		t.Fatalf("tool metadata not saved: %v\n%s", err, cfg)
	}
	for _, args := range [][]string{{"images"}, {"--images"}, {"run", "--images"}} {
		out, stderr, code = s.run(t, s.project, "", 0, args...)
		if code != 0 {
			t.Fatalf("list: %d %q", code, stderr)
		}
		for _, text := range []string{"work-v1", "node:24-bookworm", "1.4.0", "2048", "ready", "rg@14.1.1", "aqua:sharkdp/fd@10.3.0"} {
			if !strings.Contains(out, text) {
				t.Fatalf("list missing %q: %s", text, out)
			}
		}
	}
	out, stderr, code = s.run(t, s.project, "build-fail", 0, "images", "status")
	if code != 0 || stderr != "" || !strings.Contains(out, "work-v1 [default for pi]") ||
		!strings.Contains(out, "- rg@14.1.1") || !strings.Contains(out, "- aqua:sharkdp/fd@10.3.0") {
		t.Fatalf("images status: %d %q %q", code, out, stderr)
	}
	if after, err := os.ReadFile(s.capture + ".build"); err != nil || !bytes.Equal(data, after) {
		t.Fatal("catalog commands invoked a build or verification VM", err)
	}
	if _, err := os.Stat(s.capture); !os.IsNotExist(err) {
		t.Fatal("catalog commands launched VM")
	}
	if _, err := os.Stat(filepath.Join(s.data, "profiles")); !os.IsNotExist(err) {
		t.Fatal("image commands created profile")
	}

	_, stderr, code = s.run(t, s.project, "", 0, "--agent", "pi", "--profile", "work", "--mem", "4096")
	if code != 0 {
		t.Fatalf("launch: %d %s", code, stderr)
	}
	for _, text := range []string{"Profile: work", "Image: work-v1", "Linux base: node:24-bookworm", "Pi: 1.4.0 (built)", "CPU: 2", "RAM: 4096 MiB"} {
		if !strings.Contains(stderr, text) {
			t.Fatalf("launch display missing %q: %s", text, stderr)
		}
	}
	first := s.argv(t)
	archive := value(first, "--from")
	if !filepath.IsAbs(archive) || !strings.HasSuffix(archive, ".smolmachine") ||
		value(first, "--init") != "" || value(first, "--mem") != "4096" || value(first, "--") != "/opt/agentnook/mise/exec" {
		t.Fatalf("bad archive launch: %q", first)
	}
	// Build a second immutable name and change only future-directory defaults.
	_, stderr, code = s.run(t, s.project, "", 0, "images", "build", "work-v2", "--agent", "pi", "--agent-version", "1.4.0", "--mem", "6144")
	if code != 0 {
		t.Fatalf("second build: %d %s", code, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0, "images", "default", "work-v2")
	if code != 0 {
		t.Fatalf("default: %d %s", code, stderr)
	}
	_, stderr, code = s.run(t, s.project, "", 0)
	if code != 0 {
		t.Fatalf("remembered launch: %d %s", code, stderr)
	}
	if value(s.argv(t), "--from") != archive || value(s.argv(t), "--mem") != "4096" {
		t.Fatal("default update changed existing directory")
	}
	other := filepath.Join(filepath.Dir(s.project), "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	_, stderr, code = s.run(t, other, "", 0, "--agent", "pi", "--profile", "work")
	if code != 0 {
		t.Fatalf("new directory: %d %s", code, stderr)
	}
	if value(s.argv(t), "--from") == archive || value(s.argv(t), "--mem") != "6144" || value(s.argv(t), "--") != "pi" {
		t.Fatal("new directory did not adopt default")
	}
	_, stderr, code = s.run(t, other, "", 0, "--image", "work-v1", "--cpus", "3")
	if code != 0 {
		t.Fatalf("named selection: %d %s", code, stderr)
	}
	if value(s.argv(t), "--from") != archive || value(s.argv(t), "--cpus") != "3" {
		t.Fatal("explicit catalog name/resource override not applied")
	}
}

func TestFailedManagedBuildLeavesConfigAndCatalogUntouched(t *testing.T) {
	for _, action := range []string{"build-fail", "tools-fail", "pack-fail"} {
		t.Run(action, func(t *testing.T) {
			s := newSandbox(t)
			if _, stderr, code := s.run(t, s.project, "", 0, "images", "build", "keep", "--agent", "pi", "--agent-version", "1.4.0", "--set-default"); code != 0 {
				t.Fatalf("%d %s", code, stderr)
			}
			path := filepath.Join(s.data, "config.toml")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.project, "mise.toml"), []byte("[tools]\nrg = \"14.1.1\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, stderr, code := s.run(t, s.project, action, 0, "images", "build", "bad", "--agent", "pi", "--agent-version", "1.4.0", "--set-default", "--mise-config", "./mise.toml"); code != 1 {
				t.Fatalf("failed build: %d %s", code, stderr)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed build changed config")
			}
			entries, err := os.ReadDir(filepath.Join(s.data, "images"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("failed build leaked files: %v %v", entries, err)
			}
		})
	}
}
