package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/tui"
)

func TestWatchReadOnlyAndIndependentOfDirectory(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "smolvm"), []byte("#!/bin/sh\nprintf '[]\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(f.root) // No project configuration is required in the caller's folder.
	called := 0
	f.app.Watch = func(scope string, load tui.FleetLoader) error {
		called++
		if scope != "All projects · AGENTNOOK_HOME: "+f.data {
			t.Fatal("not global", scope)
		}
		data, err := load(context.Background())
		if err != nil || len(data.Instances) != 0 {
			t.Fatalf("%+v %v", data, err)
		}
		return nil
	}
	f.run(t, 0, "watch")
	if called != 1 || f.prompt.calls != 0 || f.resolves != 0 {
		t.Fatal("watch entered launch flow")
	}
	assertNoData(t, f)
}

func TestWatchHelpArgumentsAndTerminal(t *testing.T) {
	f := setup(t)
	calls := 0
	f.app.Watch = func(string, tui.FleetLoader) error { calls++; return nil }
	for _, args := range [][]string{{"watch", "--cpus", "2"}, {"watch", "extra"}, {"watch", "--"}, {"watch", "--help", "extra"}, {"watch", "--all"}, {"watch", "--all", "--help"}, {"watch", "-h", "--all"}} {
		f.run(t, 2, args...)
	}
	f.app.Interactive = func() bool { return false }
	f.run(t, 1, "watch")
	if !strings.Contains(f.errOut.String(), "terminal input and output") {
		t.Fatal(f.errOut.String())
	}
	t.Setenv("PATH", "")
	for _, args := range [][]string{{"watch", "--help"}, {"watch", "-h"}} {
		f.run(t, 0, args...)
		if !strings.Contains(f.out.String(), "Usage: nook watch\n") || strings.Contains(f.out.String(), "--all") || calls != 0 {
			t.Fatal("help advertised removed flag or opened watch", f.out.String())
		}
	}
	f.app.Interactive = func() bool { return true }
	f.run(t, 127, "watch")
	assertNoData(t, f)
}

func TestWatchAllProjectsReadOnly(t *testing.T) {
	f := setup(t)
	var machines []map[string]any
	verbose := "NAME STATE CPUS MEMORY MOUNTS\n"
	for _, name := range []string{"current", "other", "foreign"} {
		project, home := f.project, f.data
		if name != "current" {
			project = filepath.Join(f.root, "other project")
		}
		if name == "foreign" {
			home += "-other"
		}
		machines = append(machines, map[string]any{"name": name, "state": "running", "cpus": 2, "memory_mib": 2048, "mounts": 2})
		verbose += fmt.Sprintf("%s running 2 2048 MiB 2\n  Mount: %s -> /workspace\n  Mount: %s/profiles/work/pi -> /root\n", name, project, home)
	}
	data, err := json.Marshal(machines)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\n 'machine ls --json') printf '%%s\\n' '%s';;\n 'machine ls --verbose') printf '%%s\\n' '%s';;\n *) exit 99;;\nesac\n", data, verbose)
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "smolvm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{f.project, f.root} {
		t.Chdir(cwd)
		called := false
		f.app.Watch = func(scope string, load tui.FleetLoader) error {
			called = true
			if !strings.Contains(scope, "All projects") || !strings.Contains(scope, f.data) {
				t.Fatal("global scope missing data home", scope)
			}
			snapshot, err := load(context.Background())
			if err != nil || len(snapshot.Instances) != 2 {
				t.Fatalf("cwd=%s: %+v %v", cwd, snapshot, err)
			}
			for _, item := range snapshot.Instances {
				if item.Project == "" || item.Name == "foreign" {
					t.Fatal("missing project or foreign data home", item)
				}
			}
			return nil
		}
		f.run(t, 0, "watch")
		if !called || f.prompt.calls != 0 || f.resolves != 0 {
			t.Fatal("watch entered launch flow or skipped UI")
		}
		assertNoData(t, f)
	}
}

func TestWatchUIFailure(t *testing.T) {
	f := setup(t)
	f.app.Watch = func(string, tui.FleetLoader) error { return errors.New("terminal failed") }
	f.run(t, 1, "watch")
	if !strings.Contains(f.errOut.String(), "watch: terminal failed") {
		t.Fatal(f.errOut.String())
	}
	assertNoData(t, f)
}
