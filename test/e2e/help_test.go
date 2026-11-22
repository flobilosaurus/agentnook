package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestNormalAndAdvancedHelpHaveNoSideEffects(t *testing.T) {
	s := newSandbox(t)
	for _, args := range [][]string{{"--help"}, {"run", "--help"}, {"--help-all"}, {"run", "--help-all"}} {
		out, stderr, code := s.run(t, s.project, "", 0, args...)
		advanced := args[len(args)-1] == "--help-all"
		if code != 0 || stderr != "" || !strings.Contains(out, "Later runs:") {
			t.Fatal(code, out, stderr)
		}
		if strings.Contains(out, "--agent NAME") != advanced || strings.Contains(out, "--image NAME") != advanced {
			t.Fatal("help does not separate daily use from image/agent management", out)
		}
		for _, path := range []string{s.data, s.capture, s.capture + ".build", s.capture + ".machines"} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("help caused side effects", path, err)
			}
		}
	}
}
