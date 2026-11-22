package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/telemetry"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

func TestInteractiveTelemetryIndependentOfHerdr(t *testing.T) {
	for _, mode := range []string{"outside-herdr", "opted-out", "missing-pane", "missing-socket"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			herdrEnv(t)
			switch mode {
			case "outside-herdr":
				t.Setenv("HERDR_ENV", "")
			case "opted-out":
				t.Setenv("AGENTNOOK_HERDR", "0")
			case "missing-pane":
				t.Setenv("HERDR_PANE_ID", "")
			case "missing-socket":
				t.Setenv("HERDR_SOCKET_PATH", "")
			}
			var dir string
			f.app.Runner = func(_ string, args []string, _ vm.IO) int {
				for _, arg := range args {
					if strings.HasSuffix(arg, ":"+telemetry.GuestDir) {
						dir = strings.TrimSuffix(arg, ":"+telemetry.GuestDir)
					}
				}
				if dir == "" {
					t.Fatal("missing shared telemetry", args)
				}
				if _, err := os.Stat(filepath.Join(dir, "status.ts")); err != nil {
					t.Fatal(err)
				}
				r, err := telemetry.Open(dir)
				if err != nil {
					t.Fatal(err)
				}
				_ = r.Close()
				return 23
			}
			f.run(t, 23, "--profile", "work")
			if strings.Contains(f.errOut.String(), "reporting unavailable") {
				t.Fatal("adapter activated without usable pane context", f.errOut.String())
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("telemetry not cleaned up", err)
			}
		})
	}
}

func TestLaunchWithoutInteractiveTelemetry(t *testing.T) {
	for _, mode := range []string{"no-tty", "--print", "--mode=rpc", "--no-extensions"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			herdrEnv(t)
			args := []string{"--agent", "pi", "--profile", "work"}
			if mode == "no-tty" {
				f.app.Interactive = func() bool { return false }
			} else {
				args = append(args, "--", mode)
			}
			f.run(t, 0, args...)
			if strings.Contains(strings.Join(f.argv, " "), "agentnook-telemetry") || strings.Contains(f.errOut.String(), "reporting unavailable") {
				t.Fatal("telemetry or adapter activated", f.argv, f.errOut.String())
			}
		})
	}
}

func TestTelemetryPreparationFailurePreservesPiExitCode(t *testing.T) {
	f := setup(t)
	t.Setenv("TMPDIR", filepath.Join(f.root, "missing"))
	f.exit = 17
	f.run(t, 17, "--profile", "work")
	if !strings.Contains(f.errOut.String(), "activity reporting unavailable") || strings.Contains(strings.Join(f.argv, " "), "agentnook-telemetry") {
		t.Fatal("preparation failure changed launch", f.errOut.String(), f.argv)
	}
}
