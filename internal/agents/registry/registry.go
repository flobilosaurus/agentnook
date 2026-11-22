// Package registry is the sole composition point for concrete agent adapters.
package registry

import (
	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/claude"
	"github.com/flobilosaurus/agentnook/internal/agents/opencode"
	"github.com/flobilosaurus/agentnook/internal/agents/pi"
)

// All returns a fresh catalog. No init registration or mutable global state.
func All() agents.Catalog { return agents.Catalog{pi.Adapter{}, opencode.Adapter{}, claude.Adapter{}} }
