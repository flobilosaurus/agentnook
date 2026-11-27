package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestShortHelpPointsToCommandHelpAndLaunchReference(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"run", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := setup(t)
			t.Setenv("PATH", "")
			f.run(t, 0, args...)
			help := f.out.String()
			for _, want := range []string{
				"reuse the saved setup", "--select", "--env", "--mount",
				"Commands:", "  nook images ", "  nook watch ", "  nook network ",
				"nook <command> --help", "nook --help-all",
				"Everything after -- is passed unchanged", "nook -- --continue",
			} {
				if !strings.Contains(help, want) {
					t.Fatalf("short help missing %q:\n%s", want, help)
				}
			}
			for _, hidden := range []string{"--agent NAME", "--cpus", "--mount HOST:GUEST", "--env NAME", "--clear-", "--network ask|full", "nook images build"} {
				if strings.Contains(help, hidden) {
					t.Fatalf("short help exposes %q:\n%s", hidden, help)
				}
			}
			assertNoData(t, f)
		})
	}
}

func TestFullHelpListsLaunchOptionsAndLinksToSubcommandHelp(t *testing.T) {
	for _, args := range [][]string{{"--help-all"}, {"run", "--help-all"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := setup(t)
			t.Setenv("PATH", "")
			f.run(t, 0, args...)
			help := f.out.String()
			for _, want := range []string{
				"--select", "--agent NAME", "--profile NAME", "--image NAME", "--select-image", "--agent-version VERSION", "--images",
				"--mount HOST:GUEST[:ro]", "--clear-mounts", "--env NAME", "--clear-env", "Only env names are saved",
				"--network ask|full", "--allow-private CIDR", "--clear-allow-private",
				"--cpus N", "--mem MiB", "--storage GiB", "--dry-run", "--version", "--help-all",
				"nook images --help", "nook watch --help", "nook network --help",
			} {
				if !strings.Contains(help, want) {
					t.Fatalf("launch reference missing %q:\n%s", want, help)
				}
			}
			if strings.Count(help, "Usage:") != 1 || strings.Contains(help, "nook images build") {
				t.Fatalf("launch reference duplicates other help:\n%s", help)
			}
			assertNoData(t, f)
		})
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
