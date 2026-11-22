package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDefaultAndOverride(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("AGENTNOOK_HOME", "")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != filepath.Join(home, ".agentnook") {
		t.Fatalf("root %q", p.Root)
	}
	t.Setenv("AGENTNOOK_HOME", filepath.Join(home, "custom"))
	p, err = Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if p.ConfigFile() != filepath.Join(home, "custom", "config.toml") {
		t.Fatal(p)
	}
}

func TestResolveSymlinkAncestor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTNOOK_HOME", filepath.Join(link, "not", "created"))
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	target, _ = filepath.EvalSymlinks(target)
	if p.Root != filepath.Join(target, "not", "created") {
		t.Fatal(p.Root)
	}
}

func TestEnsureCreatesDirectoriesOnlyAndKeepsCredentials(t *testing.T) {
	p := Paths{Root: filepath.Join(t.TempDir(), "data")}
	dir, err := p.EnsureAgentDir("work", "pi")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Pi dir not empty: %v, %v", entries, err)
	}
	for _, path := range []string{p.Root, filepath.Dir(filepath.Dir(dir)), filepath.Dir(dir), dir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode: %v", path, info.Mode())
		}
	}
	auth := filepath.Join(dir, "agent", "auth.json")
	if err := os.Mkdir(filepath.Dir(auth), 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("{\"test\":\"keep me\"}")
	if err := os.WriteFile(auth, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnsureAgentDir("work", "pi"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(auth)
	if string(got) != string(want) {
		t.Fatal("credentials overwritten")
	}
	if _, err := p.EnsureAgentDir("../escape", "pi"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestRefuseProfileSymlinks(t *testing.T) {
	for _, component := range []string{"profiles", "profiles/work", "profiles/work/pi"} {
		t.Run(component, func(t *testing.T) {
			p := Paths{Root: filepath.Join(t.TempDir(), "data")}
			path := filepath.Join(p.Root, component)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			target := t.TempDir()
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := p.EnsureAgentDir("work", "pi"); err == nil {
				t.Fatal("symlink accepted")
			}
			entries, _ := os.ReadDir(target)
			if len(entries) != 0 {
				t.Fatal("wrote outside profile storage")
			}
		})
	}
}

func TestRejectOverlappingAndAmbiguousMounts(t *testing.T) {
	p := Paths{Root: "/home/user/.agentnook"}
	for _, project := range []string{"/", "/home/user", p.Root, p.Root + "/profiles/work", "/repo:bad", "/repo\nname"} {
		if err := p.ValidateProject(project); err == nil {
			t.Errorf("accepted %q", project)
		}
	}
	for _, project := range []string{"/home/user/repo", "/home/user/.agentnook-other", "/repo with spaces"} {
		if err := p.ValidateProject(project); err != nil {
			t.Error(err)
		}
	}
	p.Root = "/bad:data"
	if err := p.ValidateProject("/repo"); err == nil {
		t.Fatal("ambiguous root accepted")
	}
}
