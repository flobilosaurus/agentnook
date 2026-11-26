package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadMiseTools(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mise.toml")
	const input = `
[tools]
rg = "14.1.1"
"aqua:sharkdp/fd" = "10.3.0"
"npm:@scope/cli" = "latest"
node = ["24", "22"]

[tasks.test]
run = "exit 99"
[hooks]
enter = "exit 98"
[env]
PATH = "ignored"
`
	if err := os.WriteFile(file, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{"rg@14.1.1", "aqua:sharkdp/fd@10.3.0", "npm:@scope/cli@latest", "node@24", "node@22"}
	got, err := LoadMiseTools(file)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("LoadMiseTools = %q, %v; want %q", got, err, want)
	}
}

func TestLoadMiseToolsInvalid(t *testing.T) {
	for _, tc := range []struct{ input, message string }{
		{"[env]", "non-empty [tools]"},
		{"[tools]", "non-empty [tools]"},
		{`tools = "rg"`, "non-empty [tools]"},
		{`[tools`, "toml:"},
		{"[tools]\nrg = 14", `[tools] entry "rg"`},
		{"[tools]\nrg = []", `[tools] entry "rg"`},
		{"[tools]\nrg = [\"14\", 14]", `[tools] entry "rg"`},
		{"[tools]\nrg = \"\"", `[tools] entry "rg"`},
		{"[tools]\nrg = { version = \"14\" }", `[tools] entry "rg"`},
		{"[tools]\nrg = \"14\"\nfd.version = \"10\"", `[tools] entry "fd"`},
		{"[tools]\n\"--help\" = \"14\"", "invalid mise tool"},
		{"[tools]\nrg = \"14\\n15\"", "invalid mise tool"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "mise.toml")
			if err := os.WriteFile(file, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadMiseTools(file)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("LoadMiseTools = %q, %v; want error containing %q", got, err, tc.message)
			}
		})
	}
}
