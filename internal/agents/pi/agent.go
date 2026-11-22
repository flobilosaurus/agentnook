// Package pi owns the Pi adapter and its guest extension.
package pi

import (
	_ "embed"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

type Adapter struct{}

var _ agents.Agent = Adapter{}

func (Adapter) Spec() agents.Spec {
	return agents.Spec{Name: "pi", Label: "Pi", Package: "@earendil-works/pi-coding-agent"}
}
func (Adapter) ValidateVersion(version string) error { return agents.ValidateVersion(version) }
func (a Adapter) InstallScript() string              { return agents.InstallScript(a.Spec(), "") }
func (Adapter) Environment() []string                { return []string{"PI_CODING_AGENT_DIR=/root/.pi/agent"} }
func (a Adapter) Command(pass []string) []string     { return agents.Command(a.Spec().Name, pass) }
func (Adapter) ReservedPaths() []string              { return nil }

//go:embed status.ts
var extension []byte

func (Adapter) Telemetry(tty bool, pass []string) *agents.TelemetryPlan {
	if !agents.Interactive(tty, pass, []string{"-p", "--print", "--mode", "--no-extensions", "--list-models"}, nil) {
		return nil
	}
	return &agents.TelemetryPlan{
		Files:   map[string][]byte{"status.ts": extension},
		Command: []string{"pi", "--extension", agents.TelemetryDir + "/status.ts"},
	}
}
