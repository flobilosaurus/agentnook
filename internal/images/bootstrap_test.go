package images

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/vm"
)

func TestLatestStableVersionResolution(t *testing.T) {
	for _, tc := range []struct {
		agent, body string
		status      int
		ok          bool
	}{{"pi", `{"version":"1.4.0"}`, 200, true}, {"claude", `{"version":"2.1.99"}`, 200, true}, {"opencode", `{"version":"2.3.2"}`, 200, true}, {"opencode", `{"version":"1.19.6"}`, 200, false}, {"pi", `{"version":"1.4.0-beta"}`, 200, false}, {"pi", `{"version":"latest"}`, 200, false}, {"pi", `{`, 200, false}, {"pi", `{}`, 200, false}, {"pi", `{"version":"1.4.0"}`, 503, false}} {
		t.Run(tc.agent+tc.body, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer s.Close()
			v, err := latestVersion(s.Client(), s.URL, testAgent(t, tc.agent))
			if (err == nil) != tc.ok {
				t.Fatal(v, err)
			}
		})
	}
}
func TestBootstrapOncePerAgentAndConcurrentStarts(t *testing.T) {
	log := smolvmStub(t)
	p, _ := buildFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for _, agent := range []string{"pi", "pi", "claude", "claude", "opencode", "opencode"} {
		wg.Add(1)
		go func(agent string) {
			defer wg.Done()
			version := "2.3.2"
			if agent == "pi" {
				version = "1.4.0"
			}
			_, err := Bootstrap(p, vm.IO{Out: io.Discard, Err: io.Discard}, testAgent(t, agent), version, func(string) (string, error) { panic("explicit version queried registry") })
			errs <- err
		}(agent)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := config.Load(p.ConfigFile())
	if err != nil || len(c.Images) != 3 || len(c.DefaultImages) != 3 {
		t.Fatal(c, err)
	}
	data, _ := os.ReadFile(log)
	if strings.Count(string(data), "pack\ncreate\n") != 3 {
		t.Fatal(string(data))
	}
	before, _ := os.ReadFile(p.ConfigFile())
	_, err = Bootstrap(p, vm.IO{}, testAgent(t, "pi"), "", func(string) (string, error) { panic("existing build queried registry") })
	after, _ := os.ReadFile(p.ConfigFile())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing catalog changed", err)
	}
}
