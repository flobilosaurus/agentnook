package egress

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const maxHistoryLine = 4096

var historyName = regexp.MustCompile(`^nook-run-[a-z0-9-]{1,64}$`)

// MachineHistory is durable host metadata, never authority to reach a broker.
// An unset Ended means not finalized, not proof that the VM is still running.
type MachineHistory struct {
	Version  int       `json:"version"`
	Machine  string    `json:"machine"`
	Info     Info      `json:"info"`
	Mode     string    `json:"mode"`
	Coverage string    `json:"coverage"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitempty"`
	Count    uint64    `json:"count"`
	Warning  string    `json:"warning,omitempty"`
}

// HistoryEvent contains policy metadata only. Allowed does not mean connected,
// HTTP success, or safe content. No initial application bytes are retained.
type HistoryEvent struct {
	Sequence  uint64    `json:"sequence"`
	Time      time.Time `json:"time"`
	MachineID string    `json:"machine_id,omitempty"`
	Protocol  string    `json:"protocol"`
	Target    string    `json:"target"`
	Decision  string    `json:"decision"` // allowed or denied
	Reason    string    `json:"reason"`
}

type HistoryQuery struct {
	Machine string
	Before  int64 // Stable byte cursor; zero means the newest page.
	Limit   int
}

type HistoryPage struct {
	Before     int64          `json:"before"`
	Machine    string         `json:"machine"`
	Events     []HistoryEvent `json:"events"` // Newest first.
	NextBefore int64          `json:"next_before"`
	HasOlder   bool           `json:"has_older"`
}

type HistoryView struct {
	Machines []MachineHistory `json:"machines"`
	Page     HistoryPage      `json:"page"`
}

func secureHistoryFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
			err = errors.New("unsafe history file permissions or owner")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func historyDirectory(root string, create bool) (string, error) {
	dir := filepath.Join(root, "network", "history")
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
	}
	if err := safeRoot(root); err != nil {
		return "", err
	}
	for _, p := range []string{filepath.Join(root, "network"), dir} {
		if create {
			if err := privateDir(p); err != nil {
				return "", err
			}
		} else if err := checkHistoryDir(p); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func checkHistoryDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("unsafe history directory permissions or owner")
	}
	return nil
}

type historyWriter struct {
	dir         string
	meta        MachineHistory
	file        *os.File
	lastCount   uint64
	lastWarning string
}

