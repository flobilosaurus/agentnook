package vm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestArgsIncludeReadWriteAndReadOnlyExtraMountsBeforeCommand(t *testing.T) {
	mounts := []config.Mount{
		{Host: "/host/with spaces ; $(echo nope)", Guest: "/mnt/shared"},
		{Host: "/host/docs", Guest: "/workspace/docs", ReadOnly: true},
	}
	pass := []string{"--print", "--mount", "not-a-launcher-mount"}
	args := Args("/project", "/profile/.pi", config.Defaults(), testAgent(t, "pi"), false, pass, mounts...)
	var volumes []string
	for i := 0; i < len(args) && args[i] != "--"; i++ {
		if args[i] == "--volume" {
			volumes = append(volumes, args[i+1])
		}
	}
	want := []string{"/project:/workspace", "/profile/.pi:/root",
		"/host/with spaces ; $(echo nope):/mnt/shared", "/host/docs:/workspace/docs:ro"}
	if !reflect.DeepEqual(volumes, want) {
		t.Fatalf("volumes=%q want=%q", volumes, want)
	}
	if !reflect.DeepEqual(args[len(args)-len(pass):], pass) {
		t.Fatal("Pi arguments changed")
	}
	if strings.Contains(strings.Join(args, "\n"), ":rw") {
		t.Fatal("unexpected explicit write mode")
	}
}
