package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/vm"
)

func herdrEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "test:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/not/a/real/socket")
	t.Setenv("AGENTNOOK_HERDR", "1")
}

func TestHerdrLaunchReportsAndCleansUpOnVMFailure(t *testing.T) {
	f := setup(t)
	herdrEnv(t)
	log := filepath.Join(f.root, "herdr.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + fmt.Sprintf("%q", log) + "\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "herdr"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var telemetry string
	f.app.Runner = func(_ string, args []string, _ vm.IO) int {
		var runID string
		for _, arg := range args {
			if strings.HasSuffix(arg, ":/run/agentnook-telemetry") {
				telemetry = strings.TrimSuffix(arg, ":/run/agentnook-telemetry")
			}
			if strings.HasPrefix(arg, "AGENTNOOK_REPORT_ID=") {
				runID = strings.TrimPrefix(arg, "AGENTNOOK_REPORT_ID=")
			}
		}
		if telemetry == "" || runID == "" {
			t.Fatal("missing telemetry", args)
		}
		if strings.Contains(strings.Join(args, " "), "/not/a/real/socket") {
			t.Fatal("socket leaked")
		}
		if !strings.HasSuffix(strings.Join(args, " "), "--continue prompt with spaces") {
			t.Fatal("Pi arguments changed")
		}
		deadline := time.Now().Add(5 * time.Second)
		seq := 0
		for time.Now().Before(deadline) {
			seq++
			report := fmt.Sprintf(`{"version":2,"runId":%q,"seq":%d,"sessionId":"guest-session","state":"working","uptimeSeconds":42}`, runID, seq)
			if err := os.WriteFile(filepath.Join(telemetry, "status.json.tmp"), []byte(report), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(telemetry, "status.json.tmp"), filepath.Join(telemetry, "status.json")); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(log)
			if strings.Contains(string(data), "report-agent") {
				return 17
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("bridge never reported")
		return 0
	}
	f.run(t, 17, "--profile", "work", "--", "--continue", "prompt with spaces")
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"--source agentnook", "--agent agentnook", "--state working", "agentnook_session_id=guest-session", "--clear-token agentnook_session_id", "release-agent test:p1"} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("missing %q: %s", text, data)
		}
	}
	if _, err := os.Stat(telemetry); !os.IsNotExist(err) {
		t.Fatal("telemetry remains")
	}
	entries, err := os.ReadDir(filepath.Join(f.data, "profiles", "work", "pi"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("profile modified: %v %v", entries, err)
	}
}

func TestHerdrUnavailableStillLaunchesPi(t *testing.T) {
	f := setup(t)
	herdrEnv(t)
	f.exit = 23
	f.run(t, 23, "--profile", "work")
	if !strings.Contains(f.errOut.String(), "telemetry remains enabled") {
		t.Fatal(f.errOut.String())
	}
	if !strings.Contains(strings.Join(f.argv, " "), "/run/agentnook-telemetry") {
		t.Fatal("failed adapter disabled shared telemetry")
	}
}

// Run the actual VM supervisor in a separate process so signals cannot affect
// the test runner. No real Herdr socket, VM or credentials are used.
func TestHerdrSignalHelper(t *testing.T) {
	if os.Getenv("AGENTNOOK_TEST_SIGNAL_HELPER") != "1" {
		return
	}
	app := App{Interactive: func() bool { return true }}
	os.Exit(app.Run([]string{"--agent", "pi", "--profile", "work", "--agent-version", "1.4.0"}))
}

func TestHerdrSignalReleasesAuthority(t *testing.T) {
	f := setup(t)
	addCatalogImage(t, f, "work-v1")
	herdrEnv(t)
	dirCapture := filepath.Join(f.root, "telemetry-dir")
	t.Setenv("AGENTNOOK_TEST_TELEMETRY_DIR", dirCapture)
	log := filepath.Join(f.root, "herdr.log")
	herdrScript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + fmt.Sprintf("%q", log) + "\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "herdr"), []byte(herdrScript), 0o700); err != nil {
		t.Fatal(err)
	}
	vmScript := `#!/bin/sh
if [ "$1" = --version ]; then printf "smolvm 1.36.1\n"; exit 0; fi
case "$1 $2" in
  "machine data-dir") printf '%s\n' "$AGENTNOOK_HOME"; exit 0 ;;
  "machine start"|"machine stop"|"machine delete") exit 0 ;;
esac
for arg in "$@"; do
  case "$arg" in
    *:/run/agentnook-telemetry) dir=${arg%:/run/agentnook-telemetry} ;;
    AGENTNOOK_REPORT_ID=*) run=${arg#AGENTNOOK_REPORT_ID=} ;;
  esac
done
if [ "$2" = create ]; then
  printf '%s\n' "$dir" > "$AGENTNOOK_TEST_TELEMETRY_DIR"
  exit 0
fi
IFS= read -r dir < "$AGENTNOOK_TEST_TELEMETRY_DIR"
trap 'exit 129' HUP
seq=0
while :; do
  seq=$((seq+1))
  printf '{"version":2,"runId":"%s","seq":%s,"sessionId":"signal-session","state":"idle","uptimeSeconds":42}' "$run" "$seq" > "$dir/status.json.tmp"
  /bin/mv "$dir/status.json.tmp" "$dir/status.json"
  /bin/sleep 0.1
done
`
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "smolvm"), []byte(vmScript), 0o700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestHerdrSignalHelper$")
	cmd.Env = append(os.Environ(), "AGENTNOOK_TEST_SIGNAL_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(log)
		if strings.Contains(string(data), "report-agent") {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		cancel()
		_ = cmd.Wait()
		t.Fatal("subprocess never reported")
	}
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 129 {
		t.Fatalf("signal exit: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(data), "release-agent") || !strings.Contains(string(data), "--clear-token") {
		t.Fatalf("missing signal cleanup: %v %s", err, data)
	}
	dir, err := os.ReadFile(dirCapture)
	if err != nil || len(dir) == 0 {
		t.Fatalf("missing telemetry path: %v", err)
	}
	if _, err := os.Stat(string(dir)); !os.IsNotExist(err) {
		t.Fatal("signal did not clean up shared telemetry", err)
	}
}

func TestHerdrDryRunDoesNotPrepareTelemetry(t *testing.T) {
	f := setup(t)
	addCatalogImage(t, f, "work-v1")
	herdrEnv(t)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	f.run(t, 0, "--dry-run", "--profile", "work")
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatal("dry-run created telemetry")
	}
	if strings.Contains(f.out.String(), "agentnook-telemetry") {
		t.Fatal("dry-run has transient mount")
	}
	if f.runs != 0 || len(f.load(t).Projects) != 0 {
		t.Fatal("dry-run saved a project or started VM")
	}
}
