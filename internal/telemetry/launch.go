// Package telemetry manages untrusted, display-only activity reports. Agent-specific
// payloads and launch commands are supplied by injected adapters.
package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/flobilosaurus/agentnook/internal/agents"
)

const GuestDir = agents.TelemetryDir
const directoryPrefix = "agentnook-telemetry-"

type Launch struct {
	dir, runID string
	plan       agents.TelemetryPlan
	once       sync.Once
}

func (l *Launch) Dir() string   { return l.dir }
func (l *Launch) RunID() string { return l.runID }

// Validate plans before touching the host filesystem. Adapters may supply files
// and shares beneath this run only; no arbitrary host sources are accepted.
func validatePlan(plan *agents.TelemetryPlan) error {
	local := func(name string) bool {
		return name != "." && filepath.IsLocal(name) && filepath.Clean(name) == name && !strings.ContainsAny(name, ":\x00\r\n")
	}
	for name := range plan.Files {
		if !local(name) {
			return fmt.Errorf("invalid telemetry file %q", name)
		}
	}
	for _, name := range plan.Directories {
		if !local(name) {
			return fmt.Errorf("invalid telemetry directory %q", name)
		}
	}
	for _, mount := range plan.Mounts {
		if !local(mount.Source) || !path.IsAbs(mount.Guest) || path.Clean(mount.Guest) != mount.Guest || strings.ContainsAny(mount.Guest, ":\x00\r\n") {
			return errors.New("invalid telemetry share")
		}
	}
	if len(plan.Command) == 0 || plan.Command[0] == "" {
		return errors.New("empty telemetry workload")
	}
	return nil
}

func Prepare(adapter agents.Agent, tty bool, pass []string) (*Launch, error) {
	if adapter == nil {
		return nil, errors.New("telemetry requires an agent adapter")
	}
	plan := adapter.Telemetry(tty, pass)
	if plan == nil {
		return nil, nil
	}
	if err := validatePlan(plan); err != nil {
		return nil, err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	l := &Launch{runID: hex.EncodeToString(id), plan: *plan}
	dir := filepath.Join(os.TempDir(), directoryPrefix+l.runID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	l.dir = dir
	ok := false
	defer func() {
		if !ok {
			l.Close()
		}
	}()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	l.dir = canonical
	if strings.ContainsAny(l.dir, ":\x00\r\n") {
		return nil, errors.New("invalid telemetry mount path")
	}
	for _, name := range plan.Directories {
		if err := os.MkdirAll(filepath.Join(l.dir, name), 0o700); err != nil {
			return nil, err
		}
	}
	for name, data := range plan.Files {
		if err := os.WriteFile(filepath.Join(l.dir, name), data, 0o600); err != nil {
			return nil, err
		}
	}
	ok = true
	return l, nil
}

// Args keeps per-run identity in the workload, outside smolvm's hashed --env inputs.
func (l *Launch) Args(args []string) []string {
	for i, arg := range args {
		if arg != "--" || i+1 >= len(args) {
			continue
		}
		out := append([]string(nil), args[:i]...)
		out = append(out, "--volume", l.dir+":"+GuestDir)
		for _, mount := range l.plan.Mounts {
			volume := filepath.Join(l.dir, mount.Source) + ":" + mount.Guest
			if mount.ReadOnly {
				volume += ":ro"
			}
			out = append(out, "--volume", volume)
		}
		out = append(out, "--", "env", "AGENTNOOK_REPORT_ID="+l.runID)
		out = append(out, l.plan.Command...)
		return append(out, args[i+2:]...)
	}
	return args
}
func (l *Launch) Close() { l.once.Do(func() { _ = os.RemoveAll(l.dir) }) }
