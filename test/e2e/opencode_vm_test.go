package e2e

import "testing"

func TestRealOpenCodeVM(t *testing.T) {
	realVMSmoke(t, "opencode", "2.3.2")
}
