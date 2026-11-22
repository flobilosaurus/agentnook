package cli

import "testing"

func TestImageBuildRejectsAdapterSpecificInvalidVersionWithoutWriting(t *testing.T) {
	f := setup(t)
	f.run(t, 2, "images", "build", "wrong-version", "--agent", "opencode", "--agent-version", "1.19.6")
	assertNoData(t, f)
}

func TestImageBuildCLIResolvesAndSetsDefaultsPerAgent(t *testing.T) {
	f := setup(t)
	for _, agent := range []string{"pi", "opencode", "claude"} {
		name := agent + "-dev"
		f.run(t, 0, "images", "build", name, "--agent", agent, "--set-default")
		c := f.load(t)
		e, found := c.FindImage(name)
		if !found || e.VM.Agent != agent || e.VM.AgentVersion == "" || c.DefaultImages[agent] != name {
			t.Fatal(c)
		}
	}
	c := f.load(t)
	if f.resolves != 3 || f.runs != 0 || len(c.DefaultImages) != 3 || len(c.Profiles) != 0 {
		t.Fatal("build leaked defaults, launched VM or created profile", c)
	}
	f.run(t, 0, "images", "default", "claude-dev")
	if f.load(t).DefaultImages["pi"] != "pi-dev" {
		t.Fatal("Claude default changed Pi default")
	}
}
