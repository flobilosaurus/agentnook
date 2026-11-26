package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealMiseVM(t *testing.T) {
	fixture, run := realVMLauncher(t)
	cfg := filepath.Join(fixture.workspace, ".mise.toml")
	// Starting the agent must not require trusting or resolving project config.
	if err := os.WriteFile(cfg, []byte("[tools]\nrg = \"13.0.0\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	buildConfig := filepath.Join(fixture.workspace, "image-tools.toml")
	if err := os.WriteFile(buildConfig, []byte(`[tools]
rg = "14.1.1"
"aqua:sharkdp/fd" = "10.3.0"

[hooks]
enter = "exit 91"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	run("images", "build", "pi-tools", "--agent", "pi", "--agent-version", "1.4.0",
		"--base-image", "node:24-bookworm-slim", "--storage", "8",
		"--mise-config", "./image-tools.toml")
	if err := os.Remove(buildConfig); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, selection := range [][]string{{"--agent", "pi", "--profile", "smoke", "--image", "pi-tools"}, nil} {
		args := append(selection, "--", "--offline", "--no-session")
		pi := startTestTerminal(t, fixture.command(ctx, args...))
		pi.expect(t, 90*time.Second, `Pi can explain its own features`)
		pi.send(t, "!rg --version && fd --version\r")
		pi.expect(t, 20*time.Second, `ripgrep 14\.1\.1`)
		pi.expect(t, 20*time.Second, `fd 10\.3\.0`)
		pi.send(t, "/quit\r")
		pi.wait(t)
	}
}
