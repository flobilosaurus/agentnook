package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/tui"
)

func TestWatchReadOnlyAndCanonicalDirectory(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "smolvm"), []byte("#!/bin/sh\nprintf '[]\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(f.root, "alias")
	if err := os.Symlink(f.project, alias); err != nil {
		t.Fatal(err)
	}
	t.Chdir(alias)
	called := 0
	f.app.Watch = func(project string, load tui.FleetLoader) error {
		called++
		if project != f.project {
			t.Fatal("not canonical", project)
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
	for _, args := range [][]string{{"watch", "--cpus", "2"}, {"watch", "extra"}, {"watch", "--"}, {"watch", "--help", "extra"}} {
		f.run(t, 2, args...)
	}
	f.app.Interactive = func() bool { return false }
	f.run(t, 1, "watch")
	if !strings.Contains(f.errOut.String(), "terminal input and output") {
		t.Fatal(f.errOut.String())
	}
	t.Setenv("PATH", "")
	f.run(t, 0, "watch", "--help")
	if !strings.Contains(f.out.String(), "Usage: nook watch") || calls != 0 {
		t.Fatal("help queried machines or opened watch")
	}
	f.app.Interactive = func() bool { return true }
	f.run(t, 127, "watch")
	assertNoData(t, f)
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
