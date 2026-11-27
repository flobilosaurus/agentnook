package e2e

import "testing"

func TestRealClaudeVM(t *testing.T) {
	fixture, _ := realVMSmoke(t, "claude", "2.1.323")
	t.Run("EnvironmentForwarding", func(t *testing.T) {
		const settings = `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"printf '%s' \"$` + exampleEnv + `\" > /workspace/` + envProbeFile + `"}]}]}}`
		// Trigger startup hooks without signing in or sending a model request.
		fixture.testEnvironmentProbe(t, "--init-only", "--strict-mcp-config", "--settings", settings)
	})
}
