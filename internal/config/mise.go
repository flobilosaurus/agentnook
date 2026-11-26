package config

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// ValidateToolSpec leaves backend and version syntax to mise.
func ValidateToolSpec(spec string) error {
	if spec == "" || strings.TrimSpace(spec) != spec || strings.HasPrefix(spec, "-") || strings.IndexFunc(spec, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid mise tool %q: expected TOOL or TOOL@VERSION", spec)
	}
	return nil
}

// LoadMiseTools translates only [tools] into mise CLI arguments. The original
// file (including tasks, hooks, and environment settings) never enters the VM.
func LoadMiseTools(path string) ([]string, error) {
	var document map[string]any
	meta, err := toml.DecodeFile(path, &document)
	if err != nil {
		return nil, err
	}
	entries, ok := document["tools"].(map[string]any)
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("expected a non-empty [tools] table")
	}
	var tools []string
	// Preserve declaration order, especially for tools providing the same command.
	for _, key := range meta.Keys() {
		if len(key) < 2 || key[0] != "tools" {
			continue
		}
		name := key[1]
		if err := ValidateToolSpec(name); err != nil {
			return nil, err
		}
		var versions []any
		switch value := entries[name].(type) {
		case string:
			versions = []any{value}
		case []any:
			versions = value
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("[tools] entry %q: expected a version string or non-empty array of version strings", name)
		}
		for _, value := range versions {
			version, ok := value.(string)
			if !ok || version == "" || strings.TrimSpace(version) != version {
				return nil, fmt.Errorf("[tools] entry %q: expected a non-empty version string without surrounding whitespace", name)
			}
			spec := name + "@" + version
			if err := ValidateToolSpec(spec); err != nil {
				return nil, err
			}
			tools = append(tools, spec)
		}
	}
	return tools, nil
}
