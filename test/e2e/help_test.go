package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHelpPagesHaveNoSideEffects(t *testing.T) {
	s := newSandbox(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "nook <command> --help"},
		{[]string{"run", "--help"}, "nook <command> --help"},
		{[]string{"--help-all"}, "Directory shares and environment:"},
		{[]string{"run", "--help-all"}, "Directory shares and environment:"},
		{[]string{"images", "--help"}, "Build options:"},
		{[]string{"images", "status", "--help"}, "Usage:\n  nook images status"},
		{[]string{"watch", "--help"}, "Usage: nook watch"},
		{[]string{"network", "--help"}, "Usage: nook network"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, stderr, code := s.run(t, s.project, "", 0, tc.args...)
			if code != 0 || stderr != "" || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d; want %q in stdout, got:\n%s\nstderr: %s", code, tc.want, out, stderr)
			}
			for _, path := range []string{s.data, s.capture, s.capture + ".build", s.capture + ".machines"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("help caused side effects", path, err)
				}
			}
		})
	}
}
