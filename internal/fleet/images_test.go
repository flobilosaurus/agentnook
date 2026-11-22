package fleet

import (
	"context"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func TestPackedImageMetadataUsesExactIdentity(t *testing.T) {
	entry := config.BuiltImage{Name: "work", BaseImage: config.BaseImage, VM: config.Options{Image: "/images/work/image.smolmachine", Agent: "pi", AgentVersion: "1.4.0"}}
	for _, tc := range []struct {
		name, reference, agent string
		duplicate, known       bool
	}{
		{"exact", entry.VM.Image, "pi", false, true},
		{"base image is not identity", config.BaseImage, "pi", false, false},
		{"other artifact", "/images/new/image.smolmachine", "pi", false, false},
		{"other agent", entry.VM.Image, "claude", false, false},
		{"ambiguous", entry.VM.Image, "pi", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := []config.BuiltImage{entry}
			if tc.duplicate {
				entries = append(entries, entry)
			}
			instances := []Instance{{Image: tc.reference, Agent: tc.agent}}
			(&Source{}).imageMetadata(context.Background(), instances, entries)
			if (instances[0].ImageName != "") != tc.known {
				t.Fatal(instances)
			}
			if tc.known && (instances[0].AgentVersion != "1.4.0" || instances[0].BaseImage != config.BaseImage) {
				t.Fatal(instances)
			}
		})
	}
}

func TestPackedMachineLabelsParse(t *testing.T) {
	items, err := parseMachines([]byte(`[{"name":"nook-run-1","state":"running","cpus":2,"memory_mib":2048,"mounts":2,"labels":{"agentnook.image":"/images/work/image.smolmachine","agentnook.ephemeral":"true"}}]`))
	if err != nil || len(items) != 1 || items[0].Labels["agentnook.image"] != "/images/work/image.smolmachine" {
		t.Fatal(items, err)
	}
}
