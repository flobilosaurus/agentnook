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
		{"smolvm 1.36.1", true}, {"smolvm 1.37.0", true}, {"smolvm 2.0.0", true},
		{"smolvm 1.33.0", false}, {"smolvm 1.35.4", false}, {"smolvm 1.36.0", false},
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
			if !tc.ok && !strings.Contains(err.Error(), "smolvm >= 1.36.1") {
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
			const secret = "test-secret with spaces; $HOME\nsecond line"
			t.Setenv("VM_TEST_SECRET", secret)
			script := `#!/bin/sh
printf '%s %s\n' "$1" "$2" >> "$VM_TEST_LOG"
printf '%s\000' "$@" > "$VM_TEST_LOG.$2"
if [ "$2" = "$VM_TEST_FAIL" ]; then exit 7; fi
if [ "$2" = exec ]; then
    printf '%s' "$VM_TEST_SECRET" > "$VM_TEST_LOG.exec.env"
    exit ` + strconv.Itoa(tc.exit) + `
fi
exit 0
`
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			pass := []string{"pi", "--print", "spaces ; $(echo nope)", "--select"}
			args := []string{"machine", "run", "--net", "--block-private", "--allow-private", "10.20.0.0/16", "--interactive", "--from", "/private/image.smolmachine", "--cpus", "3", "--mem", "4096", "--storage", "8", "--user", "root", "--workdir", "/workspace", "--env", "HOME=/root", "--secret-env", "VM_TEST_SECRET=VM_TEST_SECRET", "--volume", "/project with spaces:/workspace", "--volume", "/profile:/root", "--tty", "--"}
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
			for _, want := range []string{"agentnook.image=/private/image.smolmachine", "agentnook.network=full", "agentnook.env=VM_TEST_SECRET", "agentnook.ephemeral=true", "/project with spaces:/workspace", "/profile:/root", "--from", "--block-private", "10.20.0.0/16"} {
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
			for _, stage := range []string{"create", "start", "exec", "delete"} {
				data, err := os.ReadFile(log + "." + stage)
				if err != nil || bytes.Contains(data, []byte(secret)) {
					t.Fatal(stage, err, "secret value must not appear in argv")
				}
				ref := []byte("--secret-env\x00VM_TEST_SECRET=VM_TEST_SECRET\x00")
				if bytes.Contains(data, ref) != (stage == "exec") {
					t.Fatal("secret reference must appear only in exec", stage)
				}
			}
			if got, err := os.ReadFile(log + ".exec.env"); err != nil || string(got) != secret {
				t.Fatal("smolvm exec did not inherit host variable", err)
			}
		})
	}
}

func TestFindMediatedRequiresVersionedProtocol(t *testing.T) {
	for _, version := range []string{"1.33.0", "1.35.3", "1.35.4", "1.36.0", "1.36.1", "2.0.0"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "smolvm"), []byte("#!/bin/sh\necho smolvm "+version+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir)
		_, err := FindMediated()
		want := version == "1.36.1" || version == "2.0.0"
		if (err == nil) != want {
			t.Fatal(version, err)
		}
	}
}

func TestLaunchMediationBindingIsHostOnlyAndRequired(t *testing.T) {
	t.Setenv("SMOLVM_INTERCEPTOR_TOKEN", "")
	dir := t.TempDir()
	binary := filepath.Join(dir, "smolvm")
	log := filepath.Join(dir, "calls")
	t.Setenv("VM_TEST_LOG", log)
	script := `#!/bin/sh
printf '%s\000' "$@" > "$VM_TEST_LOG.$2"
printf '%s' "${SMOLVM_INTERCEPTOR_TOKEN:-}" > "$VM_TEST_LOG.$2.token"
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"machine", "run", "--net", "--mediated-egress", "--from", "/image.smolmachine", "--", "agent"}
	var output bytes.Buffer
	if code := Launch(binary, args, IO{Err: &output}); code == 0 {
		t.Fatal("missing broker failed open")
	}
	if _, err := os.Stat(log + ".create"); !os.IsNotExist(err) {
		t.Fatal("VM created without broker")
	}
	token := strings.Repeat("ab", 32)
	if code := Launch(binary, args, IO{Err: &output, Mediation: &Mediation{Address: "127.0.0.1:12345", Token: token}}); code != 0 {
		t.Fatal(code, output.String())
	}
	for _, stage := range []string{"create", "start", "exec", "delete"} {
		data, err := os.ReadFile(log + "." + stage)
		if err != nil || bytes.Contains(data, []byte(token)) {
			t.Fatal(stage, err, "token leaked into argv")
		}
		if stage == "create" && (!bytes.Contains(data, []byte("agentnook.network=ask\x00")) || !bytes.Contains(data, []byte("agentnook.env=\x00"))) {
			t.Fatal("missing network or environment label")
		}
		env, _ := os.ReadFile(log + "." + stage + ".token")
		if stage == "start" {
			if string(env) != token || !bytes.Contains(data, []byte("--mediated-egress\x00--egress-interceptor\x00127.0.0.1:12345\x00")) {
				t.Fatal("missing start binding", string(data))
			}
		} else if len(env) != 0 || bytes.Contains(data, []byte("--mediated-egress")) {
			t.Fatal("binding leaked into other lifecycle stages", stage)
		}
	}
}

func TestLaunchRejectsPrivateExceptionsUnderMediation(t *testing.T) {
	args := []string{"machine", "run", "--net", "--block-private", "--mediated-egress", "--allow-private", "10.20.0.0/16", "--from", "/image.smolmachine", "--", "agent"}
	var output bytes.Buffer
	if code := Launch("/must-not-run", args, IO{Err: &output, Mediation: &Mediation{Address: "127.0.0.1:12345", Token: strings.Repeat("ab", 32)}}); code != 1 || !strings.Contains(output.String(), "bypass mediated approvals") {
		t.Fatal(code, output.String())
	}
}

func TestLaunchRejectsInvalidEnvironmentLabelInput(t *testing.T) {
	args := []string{"machine", "run", "--from", "/image.smolmachine", "--secret-env", "TOKEN,invalid=HOST_TOKEN", "--", "agent"}
	var output bytes.Buffer
	if code := Launch("/must-not-run", args, IO{Err: &output}); code != 1 || output.String() != "nook: invalid environment forward\n" {
		t.Fatal(code, output.String())
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
