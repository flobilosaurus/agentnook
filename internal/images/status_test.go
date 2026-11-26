package images

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestDescribeStatusGroupsSavedAgentSelections(t *testing.T) {
	root := t.TempDir()
	pi := config.Options{Agent: "pi", Image: filepath.Join(root, "pi.smolmachine")}
	open := config.Options{Agent: "opencode", Image: filepath.Join(root, "opencode.smolmachine")}
	if err := os.WriteFile(open.Image, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.smolmachine")
	if err := os.Symlink(open.Image, alias); err != nil {
		t.Fatal(err)
	}
	c := config.Empty()
	c.Images = []config.BuiltImage{{Name: "pi-dev", VM: pi}, {Name: "oc-work", VM: open}}
	c.DefaultImages["pi"] = "PI-DEV"
	current := filepath.Join(root, "z-current")
	c.Projects[current] = config.Workspace{Agent: "opencode", Agents: map[string]config.Project{
		"opencode": {Profile: "work", VM: config.Options{Image: alias}},
		"pi":       {Profile: "private", VM: pi},
	}}
	for _, folder := range []string{"b-other", "a-other"} {
		c.Projects[filepath.Join(root, folder)] = config.Workspace{Agent: "pi", Agents: map[string]config.Project{
			"pi": {Profile: "private", VM: pi},
		}}
	}
	status := DescribeStatus(c, "/config.toml", current, nil)
	p, o := status.Images[0], status.Images[1]
	if p.Current || !p.Default || p.ArtifactState != "missing" || len(p.Assignments) != 3 || o.Default || !o.Current || len(o.Assignments) != 1 {
		t.Fatalf("wrong image summaries: %+v", status.Images)
	}
	if a := p.Assignments[0]; a.Path != current || !a.CurrentFolder || a.Active || a.Profile != "private" || a.AgentDefault != "PI-DEV" {
		t.Fatalf("wrong inactive assignment: %+v", a)
	}
	if p.Assignments[1].Path != filepath.Join(root, "a-other") || p.Assignments[2].Path != filepath.Join(root, "b-other") {
		t.Fatalf("other paths not sorted: %+v", p.Assignments)
	}
	if a := o.Assignments[0]; a.Path != current || !a.CurrentFolder || !a.Active || a.Profile != "work" || a.AgentDefault != "" {
		t.Fatalf("wrong active assignment: %+v", a)
	}
}
