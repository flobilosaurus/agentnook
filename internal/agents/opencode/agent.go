// Package opencode owns the OpenCode v2 adapter and its guest plugin.
package opencode

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

const PluginDir = "/etc/opencode/plugins/agentnook"

type Adapter struct{}

var _ agents.Agent = Adapter{}

func (Adapter) Spec() agents.Spec {
	return agents.Spec{Name: "opencode", Label: "OpenCode", Package: "@opencode/cli"}
}
func (Adapter) ValidateVersion(version string) error {
	if err := agents.ValidateVersion(version); err != nil {
		return err
	}
	if !strings.HasPrefix(version, "2.") {
		return fmt.Errorf("OpenCode requires a 2.x version (@opencode/cli)")
	}
	return nil
}
func (a Adapter) InstallScript() string {
	return agents.InstallScript(a.Spec(), `mkdir -p /etc/opencode && printf '%s\n' '{"update":"disable"}' > /etc/opencode/opencode.json
`)
}
func (Adapter) Environment() []string {
	return []string{"XDG_CONFIG_HOME=/root/.config", "XDG_DATA_HOME=/root/.local/share", "XDG_STATE_HOME=/root/.local/state", "XDG_CACHE_HOME=/root/.cache"}
}
func (a Adapter) Command(pass []string) []string { return agents.Command(a.Spec().Name, pass) }
func (Adapter) ReservedPaths() []string          { return []string{PluginDir} }

//go:embed plugin.mjs
var plugin []byte

func (a Adapter) Telemetry(tty bool, pass []string) *agents.TelemetryPlan {
	if !agents.Interactive(tty, pass, nil, []string{"run", "serve", "web", "auth", "debug", "models", "mcp", "plugin", "upgrade", "uninstall", "export", "import", "session", "attach"}) {
		return nil
	}
	return &agents.TelemetryPlan{
		Directories: []string{"opencode"},
		Files: map[string][]byte{
			"opencode/index.mjs":    plugin,
			"opencode/report.mjs":   agents.ReportSource(),
			"opencode/package.json": []byte(`{"name":"agentnook-telemetry","type":"module","exports":{".":"./index.mjs"}}`),
		},
		Mounts:  []agents.TelemetryMount{{Source: "opencode", Guest: PluginDir, ReadOnly: true}},
		Command: a.Command(nil),
	}
}
