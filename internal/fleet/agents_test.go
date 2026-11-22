package fleet

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestDiscoverAllAgentTypesFromIsolatedHomes(t *testing.T) {
	f := setupSource(t)
	cfg := config.Empty()
	var machines []machine
	verbose := "NAME STATE CPUS MEMORY MOUNTS\n"
	for _, agent := range []string{"pi", "opencode", "claude"} {
		o := config.Defaults()
		o.Agent = agent
		o.AgentVersion = "2.3.2"
		if agent == "pi" {
			o.AgentVersion = "1.4.0"
		}
		o.Image = filepath.Join(f.root, agent+".smolmachine")
		if err := cfg.AddImage(config.BuiltImage{Name: agent, BaseImage: config.BaseImage, Architecture: "arm64", BuiltAt: "2026-11-17T10:00:00Z", VM: o}); err != nil {
			t.Fatal(err)
		}
		machines = append(machines, machine{Name: agent, State: "running", CPUs: 2, MemoryMiB: 2048, MountCount: 3, Image: o.Image})
		verbose += strings.ReplaceAll(f.mounts(agent, f.project, f.source.Paths.Root), "/profiles/work/pi", "/profiles/work/"+agent)
	}
	if err := config.Save(f.source.Paths.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	f.store(t, machines, verbose)
	snapshot, err := f.source.Load(context.Background())
	if err != nil || len(snapshot.Instances) != 3 {
		t.Fatal(snapshot, err)
	}
	for _, item := range snapshot.Instances {
		if item.Agent != item.Name || item.Profile != "work" || item.AgentVersion == "" {
			t.Fatal(item)
		}
	}
}
