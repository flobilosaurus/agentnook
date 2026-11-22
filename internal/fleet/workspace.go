package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Workspace is optional, display-only Git metadata. Project remains the exact
// launch directory, including when it is a subdirectory of a checkout.
type Workspace struct {
	Repository string // Main checkout (or bare repository) anchoring the group.
	Checkout   string // Root of this specific checkout.
	Branch     string // Display label only; never used as an identity.
}

type workspaceObservation struct {
	value Workspace
	at    time.Time
}

// Git is optional. Cache successful and failed lookups briefly, bound the entire
// enrichment pass, and never let Git failure hide otherwise visible machines.
func (s *Source) observeWorkspaces(ctx context.Context, instances []Instance) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	used := make(map[string]bool)
	for _, item := range instances {
		used[item.Project] = true
	}
	projects := make([]string, 0, len(used))
	for project := range used {
		projects = append(projects, project)
	}
	sort.Strings(projects)
	if s.workspaces == nil {
		s.workspaces = make(map[string]workspaceObservation)
	}
	for _, project := range projects {
		cached, found := s.workspaces[project]
		if found && time.Since(cached.at) < 5*time.Second {
			continue
		}
		value, err := inspectWorkspace(ctx, project)
		if ctx.Err() != nil {
			break // Preserve a previous observation when this pass runs out of time.
		}
		if err != nil {
			value = Workspace{}
		}
		s.workspaces[project] = workspaceObservation{value: value, at: time.Now()}
	}
	for project := range s.workspaces {
		if !used[project] {
			delete(s.workspaces, project)
		}
	}
	for i := range instances {
		instances[i].Workspace = s.workspaces[instances[i].Project].value
	}
}

func gitEnvironment() []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			env = append(env, value)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
}

func inspectWorkspace(ctx context.Context, project string) (Workspace, error) {
	git := func(args ...string) ([]byte, error) {
		prefix := []string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull, "-C", project}
		return commandEnv(ctx, "git", gitEnvironment(), append(prefix, args...)...)
	}
	data, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return Workspace{}, err
	}
	checkout, err := canonicalGitPath(strings.TrimSuffix(string(data), "\n"))
	if err != nil {
		return Workspace{}, err
	}
	rel, err := filepath.Rel(checkout, project)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Workspace{}, errors.New("workspace is outside Git checkout")
	}
	data, err = git("worktree", "list", "--porcelain", "-z")
	if err != nil {
		return Workspace{}, err
	}
	return parseWorktrees(data, checkout)
}

func canonicalGitPath(value string) (string, error) {
	if !utf8.ValidString(value) || !filepath.IsAbs(value) || filepath.Clean(value) != value || unsafeText(value) {
		return "", errors.New("invalid Git path")
	}
	return filepath.EvalSymlinks(value)
}

// The NUL format preserves spaces and avoids Git's quoted-path conventions.
// Git lists the main worktree first, even when invoked from a linked worktree.
func parseWorktrees(data []byte, checkout string) (Workspace, error) {
	if len(data) > maxOutput || !utf8.Valid(data) || !strings.HasSuffix(string(data), "\x00\x00") {
		return Workspace{}, errors.New("invalid Git worktree list")
	}
	var main string
	var result Workspace
	seen := make(map[string]bool)
	for _, record := range strings.Split(strings.TrimSuffix(string(data), "\x00\x00"), "\x00\x00") {
		fields := strings.Split(record, "\x00")
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "worktree ") {
			return Workspace{}, errors.New("invalid Git worktree record")
		}
		path, err := canonicalGitPath(strings.TrimPrefix(fields[0], "worktree "))
		if err != nil {
			// A prunable, unrelated linked checkout may no longer exist.
			if main != "" {
				continue
			}
			return Workspace{}, err
		}
		if seen[path] {
			return Workspace{}, errors.New("duplicate Git worktree")
		}
		seen[path] = true
		if main == "" {
			main = path
		}
		if path != checkout {
			continue
		}
		result = Workspace{Repository: main, Checkout: checkout}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "branch refs/heads/") {
				result.Branch = strings.TrimPrefix(field, "branch refs/heads/")
			} else if field == "detached" {
				result.Branch = "detached"
			}
		}
		if unsafeText(result.Branch) {
			return Workspace{}, errors.New("invalid Git branch")
		}
	}
	if result.Checkout == "" {
		return Workspace{}, errors.New("workspace missing from Git worktree list")
	}
	return result, nil
}
