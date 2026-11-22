package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestHelpSeparatesEverydayAndAdvancedOptions(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"run", "--help"}, {"help"}} {
		f := setup(t)
		t.Setenv("PATH", "")
		f.run(t, 0, args...)
		help := f.out.String()
		for _, want := range []string{"First run:", "Later runs:", "--select", "--help-all", "nook -- --continue"} {
			if !strings.Contains(help, want) {
				t.Fatalf("missing %q: %s", want, help)
			}
		}
		for _, hidden := range []string{"--agent NAME", "--profile NAME", "--image NAME", "--select-image", "--cpus", "nook images build"} {
			if strings.Contains(help, hidden) {
				t.Fatalf("everyday help exposes %q", hidden)
			}
		}
		assertNoData(t, f)
	}
	for _, args := range [][]string{{"--help-all"}, {"run", "--help-all"}} {
		f := setup(t)
		t.Setenv("PATH", "")
		f.run(t, 0, args...)
		help := f.out.String()
		for _, want := range []string{"--agent NAME", "--profile NAME", "--image NAME", "--select-image", "--agent-version", "--cpus", "nook images build"} {
			if !strings.Contains(help, want) {
				t.Fatalf("advanced help missing %q", want)
			}
		}
		assertNoData(t, f)
	}
}

func TestNormalStartsReuseSetupWithoutAnyPicker(t *testing.T) {
	f := setup(t)
	picker := &fakeImagePrompter{}
	f.app.ImagePrompter = picker
	f.run(t, 0)
	first := stableLaunchArgs(f.argv)
	if f.prompt.calls != 1 || f.agentPrompt.calls != 1 || picker.calls != 0 {
		t.Fatal("unexpected first setup prompts")
	}
	for range 3 {
		f.run(t, 0)
		if !reflect.DeepEqual(first, stableLaunchArgs(f.argv)) {
			t.Fatal("normal start changed saved setup")
		}
	}
	if f.prompt.calls != 1 || f.agentPrompt.calls != 1 || picker.calls != 0 || f.resolves != 1 {
		t.Fatal("normal starts prompted or rebuilt", f.prompt.calls, f.agentPrompt.calls, picker.calls, f.resolves)
	}
}
