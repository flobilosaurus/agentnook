package e2e

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const exampleEnv = "AGENTNOOK_E2E_EXAMPLE"
const envProbeFile = ".agentnook-env-probe"

// Each phase uses a new VM. The second must reuse the saved name, not the first value.
func testEnvironmentLaunches(t *testing.T, check func(*testing.T, context.Context, []string, string)) {
	t.Helper()
	for _, tc := range []struct {
		name  string
		flags []string
		value string
	}{
		{"Explicit", []string{"--env", exampleEnv}, "example with spaces and $HOME"},
		{"Saved", nil, "updated host value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			check(t, ctx, tc.flags, tc.value)
		})
	}
}

// The agent's plugin or hook writes its observed value to the mounted workspace.
func (f realVMFixture) testEnvironmentProbe(t *testing.T, agentArgs ...string) {
	t.Helper()
	probe := filepath.Join(f.workspace, envProbeFile)
	testEnvironmentLaunches(t, func(t *testing.T, ctx context.Context, flags []string, want string) {
		if err := os.Remove(probe); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		cmd := f.command(ctx, append(append(flags, "--"), agentArgs...)...)
		cmd.Env = append(cmd.Env, exampleEnv+"="+want)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("agent launch: %v\n%s", err, output)
		}
		got, err := os.ReadFile(probe)
		if err != nil || string(got) != want {
			t.Fatalf("agent observed %q, want %q: %v\n%s", got, want, err, output)
		}
	})
}
