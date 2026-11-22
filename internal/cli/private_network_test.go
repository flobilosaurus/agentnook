package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func privateArgs(args []string) []string {
	var values []string
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		if args[i] == "--allow-private" && i+1 < len(args) {
			values = append(values, args[i+1])
			i++
		}
	}
	return values
}

func TestPrivateProtectionDefaultAndSavedExceptions(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "pi", "--profile", "work")
	if !strings.Contains(strings.Join(f.argv, " "), "--block-private") || len(privateArgs(f.argv)) != 0 {
		t.Fatal("default work VM not protected", f.argv)
	}
	// Also protect a saved pre-feature project, without requiring a flag.
	f.run(t, 0)
	if !strings.Contains(strings.Join(f.argv, " "), "--block-private") {
		t.Fatal("existing project not protected")
	}
	f.run(t, 0, "--allow-private", "10.20.1.2/16", "--allow-private", "fd00::1/64", "--allow-private", "10.20.0.0/16")
	want := []string{"10.20.0.0/16", "fd00::/64"}
	if !reflect.DeepEqual(privateArgs(f.argv), want) || !reflect.DeepEqual(f.load(t).Projects[f.project].Agents["pi"].AllowPrivate, want) {
		t.Fatal("exceptions not normalized/saved", f.argv)
	}
	f.run(t, 0, "--network", "full")
	if !reflect.DeepEqual(privateArgs(f.argv), want) {
		t.Fatal("full dropped saved exceptions")
	}
	f.run(t, 0, "--allow-private", "127.0.0.1/32")
	if !reflect.DeepEqual(privateArgs(f.argv), []string{"127.0.0.1/32"}) {
		t.Fatal("repeat invocation must replace, not append", f.argv)
	}
	f.run(t, 0, "--agent", "claude", "--profile", "work")
	if len(privateArgs(f.argv)) != 0 || len(f.load(t).Projects[f.project].Agents["pi"].AllowPrivate) != 1 {
		t.Fatal("exceptions leaked across agent selections")
	}
	f.run(t, 0, "--agent", "pi", "--network", "ask", "--clear-allow-private")
	if len(privateArgs(f.argv)) != 0 || len(f.load(t).Projects[f.project].Agents["pi"].AllowPrivate) != 0 || !strings.Contains(strings.Join(f.argv, " "), "--block-private") {
		t.Fatal("clear did not retain protected ask mode", f.argv)
	}
}

func TestPrivateExceptionsRejectBadInputWithoutEffects(t *testing.T) {
	for _, args := range [][]string{
		{"--allow-private", "bad"}, {"--allow-private", "localhost"},
		{"--allow-private", "::ffff:127.0.0.1/128"},
		{"--allow-private", "10.0.0.0/8", "--clear-allow-private"},
		{"--agent", "pi", "--profile", "work", "--network", "ask", "--allow-private", "10.0.0.0/8"},
	} {
		f := setup(t)
		f.run(t, 2, args...)
		assertNoData(t, f)
	}
}

func TestSavedPrivateExceptionsCannotSilentlyBypassAsk(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--allow-private", "10.20.0.0/16")
	file := filepath.Join(f.data, "config.toml")
	before, _ := os.ReadFile(file)
	runs := f.runs
	f.run(t, 2, "--network", "ask")
	after, _ := os.ReadFile(file)
	if f.runs != runs || string(before) != string(after) || !strings.Contains(f.errOut.String(), "bypass approvals") {
		t.Fatal("saved exception bypassed broker or changed config", f.errOut.String())
	}
}

func TestPrivateDryRunIsReadOnlyAndPreservesAgentArguments(t *testing.T) {
	f := setup(t)
	image := addCatalogImage(t, f, "private-test")
	file := filepath.Join(f.data, "config.toml")
	before, _ := os.ReadFile(file)
	t.Setenv("PATH", "")
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--image", image.Name, "--allow-private", "10.20.1.2/16", "--dry-run", "--", "--allow-private", "guest-argument")
	var args []string
	if err := json.Unmarshal(f.out.Bytes(), &args); err != nil || !reflect.DeepEqual(privateArgs(args), []string{"10.20.0.0/16"}) {
		t.Fatal(args, err)
	}
	if !reflect.DeepEqual(args[len(args)-2:], []string{"--allow-private", "guest-argument"}) || !strings.Contains(strings.Join(args, " "), "--block-private") {
		t.Fatal("dry-run lost protection or changed agent argv", args)
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) || f.runs != 0 {
		t.Fatal("dry run changed state")
	}
}

func TestBuildVMNetworkUnchangedByWorkVMProtection(t *testing.T) {
	f := setup(t)
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--allow-private", "10.20.0.0/16")
	log, err := os.ReadFile(os.Getenv("AGENTNOOK_TEST_SMOLVM_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--block-private", "--allow-private"} {
		if strings.Contains(string(log), flag) {
			t.Fatal("work VM policy leaked into build lifecycle", flag)
		}
	}
}
