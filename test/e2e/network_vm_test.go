package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/egress"
)

// This test needs no agent credentials or image pull. The bundled bare Linux
// guest reaches only a test HTTP server on the host through smolvm's gateway.
func TestRealNetworkVM(t *testing.T) {
	if os.Getenv("AGENTNOOK_VM_SMOKE") != "1" {
		t.Skip("set AGENTNOOK_VM_SMOKE=1 to test real mediated networking")
	}
	historyRoot := t.TempDir()
	t.Cleanup(func() {
		view, err := egress.ReadHistory(historyRoot, egress.HistoryQuery{})
		if err != nil || len(view.Machines) != 1 || view.Machines[0].Ended.IsZero() || view.Machines[0].Count < 5 {
			t.Error("history did not survive real VM deletion", view, err)
		}
	})
	h := newNetworkVMHarness(t)
	root, name, command := h.root, h.name, h.command
	var hits atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "nook-network-ok")
	}))
	defer origin.Close()
	port := strings.TrimPrefix(origin.URL, "http://127.0.0.1:")
	broker, err := egress.Start(filepath.Join(root, "nook"), egress.Info{Project: "/network-test", Profile: "test", Agent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if out, err := command(nil, "machine", "create", "--name", name, "--net", "--allow-cidr", "100.96.0.1/32", "--allow-cidr", "127.0.0.1/32", "--cpus", "1", "--mem", "256", "--storage", "1"); err != nil {
		t.Fatalf("create: %v: %s", err, out)
	}
	finishAudit := observeNetworkVM(t, h, historyRoot, "ask")
	defer func() { _ = finishAudit(false) }()
	if out, err := command([]string{"SMOLVM_INTERCEPTOR_TOKEN=" + broker.Token()}, "machine", "start", "--name", name, "--mediated-egress", "--egress-interceptor", broker.Address()); err != nil {
		t.Fatalf("start: %v: %s", err, out)
	}
	if out, err := command(nil, "machine", "exec", "--name", name, "--", "nslookup", "-type=A", "host.docker.internal"); err != nil {
		t.Fatalf("internal DNS audit probe: %v: %s", err, out)
	}
	type result struct {
		out string
		err error
	}
	request := func() <-chan result {
		ch := make(chan result, 1)
		go func() {
			out, err := command(nil, "machine", "exec", "--name", name, "--", "sh", "-c", "wget -q -T 8 -O - http://100.96.0.1:"+port)
			ch <- result{out, err}
		}()
		return ch
	}
	waitPending := func() egress.Entry {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			s, err := egress.Load(filepath.Join(root, "nook"))
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Requests) > 0 {
				return s.Requests[0]
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("no mediated connection reached broker")
		return egress.Entry{}
	}
	for _, choice := range []string{"deny", "once", "session"} {
		before := hits.Load()
		ch := request()
		entry := waitPending()
		if hits.Load() != before {
			t.Fatal("upstream contacted BEFORE approval")
		}
		if err := egress.Decide(entry, choice); err != nil {
			t.Fatal(err)
		}
		r := <-ch
		if choice == "deny" {
			if r.err == nil || hits.Load() != before {
				t.Fatalf("denied request reached origin: %v %s", r.err, r.out)
			}
		} else if r.err != nil || !strings.Contains(r.out, "nook-network-ok") || hits.Load() != before+1 {
			t.Fatalf("approved request failed: %v %s", r.err, r.out)
		}
	}
	// Session grant allows another connection without another user decision.
	if r := <-request(); r.err != nil || !strings.Contains(r.out, "nook-network-ok") {
		t.Fatalf("session grant: %v %s", r.err, r.out)
	}
	broker.Close()
	before := hits.Load()
	if r := <-request(); r.err == nil || hits.Load() != before {
		t.Fatalf("broker failure did not fail closed: %v %s", r.err, r.out)
	}
	if out, err := command(nil, "machine", "stop", "--name", name); err != nil {
		t.Fatalf("stop: %v: %s", err, out)
	}
	if err := finishAudit(true); err != nil {
		t.Fatal(err)
	}
	view, err := egress.ReadHistory(historyRoot, egress.HistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	allowed, denied, dns := 0, 0, 0
	for _, event := range view.Page.Events {
		if event.Protocol == "tcp" && event.Decision == "allowed" {
			allowed++
		}
		if event.Protocol == "tcp" && event.Decision == "denied" {
			denied++
		}
		if event.Protocol == "dns" && event.Decision == "allowed" {
			dns++
		}
	}
	if allowed < 3 || denied < 2 || dns < 1 {
		t.Fatal("missing real network decisions", view)
	}
}
