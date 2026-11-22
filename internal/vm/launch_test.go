package vm

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestFindRequiresSupportedSmolvm(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{"smolvm 1.33.0", true}, {"smolvm 1.34.2", true}, {"smolvm 2.0.0", true},
		{"smolvm 1.32.99", false}, {"smolvm 1.33.0-rc1", false}, {"", false},
		{"something 99.0.0", false}, {"smolvm 2.-1.0", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' '" + tc.version + "'\n"
			if err := os.WriteFile(filepath.Join(dir, "smolvm"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			binary, err := Find()
			if (err == nil) != tc.ok {
				t.Fatal(binary, err)
			}
			if tc.ok && binary != filepath.Join(dir, "smolvm") {
				t.Fatal(binary)
			}
			if !tc.ok && !strings.Contains(err.Error(), "smolvm >= 1.33.0") {
				t.Fatal(err)
			}
		})
	}
	t.Setenv("PATH", "")
	if _, err := Find(); err == nil {
		t.Fatal("missing smolvm accepted")
	}
}

func TestLaunchDisposableLifecycleAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		failure    string
		exit, want int
		stages     []string
	}{
		{"", 0, 0, []string{"machine create", "machine start", "machine exec", "machine delete"}},
		{"", 23, 23, []string{"machine create", "machine start", "machine exec", "machine delete"}},
		{"create", 0, 7, []string{"machine create", "machine delete"}},
		{"start", 0, 7, []string{"machine create", "machine start", "machine delete"}},
		{"delete", 0, 1, []string{"machine create", "machine start", "machine exec", "machine delete"}},
	} {
		t.Run(tc.failure+strconv.Itoa(tc.exit), func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			binary := filepath.Join(dir, "smolvm")
			t.Setenv("VM_TEST_LOG", log)
			t.Setenv("VM_TEST_FAIL", tc.failure)
			script := `#!/bin/sh
printf '%s %s\n' "$1" "$2" >> "$VM_TEST_LOG"
printf '%s\000' "$@" > "$VM_TEST_LOG.$2"
if [ "$2" = "$VM_TEST_FAIL" ]; then exit 7; fi
if [ "$2" = exec ]; then exit ` + strconv.Itoa(tc.exit) + `; fi
exit 0
`
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			pass := []string{"pi", "--print", "spaces ; $(echo nope)", "--select"}
			args := []string{"machine", "run", "--net", "--interactive", "--from", "/private/image.smolmachine", "--cpus", "3", "--mem", "4096", "--storage", "8", "--user", "root", "--workdir", "/workspace", "--env", "HOME=/root", "--volume", "/project with spaces:/workspace", "--volume", "/profile:/root", "--tty", "--"}
			args = append(args, pass...)
			var output bytes.Buffer
			if got := Launch(binary, args, IO{Err: &output}); got != tc.want {
				t.Fatalf("exit %d want %d: %s", got, tc.want, &output)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Split(strings.TrimSpace(string(calls)), "\n"); !reflect.DeepEqual(got, tc.stages) {
				t.Fatal(got)
			}
			if tc.failure != "" {
				return
			}
			create, _ := os.ReadFile(log + ".create")
			execute, _ := os.ReadFile(log + ".exec")
			created := strings.Split(strings.TrimSuffix(string(create), "\x00"), "\x00")
			executed := strings.Split(strings.TrimSuffix(string(execute), "\x00"), "\x00")
			for _, want := range []string{"agentnook.image=/private/image.smolmachine", "agentnook.ephemeral=true", "/project with spaces:/workspace", "/profile:/root", "--from"} {
				if !strings.Contains(string(create), want+"\x00") {
					t.Fatal(created)
				}
			}
			if !reflect.DeepEqual(executed[len(executed)-len(pass):], pass) {
				t.Fatal(executed)
			}
			if created[3] != executed[3] || !strings.HasPrefix(created[3], "nook-run-") {
				t.Fatal(created, executed)
			}
			deleted, _ := os.ReadFile(log + ".delete")
			if !bytes.Contains(deleted, []byte(created[3]+"\x00")) {
				t.Fatal("wrong cleanup target")
			}
		})
	}
}

func TestCanceledSessionDoesNotStartNextCommand(t *testing.T) {
	session := NewSession()
	defer session.Close()
	session.signals <- syscall.SIGHUP
	if code := session.Run("/definitely-not-an-executable", nil, IO{}); code != 129 {
		t.Fatal(code)
	}
	if code := session.Run("/definitely-not-an-executable", nil, IO{}); code != 129 {
		t.Fatal(code)
	}
}

func TestLaunchRejectsInvalidPlanWithoutStartingRuntime(t *testing.T) {
	for _, args := range [][]string{nil, {"machine", "run"}, {"machine", "run", "--from"}, {"machine", "run", "--from", "/image.smolmachine"}, {"machine", "run", "--", "pi"}} {
		if got := Launch("/missing", args, IO{}); got != 1 {
			t.Fatal(args, got)
		}
	}
}
