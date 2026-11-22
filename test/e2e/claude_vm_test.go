package e2e

import "testing"

func TestRealClaudeVM(t *testing.T) {
	realVMSmoke(t, "claude", "2.1.323")
}
