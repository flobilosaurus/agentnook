package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestNetworkHelpAndHeadlessModeHaveNoSideEffects(t *testing.T) {
	s := newSandbox(t)
	for _, args := range [][]string{{"network", "--help"}, {"network"}, {"--network", "invalid"}} {
		out, stderr, code := s.run(t, s.project, "", 0, args...)
		if args[0] == "network" && len(args) == 2 {
			if code != 0 || !strings.Contains(out, "12 seconds") {
				t.Fatal(code, out, stderr)
			}
		} else if code == 0 {
			t.Fatal("invalid or noninteractive command succeeded", args)
		}
		if _, err := os.Stat(s.data); !os.IsNotExist(err) {
			t.Fatal("network command created storage", err)
		}
	}
}

func TestNetworkRejectsUnsupportedRuntimeBeforeSetup(t *testing.T) {
	s := newSandbox(t)
	_, stderr, code := s.run(t, s.project, "old-runtime", 0, "--agent", "pi", "--profile", "work", "--network", "ask")
	if code != 127 || !strings.Contains(stderr, "smolvm >= 1.36.1") {
		t.Fatal(code, stderr)
	}
	if _, err := os.Stat(s.data); !os.IsNotExist(err) {
		t.Fatal("unsupported runtime changed storage", err)
	}
}
