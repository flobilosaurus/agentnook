package telemetry

import (
	"os"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

type plannedAgent struct {
	agents.Agent
	plan *agents.TelemetryPlan
}

func (a plannedAgent) Telemetry(bool, []string) *agents.TelemetryPlan { return a.plan }

func TestInvalidAdapterPlansCannotWriteOutsidePrivateRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan agents.TelemetryPlan
	}{
		{"parent file", agents.TelemetryPlan{Files: map[string][]byte{"../escape": nil}, Command: []string{"agent"}}},
		{"absolute file", agents.TelemetryPlan{Files: map[string][]byte{"/escape": nil}, Command: []string{"agent"}}},
		{"parent directory", agents.TelemetryPlan{Directories: []string{"../escape"}, Command: []string{"agent"}}},
		{"parent mount", agents.TelemetryPlan{Mounts: []agents.TelemetryMount{{Source: "../escape", Guest: "/guest"}}, Command: []string{"agent"}}},
		{"absolute mount", agents.TelemetryPlan{Mounts: []agents.TelemetryMount{{Source: "/host", Guest: "/guest"}}, Command: []string{"agent"}}},
		{"relative guest", agents.TelemetryPlan{Mounts: []agents.TelemetryMount{{Source: "local", Guest: "relative"}}, Command: []string{"agent"}}},
		{"ambiguous guest", agents.TelemetryPlan{Mounts: []agents.TelemetryMount{{Source: "local", Guest: "/guest:ro"}}, Command: []string{"agent"}}},
		{"empty command", agents.TelemetryPlan{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			launch, err := Prepare(plannedAgent{plan: &tc.plan}, true, nil)
			if err == nil || launch != nil {
				if launch != nil {
					launch.Close()
				}
				t.Fatal("invalid plan accepted")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid plan touched filesystem", entries, err)
			}
		})
	}
}
