package config

import (
	"errors"
	"regexp"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NormalizeEnvNames validates names without consulting the host environment.
// Never include the rejected input in errors: it might be a secret value.
func NormalizeEnvNames(names []string) ([]string, error) {
	var normalized []string
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !envName.MatchString(name) {
			return nil, errors.New("expected variable names, not values or assignments")
		}
		if !seen[name] {
			normalized = append(normalized, name)
			seen[name] = true
		}
	}
	return normalized, nil
}
