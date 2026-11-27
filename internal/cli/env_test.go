package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestEnvForwardingPersistsNamesOnly(t *testing.T) {
	f := setup(t)
	const secret = "test-env-value with spaces; $HOME\nsecond line"
	t.Setenv("NOOK_TEST_SECRET", secret)
	t.Setenv("NOOK_TEST_EMPTY", "")
	t.Setenv("NOOK_TEST_UNSELECTED", "never forwarded")
	flags := []string{"--env", "NOOK_TEST_SECRET", "--env=NOOK_TEST_EMPTY", "--env", "NOOK_TEST_SECRET"}
	want := []string{"--secret-env", "NOOK_TEST_SECRET=NOOK_TEST_SECRET", "--secret-env", "NOOK_TEST_EMPTY=NOOK_TEST_EMPTY"}
	assertRefs := func(args []string) {
		t.Helper()
		refs := []string{}
		for i := 0; i+1 < len(args) && args[i] != "--"; i++ {
			if args[i] == "--secret-env" {
				refs = append(refs, args[i], args[i+1])
				i++
			}
		}
		if !slices.Equal(refs, want) {
			t.Fatal("wrong env references", refs)
		}
	}
	f.run(t, 0, flags...)
	assertRefs(f.argv)
	assertNoEnvValue := func(outputs ...string) {
		t.Helper()
		for _, output := range outputs {
			if strings.Contains(output, "test-env-value") || strings.Contains(output, "NOOK_TEST_UNSELECTED") {
				t.Fatal("env value or unselected variable leaked")
			}
		}
	}
	assertNoEnvValue(strings.Join(f.argv, "\n"), f.out.String(), f.errOut.String())
	if got := f.load(t).Projects[f.project].Agents["pi"].EnvNames; !slices.Equal(got, []string{"NOOK_TEST_SECRET", "NOOK_TEST_EMPTY"}) {
		t.Fatal("wrong saved names", got)
	}
	configFile := filepath.Join(f.data, "config.toml")
	before := readEnvTestFile(t, configFile)
	assertNoEnvValue(string(before))
	if strings.Contains(string(readEnvTestFile(t, filepath.Join(f.root, "smolvm.log"))), "NOOK_TEST_") {
		t.Fatal("env references in image build args")
	}

	runs := f.runs
	for _, flags := range [][]string{nil, {"--env", "NOOK_TEST_EMPTY"}, {"--clear-env"}} {
		f.run(t, 0, append([]string{"--dry-run"}, flags...)...)
		var plan []string
		if err := json.Unmarshal(f.out.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		switch {
		case len(flags) == 0:
			assertRefs(plan)
		case flags[0] == "--env":
			if !slices.Contains(plan, "NOOK_TEST_EMPTY=NOOK_TEST_EMPTY") || slices.Contains(plan, "NOOK_TEST_SECRET=NOOK_TEST_SECRET") {
				t.Fatal("dry-run did not replace names", plan)
			}
		case slices.Contains(plan, "--secret-env"):
			t.Fatal("dry-run did not clear names", plan)
		}
		assertNoEnvValue(f.out.String(), f.errOut.String())
		if f.runs != runs || !bytes.Equal(before, readEnvTestFile(t, configFile)) {
			t.Fatal("dry-run changed config or launched a VM")
		}
	}

	f.run(t, 0, "--", "--env", "agent-argument")
	assertRefs(f.argv)
	if !slices.Equal(f.argv[len(f.argv)-2:], []string{"--env", "agent-argument"}) {
		t.Fatal("agent args changed", f.argv)
	}
}

func readEnvTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSavedEnvIsolationReplacementAndClear(t *testing.T) {
	f := setup(t)
	const first, next = "NOOK_TEST_FIRST", "NOOK_TEST_NEXT"
	t.Setenv(first, "first")
	t.Setenv(next, "next")
	f.run(t, 0, "--agent", "pi", "--profile", "work", "--env", first)
	f.run(t, 0, "--agent", "claude", "--profile", "work")
	if slices.Contains(f.argv, "--secret-env") {
		t.Fatal("forward leaked to another agent")
	}
	other := filepath.Join(f.root, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(other)
	f.run(t, 0, "--agent", "pi", "--profile", "work")
	if slices.Contains(f.argv, "--secret-env") {
		t.Fatal("forward leaked to another project")
	}
	t.Chdir(f.project)
	f.run(t, 0, "--agent", "pi", "--env", next)
	f.run(t, 0, "--clear-env=false")
	if got := f.load(t).Projects[f.project].Agents["pi"].EnvNames; !slices.Equal(got, []string{next}) || !slices.Contains(f.argv, next+"="+next) || slices.Contains(f.argv, first+"="+first) {
		t.Fatal("saved list not replaced", got)
	}
	if err := os.Unsetenv(next); err != nil {
		t.Fatal(err)
	}
	runs := f.runs
	f.run(t, 2)
	if f.runs != runs || !strings.Contains(f.errOut.String(), next+" is not exported") || !strings.Contains(f.errOut.String(), "--clear-env") {
		t.Fatal("missing saved variable must block launch and explain recovery")
	}
	f.run(t, 0, "--clear-env")
	f.run(t, 0)
	if len(f.load(t).Projects[f.project].Agents["pi"].EnvNames) != 0 || slices.Contains(f.argv, "--secret-env") {
		t.Fatal("forwards not cleared persistently")
	}
}

func TestLaunchDoesNotRestoreConcurrentlyClearedEnv(t *testing.T) {
	f := setup(t)
	t.Setenv("NOOK_TEST_SECRET", "test value")
	f.run(t, 0, "--env", "NOOK_TEST_SECRET")
	f.app.Interactive = func() bool {
		// The launch already loaded its settings. Simulate another process clearing them.
		if err := config.Update(filepath.Join(f.data, "config.toml"), func(c *config.Config) error {
			w := c.Projects[f.project]
			p := w.Agents["pi"]
			p.EnvNames = nil
			w.Agents["pi"] = p
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return false
	}
	f.run(t, 1)
	if f.runs != 1 || !strings.Contains(f.errOut.String(), "saved environment forwards changed") || len(f.load(t).Projects[f.project].Agents["pi"].EnvNames) != 0 {
		t.Fatal("launch reused or restored a revoked forward")
	}
}

func TestInvalidOrMissingEnvFailsBeforeSetup(t *testing.T) {
	f := setup(t)
	const missing = "NOOK_TEST_MISSING"
	t.Setenv(missing, "")
	if err := os.Unsetenv(missing); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "invalid-name", "1INVALID", "SECRET=do-not-echo"} {
		f.run(t, 2, "--env", name)
		err := f.errOut.String()
		if !strings.Contains(err, "expected variable names") || strings.Contains(err, "do-not-echo") {
			t.Fatal("invalid env diagnostic")
		}
	}
	for _, args := range [][]string{{"--env", missing}, {"--dry-run", "--env", missing}} {
		f.run(t, 2, args...)
		if !strings.Contains(f.errOut.String(), missing+" is not exported on the host") {
			t.Fatal(f.errOut.String())
		}
	}
	f.run(t, 2, "--clear-env", "--env", missing)
	if !strings.Contains(f.errOut.String(), "--env and --clear-env cannot be combined") {
		t.Fatal(f.errOut.String())
	}
	assertNoData(t, f)
	if f.agentPrompt.calls != 0 || f.prompt.calls != 0 || f.resolves != 0 || f.runs != 0 {
		t.Fatal("invalid env reached setup or launch")
	}
}
