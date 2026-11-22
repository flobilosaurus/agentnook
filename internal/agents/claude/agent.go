// Package claude owns the Claude Code adapter, hooks and guest supervisor.
package claude

import (
	_ "embed"
	"encoding/json"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

type Adapter struct{}

var _ agents.Agent = Adapter{}

func (Adapter) Spec() agents.Spec {
	return agents.Spec{Name: "claude", Label: "Claude Code", Package: "@anthropic-ai/claude-code"}
}
func (Adapter) ValidateVersion(version string) error { return agents.ValidateVersion(version) }
func (a Adapter) InstallScript() string              { return agents.InstallScript(a.Spec(), "") }
func (Adapter) Environment() []string {
	return []string{"CLAUDE_CONFIG_DIR=/root/.claude", "DISABLE_AUTOUPDATER=1"}
}
func (a Adapter) Command(pass []string) []string { return agents.Command(a.Spec().Name, pass) }
func (Adapter) ReservedPaths() []string          { return nil }

//go:embed supervisor.mjs
var supervisor []byte

//go:embed hook.mjs
var hook []byte

func (Adapter) Telemetry(tty bool, pass []string) *agents.TelemetryPlan {
	if !agents.Interactive(tty, pass,
		[]string{"-p", "--print", "--settings", "--init-only", "--json-schema", "--output-format", "--input-format"},
		[]string{"auth", "doctor", "install", "update", "upgrade", "mcp", "plugin", "agents", "setup-token"}) {
		return nil
	}
	hooks := make(map[string]any)
	for _, event := range []string{"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "PermissionRequest", "PermissionDenied", "Stop", "StopFailure", "Notification", "Elicitation", "ElicitationResult"} {
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "node " + agents.TelemetryDir + "/claude-hook.mjs", "timeout": 2}}}}
	}
	settings, _ := json.Marshal(map[string]any{"hooks": hooks})
	return &agents.TelemetryPlan{
		Directories: []string{"events"},
		Files:       map[string][]byte{"claude.mjs": supervisor, "claude-hook.mjs": hook, "report.mjs": agents.ReportSource(), "claude-settings.json": settings},
		Command:     []string{"node", agents.TelemetryDir + "/claude.mjs"},
	}
}
