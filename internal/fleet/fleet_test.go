package fleet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

func testAgent(t *testing.T, name string) agents.Agent {
	t.Helper()
	a, err := registry.All().Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseMachines(t *testing.T) {
	// Schema verified against smolvm v1.33.0 machine_status_json, also used by ls.
	data := `[{"name":"run-123","state":"running","cpus":2,"memory_mib":2048,"pid":123,"mounts":2,"created_at":1731920000,"storage_gb":null,"image":"/images/dev.smolmachine","ephemeral":true,"future_field":{}}]`
	got, err := parseMachines([]byte(data))
	if err != nil || len(got) != 1 || got[0].MemoryMiB != 2048 || got[0].StorageGiB != nil || !got[0].Ephemeral {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{
		"", "null", "{}", data + "[]", strings.Replace(data, `"mounts":2`, `"mounts":[]`, 1),
		strings.Replace(data, `"cpus":2`, `"cpus":0`, 1), strings.Replace(data, `"memory_mib":2048`, `"memory_mib":-1`, 1),
		strings.Replace(data, `"pid":123`, `"pid":0`, 1), strings.Replace(data, `"storage_gb":null`, `"storage_gb":-1`, 1),
		strings.Replace(data, "run-123", `run\u001b[2J`, 1), strings.Replace(data, "run-123", "bad name", 1),
		strings.TrimSuffix(data, "]") + "," + strings.TrimPrefix(data, "["),
	} {
		if _, err := parseMachines([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, state := range []string{"running", "unreachable", "frozen", "stopped", "created", "failed"} {
		got, err := parseMachines([]byte(strings.Replace(data, "running", state, 1)))
		want := 0
		if state == "running" || state == "unreachable" || state == "frozen" {
			want = 1
		}
		if err != nil || len(got) != want {
			t.Fatalf("state %s: %+v %v", state, got, err)
		}
	}
}

func TestVerboseMounts(t *testing.T) {
	data := `NAME       STATE          CPUS MEMORY MOUNTS
run-1      running (eph)     2  2048 MiB  3
  PID: 123
  Mount: /project with spaces -> /workspace
  Mount: /profiles/work/pi -> /root
  Mount: /shared docs -> /mnt/shared docs (ro)
  Env: TOKEN=must-not-display
  Created: 2024-11-18T10:00:00Z

run-2      running (eph)     4  4096 MiB  1
  Mount: /other -> /workspace
`
	got, err := parseMounts([]byte(data), []machine{{Name: "run-1"}, {Name: "run-2"}})
	if err != nil || len(got["run-1"]) != 3 || got["run-1"][0].Host != "/project with spaces" || !got["run-1"][2].ReadOnly || got["run-1"][2].Guest != "/mnt/shared docs" || len(got["run-2"]) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{
		"", "unknown output format", "not-json but no error",
		strings.Replace(data, "/shared docs", "/shared -> /docs", 1),
		strings.Replace(data, "/project with spaces", "relative", 1),
		strings.Replace(data, "/workspace", "/work/../workspace", 1),
		strings.Replace(data, "/workspace", "/work\x1b[2J", 1),
		data + "run-1 running\n",
	} {
		if _, err := parseMounts([]byte(bad), []machine{{Name: "run-1"}}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

type sourceFixture struct {
	source                       *Source
	root, project, json, verbose string
}

func setupSource(t *testing.T) sourceFixture {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	f := sourceFixture{root: root, project: filepath.Join(root, "project with spaces"), json: filepath.Join(root, "list.json"), verbose: filepath.Join(root, "verbose.txt")}
	f.source = &Source{Agents: registry.All(), Binary: filepath.Join(root, "smolvm"), Paths: paths.Paths{Root: filepath.Join(root, "data")}}
	script := "#!/bin/sh\ncase \"$*\" in\n 'machine ls --json') /bin/cat '" + f.json + "';;\n 'machine ls --verbose') /bin/cat '" + f.verbose + "';;\n *) exit 99;;\nesac\n"
	if err := os.WriteFile(f.source.Binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.source.Close)
	return f
}

func (f sourceFixture) store(t *testing.T, machines []machine, verbose string) {
	t.Helper()
	data, err := json.Marshal(machines)
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{f.json: data, f.verbose: []byte(verbose)} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunningMachineNames(t *testing.T) {
	f := setupSource(t)
	t.Setenv("PATH", f.root)
	f.store(t, []machine{
		{Name: "nook-run-live", State: "running", CPUs: 2, MemoryMiB: 2048},
		{Name: "nook-run-frozen", State: "frozen", CPUs: 2, MemoryMiB: 2048},
		{Name: "nook-run-unreachable", State: "unreachable", CPUs: 2, MemoryMiB: 2048},
		{Name: "nook-run-stopped", State: "stopped"},
	}, "")
	// A runtime check only needs the JSON listing.
	if err := os.Remove(f.verbose); err != nil {
		t.Fatal(err)
	}
	running, err := RunningMachineNames(context.Background())
	if err != nil || len(running) != 1 || !running["nook-run-live"] {
		t.Fatal(running, err)
	}
	f.store(t, []machine{}, "")
	running, err = RunningMachineNames(context.Background())
	if err != nil || running == nil || len(running) != 0 {
		t.Fatal(running, err)
	}
	t.Setenv("PATH", "")
	running, err = RunningMachineNames(context.Background())
	if err == nil || running != nil {
		t.Fatal(running, err)
	}
}

func (f sourceFixture) mounts(name, project, home string) string {
	return name + " running (eph) 2 2048 MiB 3\n  Mount: " + project + " -> /workspace\n  Mount: " + home + "/profiles/work/pi -> /root\n  Mount: /docs -> /mnt/docs (ro)\n  Env: TOKEN=secret\n"
}

func TestSourceAllWorkspacesHomeAndCatalogMetadata(t *testing.T) {
	f := setupSource(t)
	machines := []machine{}
	verbose := "NAME STATE CPUS MEMORY MOUNTS\n"
	for _, name := range []string{"run-2", "run-1", "child", "parent", "foreign", "ordinary"} {
		machines = append(machines, machine{Name: name, State: "running", CPUs: 2, MemoryMiB: 2048, MountCount: 3, CreatedAt: 1731920000, Image: config.BaseImage, Labels: map[string]string{"agentnook.image": filepath.Join(f.root, "dev.smolmachine"), "agentnook.ephemeral": "true"}})
		project, home := f.project, f.source.Paths.Root
		switch name {
		case "child":
			project += "/child"
		case "parent":
			project = filepath.Dir(project)
		case "foreign":
			home += "-other"
		case "ordinary":
			home = "/not-agentnook"
			machines[len(machines)-1].MountCount = 4 // Unrelated staged mounts must not break discovery.
		}
		verbose += f.mounts(name, project, home)
	}
	f.store(t, machines, verbose)
	cfg := config.Empty()
	options := config.Defaults()
	options.Agent = "pi"
	options.AgentVersion = "1.4.0"
	options.Image = machines[0].Labels["agentnook.image"]
	options.CPUs, options.Memory = 8, 8192 // Image presets must NOT override observed limits.
	if err := cfg.AddImage(config.BuiltImage{Name: "dev", BaseImage: "node:24-bookworm", Architecture: "arm64", BuiltAt: "2024-11-18T10:00:00Z", VM: options}); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(f.source.Paths.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	got, err := f.source.Load(context.Background())
	if err != nil || len(got.Instances) != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	item := got.Instances[2] // child, parent, run-1, run-2; other data homes excluded.
	if item.Name != "run-1" || !item.Ephemeral || item.Image != options.Image || item.Profile != "work" || item.ImageName != "dev" || item.AgentVersion != "1.4.0" || item.BaseImage != "node:24-bookworm" || item.CPUs != 2 || item.MemoryMiB != 2048 || item.StorageGiB != nil || len(item.Mounts) != 3 || !item.Mounts[2].ReadOnly {
		t.Fatalf("wrong snapshot: %+v", item)
	}
}

func TestSourceReadOnlyUnknownMetadataErrorsAndRaces(t *testing.T) {
	f := setupSource(t)
	item := machine{Name: "run", State: "running", CPUs: 2, MemoryMiB: 2048, MountCount: 3}
	f.store(t, []machine{item}, f.mounts("run", f.project, f.source.Paths.Root))
	got, err := f.source.Load(context.Background())
	if err != nil || len(got.Instances) != 1 || got.Instances[0].AgentVersion != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := os.Stat(f.source.Paths.Root); !os.IsNotExist(err) {
		t.Fatal("discovery created data home", err)
	}
	if err := os.Mkdir(f.source.Paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.source.Paths.ConfigFile(), []byte("invalid ["), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = f.source.Load(context.Background())
	if err != nil || len(got.Instances) != 1 || got.Warning == "" {
		t.Fatal("catalog failure hid live machine", got, err)
	}
	f.store(t, []machine{item}, "No machines found\n")
	got, err = f.source.Load(context.Background())
	if err != nil || len(got.Instances) != 0 {
		t.Fatal("exit race", got, err)
	}
	item.MountCount = 4
	f.store(t, []machine{item}, f.mounts("run", f.project, f.source.Paths.Root))
	if _, err := f.source.Load(context.Background()); err == nil {
		t.Fatal("mount mismatch accepted")
	}
	if err := os.WriteFile(f.json, []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.Load(context.Background()); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestSharedTelemetryDiscoveryIsolationStalenessAndCleanup(t *testing.T) {
	f := setupSource(t)
	first, err := telemetry.Prepare(testAgent(t, "pi"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := telemetry.Prepare(testAgent(t, "pi"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	write := func(l *telemetry.Launch, seq uint64, state string) {
		t.Helper()
		data, err := json.Marshal(telemetry.Report{Version: 2, RunID: l.RunID(), Seq: seq, SessionID: "session", State: state, UptimeSeconds: 754})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(l.Dir(), "status.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	machines := []machine{}
	verbose := "NAME STATE CPUS MEMORY MOUNTS\n"
	for i, name := range []string{"run-a", "run-b", "legacy"} {
		count := 3
		if i < 2 {
			count++
		}
		machines = append(machines, machine{Name: name, State: "running", CPUs: 2, MemoryMiB: 2048, MountCount: count})
		verbose += f.mounts(name, f.project, f.source.Paths.Root)
		if i < 2 {
			l := []*telemetry.Launch{first, second}[i]
			verbose += "  Mount: " + l.Dir() + " -> " + telemetry.GuestDir + "\n"
		}
	}
	f.store(t, machines, verbose)
	write(first, 1, "working")
	write(second, 1, "idle")
	load := func() Snapshot {
		t.Helper()
		snapshot, err := f.source.Load(context.Background())
		if err != nil || len(snapshot.Instances) != 3 {
			t.Fatalf("%+v %v", snapshot, err)
		}
		return snapshot
	}
	byName := func(snapshot Snapshot, name string) Instance {
		t.Helper()
		for _, item := range snapshot.Instances {
			if item.Name == name {
				return item
			}
		}
		t.Fatal("missing instance", name)
		return Instance{}
	}
	for _, item := range load().Instances {
		if item.Activity.Fresh {
			t.Fatal("first observation fresh", item)
		}
	}
	write(first, 2, "working")
	write(second, 2, "idle")
	snapshot := load()
	if byName(snapshot, "run-a").Activity.State() != "working" || byName(snapshot, "run-b").Activity.State() != "idle" || byName(snapshot, "legacy").Activity.State() != "unknown" {
		t.Fatal(snapshot)
	}
	if byName(snapshot, "run-a").Activity.Report.UptimeSeconds != 754 {
		t.Fatal("missing uptime")
	}
	// Force an observed heartbeat into the past without sleeping in the test.
	write(first, 3, "working")
	key := "run-a" + "\x00" + first.Dir()
	f.source.readers[key].Poll(time.Now().Add(-telemetry.StaleAfter - time.Second))
	snapshot = load()
	if byName(snapshot, "run-a").Activity.Fresh || byName(snapshot, "run-b").Activity.State() != "idle" {
		t.Fatal("staleness crossed instances", snapshot)
	}
	write(first, 4, "blocked")
	if byName(load(), "run-a").Activity.State() != "blocked" {
		t.Fatal("did not recover")
	}
	// A report from a different run must never be attributed to this machine.
	data, _ := os.ReadFile(filepath.Join(second.Dir(), "status.json"))
	if err := os.WriteFile(filepath.Join(first.Dir(), "status.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if byName(load(), "run-a").Activity.Fresh {
		t.Fatal("foreign report accepted")
	}
	f.store(t, machines[2:], "NAME STATE CPUS MEMORY MOUNTS\n"+f.mounts("legacy", f.project, f.source.Paths.Root))
	if _, err := f.source.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.source.readers) != 0 {
		t.Fatal("removed instance readers retained")
	}
	f.source.Close()
	if _, err := os.Stat(first.Dir()); err != nil {
		t.Fatal("watch removed telemetry", err)
	}
}

func TestCommandBoundedAndCancellable(t *testing.T) {
	var buffer limitedBuffer
	if _, err := buffer.Write(make([]byte, maxOutput+1)); err == nil {
		t.Fatal("output not bounded")
	}
	f := setupSource(t)
	if err := os.WriteFile(f.source.Binary, []byte("#!/bin/sh\n/bin/sleep 100\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := f.source.Load(ctx); err == nil || !strings.Contains(err.Error(), "deadline exceeded") || time.Since(started) > 2*time.Second {
		t.Fatalf("unbounded command: %v", err)
	}
}