func createHistory(root, machine, mode string, info Info) (*historyWriter, error) {
	if !historyName.MatchString(machine) || mode != "ask" && mode != "full" {
		return nil, errors.New("invalid history machine or network mode")
	}
	dir, err := historyDirectory(root, true)
	if err != nil {
		return nil, err
	}
	finalDir := filepath.Join(dir, machine)
	if _, err := os.Lstat(finalDir); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("machine history already exists or is inaccessible")
	}
	dir, err = os.MkdirTemp(dir, ".initializing-")
	if err != nil {
		return nil, fmt.Errorf("create machine history: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	coverage := "denials-only"
	if mode == "ask" {
		coverage = "engine-decisions"
	}
	w := &historyWriter{dir: dir, file: f, meta: MachineHistory{Version: 1, Machine: machine, Info: info, Mode: mode, Coverage: coverage, Started: time.Now().UTC()}}
	if err := w.save(); err != nil {
		f.Close()
		return nil, err
	}
	if err := os.Rename(dir, finalDir); err != nil {
		f.Close()
		return nil, err
	}
	w.dir = finalDir
	return w, nil
}

func (w *historyWriter) save() error {
	p, err := json.Marshal(w.meta)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(w.dir, ".machine-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(p); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(w.dir, "machine.json"))
}

func (w *historyWriter) append(event HistoryEvent) error {
	event.Sequence = w.meta.Count + 1
	p, err := json.Marshal(event)
	if err != nil || len(p) >= maxHistoryLine {
		return errors.New("invalid history event")
	}
	if _, err := w.file.Write(append(p, '\n')); err != nil {
		return err
	}
	w.meta.Count++
	return nil
}

func (w *historyWriter) flush() error {
	if w.meta.Count == w.lastCount && w.meta.Warning == w.lastWarning && w.meta.Ended.IsZero() {
		return nil
	}
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.save(); err != nil {
		return err
	}
	w.lastCount, w.lastWarning = w.meta.Count, w.meta.Warning
	return nil
}

// ReadHistory needs no runtime or running VM, and never creates storage. All
// machines are discoverable; event reads are bounded and byte-cursor paginated.
func ReadHistory(root string, query HistoryQuery) (HistoryView, error) {
	dir, err := historyDirectory(root, false)
	if errors.Is(err, os.ErrNotExist) {
		if query.Machine != "" {
			return HistoryView{}, errors.New("unknown archived machine")
		}
		return HistoryView{}, nil
	}
	if err != nil {
		return HistoryView{}, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return HistoryView{}, err
	}
	view := HistoryView{}
	for _, entry := range files {
		if !historyName.MatchString(entry.Name()) {
			continue
		}
		machineDir := filepath.Join(dir, entry.Name())
		if err := checkHistoryDir(machineDir); err != nil {
			return view, err
		}
		f, err := secureHistoryFile(filepath.Join(machineDir, "machine.json"))
		if err != nil {
			return view, err
		}
		var meta MachineHistory
		err = json.NewDecoder(io.LimitReader(f, 16*1024)).Decode(&meta)
		f.Close()
		if err != nil || meta.Version != 1 || meta.Machine != entry.Name() ||
			meta.Mode != "ask" && meta.Mode != "full" ||
			meta.Mode == "ask" && meta.Coverage != "engine-decisions" ||
			meta.Mode == "full" && meta.Coverage != "denials-only" {
			return view, errors.New("invalid machine history metadata")
		}
		view.Machines = append(view.Machines, meta)
	}
	sort.Slice(view.Machines, func(i, j int) bool {
		a, b := view.Machines[i], view.Machines[j]
		if a.Started.Equal(b.Started) {
			return a.Machine < b.Machine
		}
		return a.Started.After(b.Started)
	})
	if query.Machine == "" && len(view.Machines) > 0 {
		query.Machine = view.Machines[0].Machine
	}
	if query.Machine == "" {
		return view, nil
	}
	found := false
	for _, meta := range view.Machines {
		found = found || meta.Machine == query.Machine
	}
	if !found {
		return view, errors.New("unknown archived machine")
	}
	f, err := secureHistoryFile(filepath.Join(dir, query.Machine, "events.jsonl"))
	if err != nil {
		return view, err
	}
	defer f.Close()
	view.Page, err = readHistoryPage(f, query)
	return view, err
}

func readHistoryPage(f *os.File, q HistoryQuery) (HistoryPage, error) {
	page := HistoryPage{Machine: q.Machine, Before: q.Before, Events: []HistoryEvent{}}
	info, err := f.Stat()
	if err != nil {
		return page, err
	}
	end := info.Size()
	if q.Before < 0 || q.Before > end {
		return page, errors.New("invalid history cursor")
	}
	if q.Before > 0 {
		end = q.Before
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 200)
	// Each generated line is below 4096 bytes. Read one extra line so the
	// window can start in the middle of a line without losing a whole page.
	start := max(int64(0), end-int64((limit+2)*maxHistoryLine))
	p := make([]byte, end-start)
	if _, err := f.ReadAt(p, start); err != nil && !errors.Is(err, io.EOF) {
		return page, err
	}
	if start > 0 {
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			start += int64(i + 1)
			p = p[i+1:]
		}
	}
	// A partially written final line must wait for the next refresh.
	last := bytes.LastIndexByte(p, '\n')
	if last < 0 {
		return page, nil
	}
	p = p[:last+1]
	position := start + int64(len(p))
	lines := bytes.Split(p[:len(p)-1], []byte{'\n'})
	for i := len(lines) - 1; i >= 0 && len(page.Events) < limit; i-- {
		position -= int64(len(lines[i]) + 1)
		var e HistoryEvent
		if len(lines[i]) >= maxHistoryLine || json.Unmarshal(lines[i], &e) != nil || e.Decision != "allowed" && e.Decision != "denied" {
			return page, errors.New("invalid archived network event")
		}
		page.Events = append(page.Events, e)
		page.NextBefore = position
	}
	page.HasOlder = page.NextBefore > 0
	return page, nil
}

func validMetadata(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 127 })
}
