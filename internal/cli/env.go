package cli

import (
	"fmt"
	"os"

	"github.com/flobilosaurus/agentnook/internal/config"
)

func checkedEnvNames(names []string) ([]string, error) {
	normalized, err := config.NormalizeEnvNames(names)
	if err != nil {
		return nil, fmt.Errorf("--env: %w", err)
	}
	for _, name := range normalized {
		if _, ok := os.LookupEnv(name); !ok {
			return nil, fmt.Errorf("--env: %s is not exported on the host", name)
		}
	}
	return normalized, nil
}

// envForwardArgs builds references, never copying values into the launch plan.
func envForwardArgs(names []string) []string {
	var args []string
	for _, name := range names {
		args = append(args, "--secret-env", name+"="+name)
	}
	return args
}
