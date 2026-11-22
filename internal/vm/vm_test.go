package vm

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/config"
)

func testAgent(t *testing.T, name string) agents.Agent {
	t.Helper()
	a, err := registry.All().Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAgentArgs(t *testing.T) {
	for _, agent := range []string{"pi", "opencode", "claude"} {
		for _, tty := range []bool{false, true} {
			o := config.Defaults()
			o.Agent = agent
			o.Image = "/images/" + agent + ".smolmachine"
			pass := []string{"--continue", "spaces ; $(echo nope)"}
			got := Args("/repo with spaces", "/data/profiles/work/"+agent, o, testAgent(t, agent), tty, pass)
			tail := append([]string{"--", agent}, pass...)
			if !reflect.DeepEqual(got[len(got)-len(tail):], tail) {
				t.Fatal(got)
			}
			joined := strings.Join(got, "\n")
			for _, value := range []string{"/repo with spaces:/workspace", "/data/profiles/work/" + agent + ":/root", "HOME=/root"} {
				if !strings.Contains(joined, value) {
					t.Fatal(got)
				}
			}
			if strings.Contains(joined, "--init") || strings.Contains(joined, "--tty") != tty {
				t.Fatal(got)
			}
			if agent != "pi" && strings.Contains(joined, "PI_CODING_AGENT_DIR") {
				t.Fatal("Pi env leaked", got)
			}
			if agent == "claude" && !strings.Contains(joined, "DISABLE_AUTOUPDATER=1") {
				t.Fatal("Claude updates not pinned")
			}
		}
	}
}

func TestRunIOAndExitStatus(t *testing.T) {
	script := filepath.Join(t.TempDir(), "smolvm")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nread line\nprintf 'out:%s:%s' \"$1\" \"$line\"\nprintf 'err' >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Run(script, []string{"argument with spaces"}, IO{In: strings.NewReader("input\n"), Out: &out, Err: &errOut})
	if code != 7 || out.String() != "out:argument with spaces:input" || errOut.String() != "err" {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	if code := Run("/missing/smolvm", nil, IO{Out: &out, Err: &errOut}); code != 1 {
		t.Fatalf("missing executable exit %d", code)
	}
}

func TestRunSuccessAndSignalExit(t *testing.T) {
	for _, tc := range []struct {
		script string
		code   int
	}{
		{"exit 0", 0},
		{"kill -TERM $$", 143},
	} {
		script := filepath.Join(t.TempDir(), "smolvm")
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if code := Run(script, nil, IO{Out: &out, Err: &out}); code != tc.code {
			t.Fatalf("%q: code %d, want %d", tc.script, code, tc.code)
		}
	}
}
