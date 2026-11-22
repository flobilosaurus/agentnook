package pi

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestExtension(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node >=22.19 required for guest extension test")
	}
	version, err := exec.Command(node, "--version").Output()
	var major, minor int
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(string(version)), "v%d.%d", &major, &minor); err != nil {
		t.Fatal(err)
	}
	if major < 22 || (major == 22 && minor < 19) {
		t.Skip("Node >=22.19 required for guest extension test")
	}
	path, err := filepath.Abs("status.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--experimental-strip-types", "status_test.mjs", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extension: %v\n%s", err, output)
	}
}
