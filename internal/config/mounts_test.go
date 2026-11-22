package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/agents/registry"
)

func TestMountConfigRoundTripAndEmptySelection(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c := Empty()
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	w := testWorkspace("work", testOptions())
	p := w.Agents["pi"]
	p.Mounts = []Mount{{Host: "/host/shared", Guest: "/mnt/shared"}, {Host: "/host/docs", Guest: "/workspace/docs", ReadOnly: true}}
	w.Agents["pi"] = p
	c.Projects["/project"] = w
	c.Projects["/empty"] = testWorkspace("work", testOptions())
	if err := Save(file, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(file)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if got.Projects["/empty"].Agents["pi"].Mounts != nil {
		t.Fatal("another selection inherited extra mounts")
	}
}

func TestValidateMounts(t *testing.T) {
	for _, guest := range []string{"/mnt/shared", "/workspace/lib", "/run/data", "/opt/data"} {
		if err := ValidateMounts([]Mount{{Host: "/host/shared", Guest: guest}}, registry.All().ReservedPaths()...); err != nil {
			t.Fatalf("valid target %q: %v", guest, err)
		}
	}
	for _, guest := range []string{
		"", "relative", "/", "/workspace", "/workspace/..", "/mnt//data", "/mnt/data/",
		"/root", "/root/.pi", "/root/.pi/agent", "/root/.claude", "/root/.local/share/opencode", "/etc/opencode/plugins/agentnook", "/etc/opencode", "/run", "/run/agentnook-herdr",
		"/run/agentnook-herdr/status.json", "/var", "/var/run", "/var/run/agentnook-herdr/status.json",
		"/run/agentnook-telemetry", "/run/agentnook-telemetry/status.json", "/var/run/agentnook-telemetry/status.json",
		"/proc", "/proc/data", "/sys/data", "/dev/data", "/mnt/bad:ro", "/mnt/bad\n", "/mnt/bad\t", "/mnt/\x1b[31m",
	} {
		if err := ValidateMounts([]Mount{{Host: "/host/shared", Guest: guest}}, registry.All().ReservedPaths()...); err == nil {
			t.Errorf("accepted guest %q", guest)
		}
	}
	for _, host := range []string{"", "relative", "/host/../shared", "/host/data:ro", "/host/bad\n", "/host/\x1b[31m"} {
		if err := ValidateMounts([]Mount{{Host: host, Guest: "/mnt/shared"}}); err == nil {
			t.Errorf("accepted host %q", host)
		}
	}
	for _, pair := range [][2]string{
		{"/mnt/shared", "/mnt/shared"}, {"/mnt/shared", "/mnt/shared/sub"},
		{"/mnt/shared/sub", "/mnt/shared"}, {"/run/shared", "/var/run/shared"},
	} {
		mounts := []Mount{{Host: "/first", Guest: pair[0]}, {Host: "/second", Guest: pair[1]}}
		if err := ValidateMounts(mounts); err == nil {
			t.Errorf("accepted overlapping targets %v", pair)
		}
	}
	if err := ValidateMounts([]Mount{{Host: "/same", Guest: "/mnt/shared"}, {Host: "/same", Guest: "/mnt/shared-other", ReadOnly: true}}); err != nil {
		t.Fatal(err)
	}
}

func TestSaveAndLoadRejectInvalidMounts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	c := Empty()
	if err := c.AddProfile("work"); err != nil {
		t.Fatal(err)
	}
	w := testWorkspace("work", testOptions())
	p := w.Agents["pi"]
	p.Mounts = []Mount{{Host: "/host", Guest: "/root/.pi"}}
	w.Agents["pi"] = p
	c.Projects["/project"] = w
	if err := Save(file, c); err == nil {
		t.Fatal("invalid mount saved")
	}
	content := `version = 1
[[profiles]]
name = "work"
[projects."/project"]
agent = "pi"
[projects."/project".agents.pi]
profile = "work"
[projects."/project".agents.pi.vm]
image = "node:24-bookworm"
agent = "pi"
agent_version = "1.4.0"
cpus = 2
memory_mib = 2048
storage_gib = 20
[[projects."/project".agents.pi.mounts]]
host = "/host"
guest = "/root/.pi"
`
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("invalid persisted mount loaded")
	}
}
