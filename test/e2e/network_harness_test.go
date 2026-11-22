package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/egress"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

// networkVMHarness keeps each bare test VM and its smolvm state isolated. It
// never reads agent profiles, imports host credentials, or pulls an image.
type networkVMHarness struct {
	root, name, binary string
	env                []string
}

func newNetworkVMHarness(t *testing.T) networkVMHarness {
	t.Helper()
	if os.Getenv("AGENTNOOK_VM_SMOKE") != "1" {
		t.Skip("set AGENTNOOK_VM_SMOKE=1 to test real VM networking")
	}
	binary, err := vm.Find()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "nook-net-vm-")
	if err != nil {
		t.Fatal(err)
	}
	h := networkVMHarness{root: root, binary: binary, name: fmt.Sprintf("nook-run-network-test-%d", time.Now().UnixNano())}
	h.env = append(os.Environ(), "HOME="+root, "SMOLVM_DATA_DIR="+filepath.Join(root, "d"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "c"), "XDG_CACHE_HOME="+filepath.Join(root, "k"),
		"XDG_DATA_HOME="+filepath.Join(root, "d"), "XDG_STATE_HOME="+filepath.Join(root, "s"))
	t.Cleanup(func() {
		if err := removeSandbox(root); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if out, err := h.command(nil, "machine", "delete", "--name", h.name, "--force"); err != nil {
			t.Errorf("delete test VM: %v: %s", err, out)
		}
	})
	return h
}

func observeNetworkVM(t *testing.T, h networkVMHarness, root, mode string) func(bool) error {
	t.Helper()
	out, err := h.command(nil, "machine", "data-dir", "--name", h.name)
	if err != nil {
		t.Fatalf("data-dir: %v: %s", err, out)
	}
	finish, err := egress.ObserveDirectory(root, h.name, mode, egress.Info{Project: "/network-test", Profile: "test", Agent: "test"}, strings.TrimSpace(out))
	if err != nil {
		t.Fatal(err)
	}
	return finish
}

func (h networkVMHarness) command(extraEnv []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.binary, args...)
	cmd.Env = append(append([]string(nil), h.env...), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
