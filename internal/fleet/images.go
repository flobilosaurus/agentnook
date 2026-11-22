package fleet

import (
	"context"

	"github.com/flobilosaurus/agentnook/internal/config"
)

// Packed machines retain the base image in smolvm's image field. Launch attaches
// the exact artifact path as a label; never infer identity from mutable defaults.
func (s *Source) imageMetadata(ctx context.Context, instances []Instance, entries []config.BuiltImage) {
	matches := make(map[string][]config.BuiltImage)
	for _, entry := range entries {
		matches[entry.VM.Image] = append(matches[entry.VM.Image], entry)
	}
	for i := range instances {
		if ctx.Err() != nil {
			return
		}
		item := &instances[i]
		candidates := matches[item.Image]
		if len(candidates) == 1 && candidates[0].VM.Agent == item.Agent {
			image := candidates[0]
			item.ImageName, item.BaseImage, item.AgentVersion = image.Name, image.BaseImage, image.VM.AgentVersion
		}
	}
}
