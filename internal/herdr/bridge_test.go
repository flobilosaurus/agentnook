package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/agents/registry"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

func testAgent(t *testing.T, name string) agents.Agent {
	t.Helper()
	a, err := registry.All().Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func environment(t *testing.T) {
	t.Helper()
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "host:p1")
	t.Setenv("HERDR_SOCKET_PATH", "/host/herdr.sock")
	t.Setenv("AGENTNOOK_HERDR", "")
	t.Setenv("WORK_PI_HERDR", "")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

type testBridge struct {
	*Bridge
	launch *telemetry.Launch
}

func prepare(t *testing.T) *testBridge {
	t.Helper()
	l, err := telemetry.Prepare(testAgent(t, "pi"), true, nil)
	if err != nil || l == nil {
		t.Fatalf("telemetry: %v", err)
	}
	t.Cleanup(l.Close)
	b, err := Prepare(l)
	if err != nil || b == nil {
		t.Fatalf("Herdr: %v", err)
	}
	t.Cleanup(b.Close)
	return &testBridge{Bridge: b, launch: l}
}

func store(t *testing.T, b *testBridge, seq uint64, state string) {
	t.Helper()
	data, err := json.Marshal(telemetry.Report{Version: 2, RunID: b.runID, Seq: seq, SessionID: "test-session", State: state, UptimeSeconds: 42})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.launch.Dir(), "status.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
func value(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestActivation(t *testing.T) {
	environment(t)
	t.Setenv("ANTHROPIC_API_KEY", "must-not-forward")
	l, err := telemetry.Prepare(testAgent(t, "pi"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if b, err := Prepare(nil); b != nil || err != nil {
		t.Fatal("activated without telemetry")
	}
	t.Setenv("AGENTNOOK_HERDR", "0")
	if b, err := Prepare(l); b != nil || err != nil {
		t.Fatal("ignored AGENTNOOK_HERDR")
	}
	t.Setenv("AGENTNOOK_HERDR", "")
	t.Setenv("WORK_PI_HERDR", "0")
	t.Setenv("AGENTNOOK_HERDR", "1")
	b := prepare(t)
	if strings.Contains(strings.Join(b.env, " "), "must-not-forward") {
		t.Fatal("credentials forwarded")
	}
	for _, key := range []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH"} {
		old := os.Getenv(key)
		t.Setenv(key, "")
		if b, err := Prepare(l); b != nil || err != nil {
			t.Fatalf("activated without %s", key)
		}
		t.Setenv(key, old)
	}
}

func TestPrepareFailureDoesNotRemoveTelemetry(t *testing.T) {
	environment(t)
	l, err := telemetry.Prepare(testAgent(t, "pi"), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Setenv("HERDR_PANE_ID", "--help\n")
	if b, err := Prepare(l); b != nil || err == nil {
		t.Fatal("accepted invalid pane")
	}
	t.Setenv("HERDR_PANE_ID", "host:p1")
	t.Setenv("PATH", t.TempDir())
	if b, err := Prepare(l); b != nil || err == nil {
		t.Fatal("accepted missing CLI")
	}
	if _, err := os.Stat(l.Dir()); err != nil {
		t.Fatal("adapter removed shared telemetry", err)
	}
}

func TestCloseLeavesSharedTelemetry(t *testing.T) {
	environment(t)
	b := prepare(t)
	b.Close()
	if _, err := os.Stat(b.launch.Dir()); err != nil {
		t.Fatal("adapter removed shared telemetry", err)
	}
}

func TestHeartbeatReplayRecoveryAndRelease(t *testing.T) {
	environment(t)
	b := prepare(t)
	var calls [][]string
	b.command = func(ctx context.Context, args []string) error {
		if ctx.Err() != nil {
			t.Fatal("cancelled cleanup")
		}
		calls = append(calls, args)
		return nil
	}
	now := time.Now()
	store(t, b, 1, "working")
	b.poll(context.Background(), now) // Baseline alone is not a heartbeat.
	store(t, b, 2, "working")
	b.poll(context.Background(), now.Add(time.Second))
	b.poll(context.Background(), now.Add(6*time.Second)) // Replay is stale.
	b.poll(context.Background(), now.Add(7*time.Second)) // Unknown reported once.
	store(t, b, 3, "blocked")
	b.poll(context.Background(), now.Add(8*time.Second))
	store(t, b, 4, "idle")
	b.poll(context.Background(), now.Add(9*time.Second))
	b.Close()
	var states []string
	var previous uint64
	for _, args := range calls {
		if !reflect.DeepEqual(args[:3], []string{"pane", args[1], "host:p1"}) || value(args, "--source") != "agentnook" || value(args, "--agent") != "agentnook" {
			t.Fatal(args)
		}
		seq, _ := strconv.ParseUint(value(args, "--seq"), 10, 64)
		if seq <= previous {
			t.Fatal("non-monotonic sequence")
		}
		previous = seq
		if args[1] == "report-agent" {
			states = append(states, value(args, "--state"))
		}
		if strings.Contains(strings.Join(args, " "), "--agent-session-") {
			t.Fatal("guest session used as host authority")
		}
	}
	if !reflect.DeepEqual(states, []string{"working", "unknown", "blocked", "idle"}) {
		t.Fatal(states)
	}
	if value(calls[0], "--token") != "agentnook_run_id="+b.runID || value(calls[0], "--ttl-ms") != "5000" {
		t.Fatal(calls[0])
	}
	if calls[len(calls)-2][1] != "report-metadata" || value(calls[len(calls)-2], "--clear-token") != "agentnook_run_id" || calls[len(calls)-1][1] != "release-agent" {
		t.Fatal("missing cleanup", calls)
	}
}

func TestStartupUnknownAndFailedClaimsStillRelease(t *testing.T) {
	environment(t)
	b := prepare(t)
	var calls [][]string
	b.command = func(_ context.Context, args []string) error {
		calls = append(calls, args)
		return errors.New("offline")
	}
	b.poll(context.Background(), b.started.Add(telemetry.StaleAfter))
	b.Close()
	if value(calls[0], "--state") != "unknown" || calls[len(calls)-1][1] != "release-agent" {
		t.Fatal(calls)
	}
}

func TestExecTimeoutAndPollerCancellation(t *testing.T) {
	environment(t)
	b := prepare(t)
	if err := os.WriteFile(b.binary, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if b.exec(ctx, nil) == nil || time.Since(start) > 2*time.Second {
		t.Fatal("CLI not bounded")
	}
	b.Start()
	b.Close()
}
