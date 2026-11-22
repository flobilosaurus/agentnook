package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

func testAgent(t *testing.T, name string) agents.Agent {
	t.Helper()
	a, err := registry.All().Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func prepare(t *testing.T) *Launch {
	t.Helper()
	l, err := Prepare(testAgent(t, "pi"), true, nil)
	if err != nil || l == nil {
		t.Fatalf("Prepare: %v %v", l, err)
	}
	t.Cleanup(l.Close)
	return l
}

func encoded(t *testing.T, id string, seq uint64, state string) []byte {
	t.Helper()
	data, err := json.Marshal(Report{Version: 2, RunID: id, Seq: seq, SessionID: "test-session", State: state, UptimeSeconds: 42})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func store(t *testing.T, l *Launch, seq uint64, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.dir, "status.json"), encoded(t, l.runID, seq, state), 0o600); err != nil {
		t.Fatal(err)
	}
}

func reader(t *testing.T, l *Launch) *Reader {
	t.Helper()
	r, err := Open(l.Dir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestActivationIndependentOfHerdr(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	t.Setenv("AGENTNOOK_HERDR", "0")
	prepare(t)
	for _, args := range [][]string{{"-p", "prompt"}, {"--print"}, {"--mode", "rpc"}, {"--mode=json"}, {"--no-extensions"}, {"--help"}, {"-v"}, {"--version"}, {"--list-models=test"}} {
		if l, err := Prepare(testAgent(t, "pi"), true, args); l != nil || err != nil {
			t.Fatalf("activated for %q: %v", args, err)
		}
	}
	if l, err := Prepare(testAgent(t, "pi"), false, nil); l != nil || err != nil {
		t.Fatal("activated without TTY")
	}
}

func TestLaunchIsolationAndCleanup(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "must-not-forward")
	b, other := prepare(t), prepare(t)
	if b.dir == other.dir || b.runID == other.runID {
		t.Fatal("launches share telemetry")
	}
	info, err := os.Stat(b.dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(b.dir, "status.ts"))
	if err != nil || string(data) != string(testAgent(t, "pi").Telemetry(true, nil).Files["status.ts"]) {
		t.Fatal("embedded extension missing")
	}
	args := []string{"machine", "run", "--tty", "--", "pi", "--continue", "prompt ; $(false)"}
	original := append([]string(nil), args...)
	got := b.Args(args)
	if !reflect.DeepEqual(args, original) || !reflect.DeepEqual(got[len(got)-2:], args[len(args)-2:]) {
		t.Fatal("original arguments mutated")
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, b.dir+":"+GuestDir) || !strings.Contains(joined, "--extension "+GuestDir+"/status.ts") || !strings.Contains(joined, "AGENTNOOK_REPORT_ID="+b.runID) {
		t.Fatal(got)
	}
	if strings.Contains(joined, "HERDR_") || strings.Contains(joined, "/host/herdr.sock") || strings.Contains(joined, "must-not-forward") {
		t.Fatal("host authority forwarded to guest")
	}
	b.Close()
	b.Close()
	if _, err := os.Stat(b.dir); !os.IsNotExist(err) {
		t.Fatal("telemetry not removed")
	}
	if _, err := os.Stat(other.dir); err != nil {
		t.Fatal("other launch removed")
	}
}

func TestLaunchesKeepInitCacheInputsStable(t *testing.T) {
	first, second := prepare(t), prepare(t)
	base := vm.Args("/project", "/profile/.pi", config.Defaults(), testAgent(t, "pi"), true, []string{"--continue"})
	// smolvm 1.33 hashes image, resolved image digest, init steps and all VM
	// --env values. Mounts and workload argv are not part of its init key.
	cacheInputs := func(args []string) []string {
		var inputs []string
		for i := 0; i < len(args) && args[i] != "--"; i++ {
			switch args[i] {
			case "--image", "--init", "--env":
				inputs = append(inputs, args[i], args[i+1])
				i++
			}
		}
		return inputs
	}
	want := cacheInputs(base)
	for _, b := range []*Launch{first, second} {
		args := b.Args(base)
		if got := cacheInputs(args); !reflect.DeepEqual(got, want) {
			t.Fatalf("Telemetry changed init-cache inputs:\ngot %q\nwant %q", got, want)
		}
		separator := -1
		for i, arg := range args {
			if arg == "--" {
				separator = i
				break
			}
		}
		workload := args[separator+1:]
		expected := []string{"env", "AGENTNOOK_REPORT_ID=" + b.runID, "pi", "--extension", GuestDir + "/status.ts", "--continue"}
		if !reflect.DeepEqual(workload, expected) {
			t.Fatalf("runtime environment: %q", workload)
		}
	}
}

func TestParseReport(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, state := range []string{"idle", "working", "blocked"} {
		if _, ok := parseReport(encoded(t, id, 1, state), id); !ok {
			t.Fatal(state)
		}
	}
	base := string(encoded(t, id, 1, "idle"))
	bad := []string{"", "[]", "null", "\xff", strings.Repeat("{", 2000), strings.Repeat("x", maxReport+1), base + "{}",
		strings.Replace(base, `"version":2`, `"version":true`, 1),
		strings.Replace(base, `"version":2`, `"version":2,"version":2`, 1),
		strings.Replace(base, `"version":2`, `"version":1`, 1),
		strings.Replace(base, `"uptimeSeconds":42`, `"uptimeSeconds":null`, 1),
		strings.Replace(base, `"uptimeSeconds":42`, `"uptimeSeconds":-1`, 1),
		strings.Replace(base, `"uptimeSeconds":42`, `"uptimeSeconds":1.5`, 1),
		strings.Replace(base, `"uptimeSeconds":42`, `"uptimeSeconds":9223372037`, 1),
		strings.Replace(base, `,"uptimeSeconds":42`, ``, 1),
		strings.Replace(base, `"seq":1`, `"seq":true`, 1),
		strings.Replace(base, `"seq":1`, `"seq":0`, 1),
		strings.Replace(base, `"seq":1`, `"seq":null`, 1),
		strings.Replace(base, `"seq":1`, `"seq":1.2`, 1),
		strings.Replace(base, `"seq":1`, `"seq":9007199254740992`, 1),
		strings.Replace(base, `"seq":1`, `"seq":-1`, 1),
		strings.Replace(base, `"state":"idle"`, `"state":[]`, 1),
		strings.Replace(base, `"state":"idle"`, `"state":"unknown"`, 1),
		strings.Replace(base, `"sessionId":"test-session"`, `"sessionId":"--help"`, 1),
		strings.Replace(base, `"sessionId":"test-session"`, `"sessionId":"x\n\u001b[2J"`, 1),
		strings.Replace(base, `"sessionId":"test-session"`, `"sessionId":"$(touch /tmp/nope)"`, 1),
		strings.Replace(base, `"sessionId":"test-session"`, `"sessionId":"`+strings.Repeat("x", 257)+`"`, 1),
		strings.Replace(base, `"runId"`, `"pane"`, 1),
		strings.Replace(base, id, "wrong-run", 1),
		strings.Replace(base, `"version":2`, `"version":2,"pane":"other:p2"`, 1),
	}
	for _, data := range bad {
		if _, ok := parseReport([]byte(data), id); ok {
			t.Errorf("accepted %.120q", data)
		}
	}
}

func TestReadRejectsSymlinksDirectoriesAndFIFOs(t *testing.T) {
	b := prepare(t)
	r := reader(t, b)
	path := filepath.Join(b.dir, "status.json")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, encoded(t, b.runID, 1, "working"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ReadReport(); ok {
		t.Fatal("followed symlink")
	}
	os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ReadReport(); ok {
		t.Fatal("accepted directory")
	}
	os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ReadReport(); ok {
		t.Fatal("accepted FIFO")
	}
	os.Remove(path)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxReport+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.ReadReport(); ok {
		t.Fatal("accepted oversized file")
	}
}

func TestHeartbeatProgressStalenessAndIndependentReaders(t *testing.T) {
	l := prepare(t)
	r, other := reader(t, l), reader(t, l)
	now := time.Now()
	if sample := r.Poll(now); sample.Fresh {
		t.Fatal("missing report fresh")
	}
	store(t, l, 1, "working")
	if sample := r.Poll(now); sample.Fresh || sample.State() != "unknown" {
		t.Fatal("baseline fresh", sample)
	}
	store(t, l, 2, "working")
	sample := r.Poll(now.Add(time.Second))
	if !sample.Fresh || !sample.Advanced || sample.State() != "working" || sample.Report.UptimeSeconds != 42 {
		t.Fatal(sample)
	}
	if sample := other.Poll(now.Add(time.Second)); sample.Fresh {
		t.Fatal("new consumer trusts old report")
	}
	if sample := r.Poll(now.Add(3 * time.Second)); !sample.Fresh || sample.Advanced {
		t.Fatal(sample)
	}
	if sample := r.Poll(now.Add(6 * time.Second)); sample.Fresh || sample.State() != "unknown" {
		t.Fatal("replay fresh", sample)
	}
	store(t, l, 1, "idle")
	if sample := r.Poll(now.Add(7 * time.Second)); sample.Fresh {
		t.Fatal("regressed sequence fresh")
	}
	store(t, l, 3, "blocked")
	for _, observer := range []*Reader{r, other} {
		if sample := observer.Poll(now.Add(8 * time.Second)); !sample.Fresh || sample.State() != "blocked" {
			t.Fatal("did not recover", sample)
		}
	}
	// A restarted Pi process may have lower uptime but must still advance seq.
	data := strings.Replace(string(encoded(t, l.runID, 4, "idle")), `"uptimeSeconds":42`, `"uptimeSeconds":0`, 1)
	if err := os.WriteFile(filepath.Join(l.dir, "status.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if sample := r.Poll(now.Add(9 * time.Second)); !sample.Fresh || sample.Report.UptimeSeconds != 0 {
		t.Fatal("process restart did not reset uptime", sample)
	}
}

func TestOpenRejectsInvalidIdentityAndSymlink(t *testing.T) {
	l := prepare(t)
	alias := filepath.Join(t.TempDir(), directoryPrefix+l.runID)
	if err := os.Symlink(l.Dir(), alias); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{alias, t.TempDir(), "relative", filepath.Join(t.TempDir(), directoryPrefix+"bad")} {
		if r, err := Open(dir); err == nil {
			r.Close()
			t.Fatalf("opened %q", dir)
		}
	}
}
