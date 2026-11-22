package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

func TestRealPrivateNetworkVM(t *testing.T) {
	h := newNetworkVMHarness(t)
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "private-network-ok")
	}))
	defer origin.Close()
	port := strings.TrimPrefix(origin.URL, "http://127.0.0.1:")
	create := func(t *testing.T, h networkVMHarness, exceptions []string) {
		t.Helper()
		args := []string{"machine", "create", "--name", h.name, "--net", "--block-private", "--cpus", "1", "--mem", "256", "--storage", "1"}
		for _, cidr := range exceptions {
			args = append(args, "--allow-private", cidr)
		}
		if out, err := h.command(nil, args...); err != nil {
			t.Fatalf("create: %v: %s", err, out)
		}
	}
	request := func(h networkVMHarness, target string) (string, error) {
		return h.command(nil, "machine", "exec", "--name", h.name, "--", "sh", "-c", "wget -q -T 2 -O - http://"+target+":"+port)
	}

	// Private destinations are refused before the approval broker. An explicit
	// TCP approval must not be a way to re-open the host or LAN implicitly.
	broker, err := egress.Start(filepath.Join(h.root, "nook"), egress.Info{Project: "/private-test", Profile: "test", Agent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	create(t, h, nil)
	askHistoryRoot := t.TempDir()
	finishAsk := observeNetworkVM(t, h, askHistoryRoot, "ask")
	defer func() { _ = finishAsk(false) }()
	if out, err := h.command([]string{"SMOLVM_INTERCEPTOR_TOKEN=" + broker.Token()}, "machine", "start", "--name", h.name, "--mediated-egress", "--egress-interceptor", broker.Address()); err != nil {
		t.Fatalf("start mediated: %v: %s", err, out)
	}
	for _, target := range []string{"100.96.0.1", "[::ffff:100.96.0.1]"} {
		if out, err := request(h, target); err == nil || hits.Load() != 0 {
			t.Fatalf("private target %s reached host: %v: %s", target, err, out)
		}
	}
	if s, err := egress.Load(filepath.Join(h.root, "nook")); err != nil || len(s.Requests) != 0 {
		t.Fatal("private target reached approval queue", s, err)
	}
	if out, err := h.command(nil, "machine", "stop", "--name", h.name); err != nil {
		t.Fatalf("stop: %v: %s", err, out)
	}
	if err := finishAsk(true); err != nil {
		t.Fatal(err)
	}
	askHistory, err := egress.ReadHistory(askHistoryRoot, egress.HistoryQuery{})
	if err != nil || len(askHistory.Page.Events) == 0 {
		t.Fatal("pre-broker denial missing from history", askHistory, err)
	}

	for _, tc := range []struct {
		name       string
		exceptions []string
		allowed    bool
	}{
		{"full/default", nil, false},
		{"full/gateway-exception-must-not-open-host-loopback", []string{"100.96.0.1/32"}, false},
		{"full/explicit-host-exceptions", []string{"100.96.0.1/32", "127.0.0.1/32"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newNetworkVMHarness(t)
			create(t, h, tc.exceptions)
			historyRoot := t.TempDir()
			finish := observeNetworkVM(t, h, historyRoot, "full")
			defer func() { _ = finish(false) }()
			if out, err := h.command(nil, "machine", "start", "--name", h.name); err != nil {
				t.Fatalf("start: %v: %s", err, out)
			}
			before := hits.Load()
			out, err := request(h, "100.96.0.1")
			if tc.allowed {
				if err != nil || !strings.Contains(out, "private-network-ok") || hits.Load() != before+1 {
					t.Fatalf("explicit exception did not work: %v: %s", err, out)
				}
			} else if err == nil || hits.Load() != before {
				t.Fatalf("private target bypassed host protection: %v: %s", err, out)
			}
			if out, err := h.command(nil, "machine", "stop", "--name", h.name); err != nil {
				t.Fatalf("stop: %v: %s", err, out)
			}
			if err := finish(true); err != nil {
				t.Fatal(err)
			}
			view, err := egress.ReadHistory(historyRoot, egress.HistoryQuery{})
			if err != nil || view.Machines[0].Coverage != "denials-only" {
				t.Fatal(view, err)
			}
			if !tc.allowed && len(view.Page.Events) == 0 {
				t.Fatal("full-mode denial not archived")
			}
			for _, event := range view.Page.Events {
				if event.Decision != "denied" {
					t.Fatal("full mode fabricated allowed traffic", event)
				}
			}
		})
	}
}
