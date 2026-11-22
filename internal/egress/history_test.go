package egress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func auditLine(action, protocol, target string) string {
	p, _ := json.Marshal(map[string]any{"timestampMs": int64(1760000000000), "machineId": strings.Repeat("ab", 16),
		"transport": protocol, "destination": target, "action": action, "reason": "local_policy",
		"access_token": "secret-must-not-be-copied", "initial_bytes": "payload-must-not-be-copied"})
	return string(p) + "\n"
}

func TestHistoryPaginationSurvivesAppendsAndMachineEnd(t *testing.T) {
	root := t.TempDir()
	w, err := createHistory(root, "nook-run-history-test", "ask", Info{Project: "/project", Agent: "agent", Profile: "work"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.file.Close()
	for i := 0; i < 123; i++ {
		if err := w.append(HistoryEvent{Time: time.Now(), Protocol: "tcp", Target: "203.0.113.1:443", Decision: "allowed", Reason: "broker_decision"}); err != nil {
			t.Fatal(err)
		}
	}
	w.meta.Ended = time.Now()
	if err := w.flush(); err != nil {
		t.Fatal(err)
	}
	query := HistoryQuery{Machine: w.meta.Machine, Limit: 7}
	seen := []uint64{}
	for {
		view, err := ReadHistory(root, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Machines) != 1 || view.Machines[0].Ended.IsZero() {
			t.Fatal(view)
		}
		for _, event := range view.Page.Events {
			seen = append(seen, event.Sequence)
		}
		if !view.Page.HasOlder {
			break
		}
		query.Before = view.Page.NextBefore
		if len(seen) == 7 {
			// New traffic cannot move the byte cursor of an older page.
			if err := w.append(HistoryEvent{Time: time.Now(), Protocol: "tcp", Target: "203.0.113.2:443", Decision: "denied", Reason: "local_policy"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(seen) != 123 {
		t.Fatal("lost events between pages", len(seen))
	}
	for i, sequence := range seen {
		if sequence != uint64(123-i) {
			t.Fatal("page repeated or skipped event", i, sequence)
		}
	}
}

func TestCollectorPersistsDecisionsWithoutSecretsOrPayloads(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	finish, err := ObserveDirectory(root, "nook-run-collector-test", "ask", Info{Project: "/project"}, source)
	if err != nil {
		t.Fatal(err)
	}
	p := auditLine("allow", "tcp", "203.0.113.1:443") + auditLine("deny", "udp", "to 203.0.113.2:123") + auditLine("allow", "dns", "example.com") + auditLine("deny", "icmp", "203.0.113.3")
	if err := os.WriteFile(filepath.Join(source, "egress-decisions.jsonl"), []byte(p), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil { // Finalization is idempotent.
		t.Fatal(err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	view, err := ReadHistory(root, HistoryQuery{})
	if err != nil || len(view.Page.Events) != 4 || view.Machines[0].Coverage != "engine-decisions" {
		t.Fatal(view, err)
	}
	var decisions []string
	for _, event := range view.Page.Events {
		decisions = append(decisions, event.Decision)
	}
	if !reflect.DeepEqual(decisions, []string{"denied", "allowed", "denied", "allowed"}) {
		t.Fatal(decisions)
	}
	stored, err := os.ReadFile(filepath.Join(root, "network", "history", "nook-run-collector-test", "events.jsonl"))
	if err != nil || strings.Contains(string(stored), "must-not-be-copied") {
		t.Fatal("raw payload or secret copied", err)
	}
}

func TestFullHistoryNeverInventsAllowedTraffic(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	finish, err := ObserveDirectory(root, "nook-run-full-test", "full", Info{}, source)
	if err != nil {
		t.Fatal(err)
	}
	p := "[2026-08-13T02:31:05Z]: egress policy denied connect to 100.96.0.1:1455\n" +
		"[2026-08-13T02:31:05Z]: egress policy denied resolve private.example\n"
	if err := os.WriteFile(filepath.Join(source, "egress-denials.log"), []byte(p), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	view, err := ReadHistory(root, HistoryQuery{})
	if err != nil || len(view.Page.Events) != 2 || view.Machines[0].Coverage != "denials-only" {
		t.Fatal(view, err)
	}
	for _, event := range view.Page.Events {
		if event.Decision != "denied" {
			t.Fatal("full mode fabricated an allowed event", event)
		}
	}
}

func TestCollectorReadsPartialLinesAndRotationsWithoutDuplicates(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	w, err := createHistory(root, "nook-run-rotation-test", "ask", Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.file.Close()
	path := filepath.Join(source, "egress-decisions.jsonl")
	c := auditCollector{writer: w, path: path, mode: "ask"}
	defer func() {
		if c.file != nil {
			c.file.Close()
		}
	}()
	line := auditLine("allow", "tcp", "203.0.113.1:443")
	if err := os.WriteFile(path, []byte(line[:len(line)-1]), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.poll(); err != nil || w.meta.Count != 0 {
		t.Fatal(err, w.meta.Count)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, err = f.WriteString("\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.poll(); err != nil || w.meta.Count != 1 {
		t.Fatal(err, w.meta.Count)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.poll(); err != nil || w.meta.Count != 2 {
		t.Fatal(err, w.meta.Count)
	}
	if err := c.poll(); err != nil || w.meta.Count != 2 {
		t.Fatal("duplicate import", err, w.meta.Count)
	}
}

func TestMultipleRotationsReportGapAndRecoverRetainedIntermediateFile(t *testing.T) {
	w, err := createHistory(t.TempDir(), "nook-run-many-rotations", "ask", Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.file.Close()
	path := filepath.Join(t.TempDir(), "egress-decisions.jsonl")
	c := auditCollector{writer: w, path: path, mode: "ask"}
	defer func() {
		if c.file != nil {
			c.file.Close()
		}
	}()
	line := []byte(auditLine("allow", "tcp", "203.0.113.1:443"))
	if err := os.WriteFile(path, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.poll(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := os.Rename(path, path+".1"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, line, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.poll(); err == nil || w.meta.Count != 2 {
		t.Fatal("missing potential-gap report or retained rotation", err, w.meta.Count)
	}
	if err := c.poll(); err != nil || w.meta.Count != 3 {
		t.Fatal("failed to continue with newest source", err, w.meta.Count)
	}
}

func TestMalformedAuditIsReportedAndDoesNotHideLaterEvents(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	finish, err := ObserveDirectory(root, "nook-run-bad-test", "ask", Info{}, source)
	if err != nil {
		t.Fatal(err)
	}
	p := strings.Repeat("x", maxHistoryLine+100) + "\n{bad-json}\n" + auditLine("deny", "tcp", "203.0.113.1:443")
	if err := os.WriteFile(filepath.Join(source, "egress-decisions.jsonl"), []byte(p), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err == nil {
		t.Fatal("malformed audit silently accepted")
	}
	view, err := ReadHistory(root, HistoryQuery{})
	if err != nil || len(view.Page.Events) != 1 || view.Machines[0].Warning == "" {
		t.Fatal(view, err)
	}
}

func TestHistoryIsHostOnlyAndReadOnlyDiscoveryCreatesNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if _, err := ReadHistory(root, HistoryQuery{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("read created storage", err)
	}
	w, err := createHistory(root, "nook-run-secure-test", "full", Info{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.file.Close()
	for _, name := range []string{"events.jsonl", "machine.json"} {
		path := filepath.Join(w.dir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal(path, err)
		}
	}
	if _, err := ReadHistory(root, HistoryQuery{Machine: "../../escape"}); err == nil {
		t.Fatal("path traversal accepted")
	}
	if _, err := createHistory(root, w.meta.Machine, "full", Info{}); err == nil {
		t.Fatal("archive overwritten")
	}
	path := filepath.Join(w.dir, "events.jsonl")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHistory(root, HistoryQuery{}); err == nil {
		t.Fatal("unsafe event permissions accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(w.dir, "machine.json"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHistory(root, HistoryQuery{}); err == nil {
		t.Fatal("history symlink accepted")
	}
}

func TestUnconfirmedStopIsNotReportedAsComplete(t *testing.T) {
	root := t.TempDir()
	finish, err := ObserveDirectory(root, "nook-run-stop-test", "full", Info{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(false); err == nil {
		t.Fatal("unconfirmed stop accepted")
	}
	view, err := ReadHistory(root, HistoryQuery{})
	if err != nil || view.Machines[0].Warning == "" {
		t.Fatal(view, err)
	}
}

func FuzzParseAudit(f *testing.F) {
	f.Add(auditLine("allow", "dns", "example.com"))
	f.Add("[2026-08-13T02:31:05Z]: egress policy denied connect to 127.0.0.1:80")
	f.Fuzz(func(t *testing.T, line string) {
		if len(line) > maxHistoryLine {
			return
		}
		for _, mode := range []string{"ask", "full"} {
			event, err := parseAudit([]byte(line), mode)
			if err == nil && event.Decision != "allowed" && event.Decision != "denied" {
				t.Fatal(fmt.Sprint(event))
			}
		}
	})
}
