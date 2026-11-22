// Package herdr optionally forwards shared agent telemetry to the host pane.
// The guest never receives Herdr's socket or authority over a different pane.
package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

const (
	source         = "agentnook"
	commandTimeout = 3 * time.Second
)

var panePattern = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,128}$`)

// Bridge owns only host-side lifecycle authority and its telemetry reader.
// The caller owns the shared telemetry launch and closes it after the bridge.
type Bridge struct {
	runID, binary, pane string
	env                 []string
	reader              *telemetry.Reader
	sequence            uint64
	started             time.Time
	session             string
	claimed, unknown    bool
	cancel              context.CancelFunc
	done                chan struct{}
	once                sync.Once
	command             func(context.Context, []string) error
}

// Prepare returns nil without an active telemetry launch and this start's
// Herdr pane context, or when forwarding is opted out. No files are created.
func Prepare(launch *telemetry.Launch) (*Bridge, error) {
	opt := os.Getenv("AGENTNOOK_HERDR")
	if launch == nil || opt == "0" || os.Getenv("HERDR_ENV") != "1" ||
		os.Getenv("HERDR_PANE_ID") == "" || os.Getenv("HERDR_SOCKET_PATH") == "" {
		return nil, nil
	}
	pane := os.Getenv("HERDR_PANE_ID")
	if !panePattern.MatchString(pane) {
		return nil, errors.New("invalid Herdr pane ID")
	}
	binary, err := exec.LookPath("herdr")
	if err != nil {
		return nil, errors.New("herdr not found on PATH")
	}
	reader, err := telemetry.Open(launch.Dir())
	if err != nil {
		return nil, err
	}
	b := &Bridge{runID: launch.RunID(), binary: binary, pane: pane, reader: reader,
		started: time.Now(), sequence: uint64(time.Now().UnixMicro())}
	// Do not forward provider credentials or profile-specific environment.
	for _, key := range []string{"HOME", "USER", "LOGNAME", "PATH", "LANG", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH"} {
		b.env = append(b.env, key+"="+os.Getenv(key))
	}
	b.command = b.exec
	return b, nil
}

func (b *Bridge) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel, b.done = cancel, make(chan struct{})
	go func() {
		defer close(b.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				b.poll(ctx, now)
			}
		}
	}()
}

func (b *Bridge) poll(ctx context.Context, now time.Time) {
	sample := b.reader.Poll(now)
	if ctx.Err() != nil {
		return
	}
	if sample.Advanced {
		b.session, b.unknown = sample.Report.SessionID, false
		_ = b.report(ctx, sample.State())
	} else if !sample.Fresh && now.Sub(b.started) >= telemetry.StaleAfter && !b.unknown {
		b.unknown = b.report(ctx, "unknown") == nil
	}
}

func (b *Bridge) call(ctx context.Context, action string, options ...string) error {
	b.sequence++
	args := []string{"pane", action, b.pane, "--source", source, "--agent", "agentnook", "--seq", strconv.FormatUint(b.sequence, 10)}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return b.command(ctx, append(args, options...))
}

func (b *Bridge) report(ctx context.Context, state string) error {
	// Even a timed-out CLI might have claimed authority; always release on exit.
	b.claimed = true
	if b.session != "" {
		_ = b.call(ctx, "report-metadata", "--applies-to-source", source,
			"--token", "agentnook_run_id="+b.runID, "--token", "agentnook_session_id="+b.session, "--ttl-ms", "5000")
	}
	return b.call(ctx, "report-agent", "--state", state)
}

func (b *Bridge) exec(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, b.binary, args...)
	cmd.Env = b.env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd.Run()
}

// Close releases metadata and lifecycle authority even after signal cancellation.
func (b *Bridge) Close() {
	b.once.Do(func() {
		if b.cancel != nil {
			b.cancel()
			<-b.done
		}
		if b.claimed {
			_ = b.call(context.Background(), "report-metadata", "--applies-to-source", source,
				"--clear-token", "agentnook_run_id", "--clear-token", "agentnook_session_id")
			_ = b.call(context.Background(), "release-agent")
		}
		_ = b.reader.Close()
	})
}
