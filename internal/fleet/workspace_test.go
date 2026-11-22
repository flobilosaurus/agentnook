package fleet

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitTest(t *testing.T, directory string, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git required for worktree tests")
	}
	prefix := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "user.name=Fleet Test", "-c", "user.email=fleet@example.invalid", "-C", directory}
	cmd := exec.Command(git, append(prefix, args...)...)
	cmd.Env = gitEnvironment()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func gitFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main, linked := filepath.Join(root, "repository with spaces"), filepath.Join(root, "elsewhere", "feature")
	if err := os.MkdirAll(main, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, main, "init", "-b", "main")
	gitTest(t, main, "commit", "--allow-empty", "-m", "initial")
	gitTest(t, main, "worktree", "add", "-b", "feat/watch", linked)
	return main, linked
}

func TestInspectWorkspaceMainLinkedSubfolderAndDetached(t *testing.T) {
	main, linked := gitFixture(t)
	sub := filepath.Join(linked, "subfolder")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	// Inherited repository overrides must not reassign host workspace identity.
	t.Setenv("GIT_DIR", "/missing/repository")
	t.Setenv("GIT_WORK_TREE", "/wrong/worktree")
	for _, tc := range []struct{ path, checkout, branch string }{
		{main, main, "main"}, {linked, linked, "feat/watch"}, {sub, linked, "feat/watch"},
	} {
		got, err := inspectWorkspace(context.Background(), tc.path)
		if err != nil || got.Repository != main || got.Checkout != tc.checkout || got.Branch != tc.branch {
			t.Fatalf("%s: %+v %v", tc.path, got, err)
		}
	}
	gitTest(t, linked, "checkout", "--detach")
	got, err := inspectWorkspace(context.Background(), linked)
	if err != nil || got.Branch != "detached" || got.Repository != main {
		t.Fatal(got, err)
	}
	if _, err := inspectWorkspace(context.Background(), filepath.Dir(main)); err == nil {
		t.Fatal("non-Git folder assigned to repository")
	}
}

func TestWorktreeEnrichmentPreservesScopeMountsAndFallback(t *testing.T) {
	main, linked := gitFixture(t)
	f := setupSource(t)
	plain := filepath.Join(f.root, "not a repo")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	var machines []machine
	verbose := "NAME STATE CPUS MEMORY MOUNTS\n"
	for i, project := range []string{main, linked, plain} {
		name := []string{"main", "linked", "plain"}[i]
		machines = append(machines, machine{Name: name, State: "running", CPUs: 2, MemoryMiB: 2048, MountCount: 3})
		verbose += f.mounts(name, project, f.source.Paths.Root)
	}
	f.store(t, machines, verbose)
	snapshot, err := f.source.Load(context.Background())
	if err != nil || len(snapshot.Instances) != 3 {
		t.Fatal(snapshot, err)
	}
	for _, item := range snapshot.Instances {
		if item.Project != item.Mounts[0].Host {
			t.Fatal("Git rewrote workspace mount", item)
		}
		if item.Name == "plain" {
			if item.Workspace != (Workspace{}) {
				t.Fatal("non-Git metadata", item)
			}
		} else if item.Workspace.Repository != main {
			t.Fatal("linked checkout not grouped", item)
		}
	}
	// Ended instances prune metadata without introducing a directory filter.
	f.store(t, machines[1:2], f.mounts("linked", linked, f.source.Paths.Root))
	snapshot, err = f.source.Load(context.Background())
	if err != nil || len(snapshot.Instances) != 1 || snapshot.Instances[0].Project != linked {
		t.Fatal(snapshot, err)
	}
	if len(f.source.workspaces) != 1 {
		t.Fatal("inactive workspace cache retained")
	}
}

func TestWorkspaceCacheRefreshAndGitUnavailable(t *testing.T) {
	main, linked := gitFixture(t)
	s := Source{}
	items := []Instance{{Project: linked}, {Project: linked}}
	s.observeWorkspaces(context.Background(), items)
	if items[0].Workspace.Repository != main || items[1].Workspace != items[0].Workspace || len(s.workspaces) != 1 {
		t.Fatal(items, s.workspaces)
	}
	gitTest(t, linked, "branch", "-m", "feat/renamed")
	cached := s.workspaces[linked]
	cached.at = time.Now().Add(-6 * time.Second)
	s.workspaces[linked] = cached
	s.observeWorkspaces(context.Background(), items)
	if items[0].Workspace.Branch != "feat/renamed" {
		t.Fatal("branch cache never refreshed", items)
	}
	t.Setenv("PATH", t.TempDir())
	s = Source{}
	s.observeWorkspaces(context.Background(), items)
	if items[0].Workspace != (Workspace{}) {
		t.Fatal("missing Git lost folder fallback", items)
	}
}

func TestWorkspaceGitBoundedAndNoHelpers(t *testing.T) {
	main, _ := gitFixture(t)
	marker := filepath.Join(t.TempDir(), "helper-ran")
	helper := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, main, "config", "core.fsmonitor", helper)
	if _, err := inspectWorkspace(context.Background(), main); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git executed a repository helper", err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n/bin/sleep 20\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	s := Source{}
	s.observeWorkspaces(ctx, []Instance{{Project: main}})
	if time.Since(started) > time.Second {
		t.Fatal("Git delayed fleet beyond deadline")
	}
}

func TestWorkspaceCacheRefreshPruningAndMissingGitFallback(t *testing.T) {
	main, _ := gitFixture(t)
	s := Source{}
	items := []Instance{{Name: "a", Project: main}, {Name: "b", Project: main}}
	s.observeWorkspaces(context.Background(), items)
	if len(s.workspaces) != 1 || items[0].Workspace.Branch != "main" || items[1].Workspace != items[0].Workspace {
		t.Fatal("lookup not shared per exact workspace", s.workspaces, items)
	}
	gitTest(t, main, "branch", "-m", "renamed")
	s.observeWorkspaces(context.Background(), items)
	if items[0].Workspace.Branch != "main" {
		t.Fatal("cache was bypassed")
	}
	expire := func() {
		entry := s.workspaces[main]
		entry.at = time.Now().Add(-6 * time.Second)
		s.workspaces[main] = entry
	}
	expire()
	s.observeWorkspaces(context.Background(), items)
	if items[0].Workspace.Branch != "renamed" {
		t.Fatal("cache did not refresh", items)
	}
	t.Setenv("PATH", "")
	expire()
	s.observeWorkspaces(context.Background(), items)
	if len(items) != 2 || items[0].Project != main || items[0].Workspace != (Workspace{}) {
		t.Fatal("missing Git hid or reassigned workspace", items)
	}
	s.observeWorkspaces(context.Background(), nil)
	if len(s.workspaces) != 0 {
		t.Fatal("unused metadata retained")
	}
}

func TestParseWorktreesRejectsMalformedAndAmbiguousOutput(t *testing.T) {
	main, linked := gitFixture(t)
	valid := gitTest(t, main, "worktree", "list", "--porcelain", "-z")
	for _, bad := range []string{
		"", "not porcelain", strings.TrimSuffix(valid, "\x00"), valid + valid,
		strings.Replace(valid, "worktree "+main, "worktree relative", 1),
		strings.Replace(valid, "feat/watch", "bad\x1b[2J", 1),
	} {
		if _, err := parseWorktrees([]byte(bad), linked); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := parseWorktrees([]byte(valid), "/not-in-list"); err == nil {
		t.Fatal("accepted missing checkout")
	}
}
