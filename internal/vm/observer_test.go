package vm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAuditLifecycleStopsThenFinalizesBeforeDelete(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		initFails, stopFails, finishFails bool
		want                              int
	}{
		{"success", false, false, false, 0},
		{"initialization", true, false, false, 1},
		{"stop", false, true, false, 1},
		{"finalization", false, false, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary, log := filepath.Join(dir, "smolvm"), filepath.Join(dir, "calls")
			t.Setenv("AUDIT_TEST_LOG", log)
			stopCode := "0"
			if tc.stopFails {
				stopCode = "7"
			}
			script := "#!/bin/sh\nprintf '%s\\n' \"$2\" >> \"$AUDIT_TEST_LOG\"\nif [ \"$2\" = stop ]; then exit " + stopCode + "; fi\n"
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			finished := false
			var output bytes.Buffer
			streams := IO{Err: &output, Observe: func(bin, name string) (func(bool) error, error) {
				calls, _ := os.ReadFile(log)
				if bin != binary || !strings.HasPrefix(name, "nook-run-") || string(calls) != "create\n" {
					t.Fatal(bin, name, string(calls))
				}
				if tc.initFails {
					return nil, errors.New("audit unavailable")
				}
				return func(stopped bool) error {
					calls, _ := os.ReadFile(log)
					if strings.Contains(string(calls), "delete") || !strings.HasSuffix(string(calls), "stop\n") || stopped == tc.stopFails {
						t.Fatal("audit drain order", stopped, string(calls))
					}
					finished = true
					if tc.finishFails || !stopped {
						return errors.New("incomplete audit")
					}
					return nil
				}, nil
			}}
			if code := Launch(binary, []string{"machine", "run", "--net", "--block-private", "--from", "/image.smolmachine", "--", "agent"}, streams); code != tc.want {
				t.Fatal(code, output.String())
			}
			calls, _ := os.ReadFile(log)
			want := []string{"create", "start", "exec", "stop", "delete"}
			if tc.initFails {
				want = []string{"create", "delete"}
			}
			if !reflect.DeepEqual(strings.Fields(string(calls)), want) || finished == tc.initFails {
				t.Fatal(string(calls), finished)
			}
		})
	}
}
