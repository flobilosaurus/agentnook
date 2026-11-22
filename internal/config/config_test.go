package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testOptions() Options {
	o := Defaults()
	o.Agent = "pi"
	o.Image = BaseImage
	o.AgentVersion = "1.4.0"
	return o
}
func testWorkspace(profile string, o Options) Workspace {
	return Workspace{Agent: o.Agent, Agents: map[string]Project{o.Agent: {Profile: profile, VM: o}}}
}
func TestProfileNames(t *testing.T) {
	for _, name := range []string{"work", "Privat_2", "a.b-c", strings.Repeat("x", 64)} {
		if err := ValidateProfileName(name); err != nil {
			t.Error(err)
		}
	}
	for _, name := range []string{"", ".", "..", "../escape", "a..b", "/abs", " work", "work ", "foo/bar", "a:b", strings.Repeat("x", 65)} {
		if ValidateProfileName(name) == nil {
			t.Errorf("accepted %q", name)
		}
	}
}
func TestOptionsValidation(t *testing.T) {
	if err := testOptions().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Options){func(o *Options) { o.CPUs = 0 }, func(o *Options) { o.Memory = -1 }, func(o *Options) { o.Storage = 0 }, func(o *Options) { o.Image = "" }, func(o *Options) { o.Image = "-" }, func(o *Options) { o.Image = "node\n--volume" }, func(o *Options) { o.AgentVersion = "latest" }, func(o *Options) { o.AgentVersion = "1.4.0; touch /tmp/nope" }, func(o *Options) { o.Agent = "bad/name" }} {
		o := testOptions()
		change(&o)
		if o.Validate() == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
}
func TestNormalizeProjectPath(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "alias")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	a, err := NormalizeProjectPath(real)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NormalizeProjectPath(link + "/.")
	if err != nil || a != b {
		t.Fatal(a, b, err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", filepath.Join(dir, "missing"), file} {
		if _, err := NormalizeProjectPath(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
}
func TestSaveLoadAndAgentSelections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "config.toml")
	c, err := Load(path)
	if err != nil || len(c.Projects) != 0 {
		t.Fatal(c, err)
	}
	for _, name := range []string{"work", "alpha"} {
		if err := c.AddProfile(name); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"work", "WORK", "../bad"} {
		if c.AddProfile(name) == nil {
			t.Fatal("accepted duplicate/invalid", name)
		}
	}
	w := testWorkspace("work", testOptions())
	o := testOptions()
	o.Agent = "claude"
	o.AgentVersion = "2.3.2"
	w.Agents["claude"] = Project{Profile: "alpha", VM: o}
	w.Agent = "claude"
	c.Projects["/project"] = w
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatal(got, c, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*"))
	if len(matches) != 0 {
		t.Fatal(matches)
	}
}
func TestVersionedResourceDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := "version = 1\n[resources]\ncpus = 3\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Resources.CPUs != 3 || c.Resources.Memory != 2048 || c.DefaultOptions("claude").Agent != "claude" {
		t.Fatal(c)
	}
}
func TestRejectBadAndOldConfigWithoutOverwriting(t *testing.T) {
	for _, content := range []string{"", "[defaults]\npi_version='1.4.0'\n", "version=0", "version=999", "not = [valid", "version=1\nsurprise=true", "version=1\n[resources]\ncpus=0", "version=1\n[[profiles]]\nname='../escape'", "version=1\n[[profiles]]\nname='work'\n[[profiles]]\nname='WORK'", "version=1\n[projects.'/somewhere']\nagent='pi'", "version=1\n[default_images]\npi='missing'"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("accepted %q", content)
		}
		if err := Update(path, func(c *Config) error { return c.AddProfile("new") }); err == nil {
			t.Errorf("updated invalid config %q", content)
		}
		got, _ := os.ReadFile(path)
		if string(got) != content {
			t.Fatal("invalid config overwritten")
		}
	}
}
func TestUpdateMergesConcurrentLaunches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	const count = 16
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			failures <- Update(path, func(c *Config) error {
				name := fmt.Sprintf("profile-%02d", i)
				if err := c.AddProfile(name); err != nil {
					return err
				}
				c.Projects[fmt.Sprintf("/project-%02d", i)] = testWorkspace(name, testOptions())
				return nil
			})
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := Load(path)
	if err != nil || len(c.Profiles) != count || len(c.Projects) != count {
		t.Fatal(c, err)
	}
	before, _ := os.ReadFile(path)
	if Update(path, func(*Config) error { return fmt.Errorf("cancel") }) == nil {
		t.Fatal("expected failed transaction")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed transaction persisted")
	}
}
func TestSaveRejectsInvalidMappings(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Projects["relative"] = testWorkspace("work", testOptions()) },
		func(c *Config) { w := testWorkspace("work", testOptions()); w.Agent = "claude"; c.Projects["/p"] = w },
		func(c *Config) {
			w := testWorkspace("work", testOptions())
			w.Agents["claude"] = w.Agents["pi"]
			c.Projects["/p"] = w
		},
	} {
		c := Empty()
		_ = c.AddProfile("work")
		change(&c)
		if Save(filepath.Join(t.TempDir(), "config.toml"), c) == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}
