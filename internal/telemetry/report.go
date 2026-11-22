package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	maxReport  = 4096
	StaleAfter = 5 * time.Second
	maxUptime  = uint64((1<<63 - 1) / int64(time.Second))
)

var (
	runPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
	sessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

type Report struct {
	Version       int    `json:"version"`
	RunID         string `json:"runId"`
	Seq           uint64 `json:"seq"`
	SessionID     string `json:"sessionId"`
	State         string `json:"state"`
	UptimeSeconds uint64 `json:"uptimeSeconds"`
}

// Sample contains only observed process uptime, never extrapolated host time.
// HeartbeatAt uses the host's monotonic clock, not a guest-provided timestamp.
type Sample struct {
	Report      Report
	HeartbeatAt time.Time
	Fresh       bool
	Advanced    bool
}

func (s Sample) State() string {
	if !s.Fresh {
		return "unknown"
	}
	return s.Report.State
}

// Reader pins a single run's directory and independently tracks sequence progress.
// Consumers must not share a Reader concurrently; each gets its own observer.
type Reader struct {
	root      *os.Root
	runID     string
	lastSeq   uint64
	lastFresh time.Time
	observed  bool
}

func Open(dir string) (*Reader, error) {
	id := strings.TrimPrefix(filepath.Base(dir), directoryPrefix)
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || !runPattern.MatchString(id) || filepath.Base(dir) != directoryPrefix+id {
		return nil, errors.New("invalid telemetry directory")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("telemetry directory must not be a symlink")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Reader{root: root, runID: id}, nil
}

func (r *Reader) Close() error { return r.root.Close() }

func (r *Reader) ReadReport() (Report, bool) {
	// Refuse symlinks, directories and guest-created FIFOs; cap every read.
	f, err := r.root.OpenFile("status.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Report{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Report{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReport+1))
	if err != nil {
		return Report{}, false
	}
	return parseReport(data, r.runID)
}

// Poll requires sequence progress after the first observation: opening watch
// must not make an old, abandoned report appear fresh for five seconds.
func (r *Reader) Poll(now time.Time) Sample {
	report, valid := r.ReadReport()
	advanced := false
	if valid && (!r.observed || report.Seq > r.lastSeq) {
		if r.observed {
			r.lastFresh, advanced = now, true
		}
		r.lastSeq, r.observed = report.Seq, true
	}
	fresh := valid && report.Seq == r.lastSeq && !r.lastFresh.IsZero() && now.Sub(r.lastFresh) < StaleAfter
	return Sample{Report: report, HeartbeatAt: r.lastFresh, Fresh: fresh, Advanced: advanced}
}

func parseReport(data []byte, runID string) (Report, bool) {
	var r Report
	if len(data) > maxReport || !utf8.Valid(data) {
		return r, false
	}
	// Reject duplicate, unknown, null and missing fields at the trust boundary.
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return r, false
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return r, false
		}
		key, ok := token.(string)
		if !ok {
			return r, false
		}
		if _, duplicate := fields[key]; duplicate {
			return r, false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return r, false
		}
		fields[key] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return r, false
	}
	if _, err := d.Token(); err != io.EOF {
		return r, false
	}
	if len(fields) != 6 {
		return r, false
	}
	for key, target := range map[string]any{"version": &r.Version, "runId": &r.RunID, "seq": &r.Seq, "sessionId": &r.SessionID, "state": &r.State, "uptimeSeconds": &r.UptimeSeconds} {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, target) != nil {
			return r, false
		}
	}
	valid := r.Version == 2 && r.RunID == runID && r.Seq > 0 && r.Seq <= 1<<53-1 && sessionPattern.MatchString(r.SessionID) &&
		r.UptimeSeconds <= maxUptime && (r.State == "idle" || r.State == "working" || r.State == "blocked")
	return r, valid
}
