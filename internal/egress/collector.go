package egress

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ObserveVM starts before the VM starts networking. Close must run after stop
// but BEFORE machine delete removes smolvm's audit files. It never changes any
// VM policy. full has denials-only coverage; ask has engine decision coverage.
func ObserveVM(binary, machine, root string, info Info, mode string) (func(bool) error, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "machine", "data-dir", "--name", machine).Output()
	if err != nil {
		return nil, errors.New("cannot locate smolvm network audit directory")
	}
	dir := strings.TrimSpace(string(output))
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || strings.ContainsAny(dir, "\x00\r\n") {
		return nil, errors.New("invalid smolvm audit directory")
	}
	return ObserveDirectory(root, machine, mode, info, dir)
}

// ObserveDirectory also supports deterministic audit-fixture and real VM tests.
func ObserveDirectory(root, machine, mode string, info Info, sourceDir string) (func(bool) error, error) {
	w, err := createHistory(root, machine, mode, info)
	if err != nil {
		return nil, err
	}
	name := "egress-denials.log"
	if mode == "ask" {
		name = "egress-decisions.jsonl"
	}
	collector := auditCollector{writer: w, path: filepath.Join(sourceDir, name), mode: mode}
	done := make(chan bool)
	result := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		defer w.file.Close()
		defer func() {
			if collector.file != nil {
				collector.file.Close()
			}
		}()
		var firstErr error
		poll := func() {
			if err := collector.poll(); err != nil && firstErr == nil {
				firstErr = err
				w.meta.Warning = "Audit collection incomplete: read, parse, rotation or storage failure."
			}
			if err := w.flush(); err != nil && firstErr == nil {
				firstErr = err
				w.meta.Warning = "Audit storage failed; recorded history may be incomplete."
			}
		}
		for {
			select {
			case <-ticker.C:
				poll()
			case stopped := <-done:
				// Drain every retained source byte once writes have stopped.
				for i := 0; i < 32; i++ {
					poll()
					if !collector.more {
						break
					}
				}
				if collector.more || len(collector.partial) > 0 || !stopped {
					w.meta.Warning = "Audit incomplete: unconfirmed stop, unread data or partial final event."
					if firstErr == nil {
						firstErr = errors.New("network audit was not completely finalized")
					}
				}
				w.meta.Ended = time.Now().UTC()
				if err := w.flush(); err != nil && firstErr == nil {
					firstErr = err
				}
				result <- firstErr
				return
			}
		}
	}()
	var once sync.Once
	var finalErr error
	return func(stopped bool) error {
		once.Do(func() { done <- stopped; finalErr = <-result })
		return finalErr
	}, nil
}

type auditCollector struct {
	writer   *historyWriter
	path     string
	mode     string
	file     *os.File
	partial  []byte
	dropping bool
	more     bool
}

// Retain an open inode across rotation; do not rely on the rotated pathname
// remaining present. More than one unseen rotation is reported, never hidden.
func (c *auditCollector) poll() error {
	c.more = false
	if c.file == nil {
		// Recover a first rotation that happened before the first poll.
		first := c.path
		if _, err := os.Lstat(c.path + ".1"); err == nil {
			first += ".1"
			c.writer.meta.Warning = "Audit already rotated before collection; older events may be missing."
		}
		f, err := openAudit(first)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		c.file = f
	}
	if err := c.drain(); err != nil || c.more {
		return err
	}
	current, err := os.Lstat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // rename may be followed by a short gap before recreation
	}
	if err != nil {
		return err
	}
	old, err := c.file.Stat()
	if err != nil {
		return err
	}
	if os.SameFile(old, current) {
		return nil
	}
	rotated, err := os.Lstat(c.path + ".1")
	var gap error
	if err != nil || !os.SameFile(old, rotated) {
		gap = errors.New("network audit rotation exceeded retained source history")
	}
	if len(c.partial) > 0 || c.dropping {
		gap = errors.New("partial event at audit rotation")
		c.partial, c.dropping = nil, false
	}
	nextPath := c.path
	if gap != nil && rotated != nil {
		nextPath += ".1" // Recover the newest retained intermediate rotation too.
	}
	f, err := openAudit(nextPath)
	if err != nil {
		return err // Keep the old inode/cursor so a retry cannot duplicate it.
	}
	c.file.Close()
	c.file = f
	if err := c.drain(); err != nil {
		return err
	}
	return gap
}

func openAudit(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Getuid()) {
			err = errors.New("unsafe smolvm network audit file")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (c *auditCollector) drain() error {
	var buf [8192]byte
	var firstErr error
	for read := 0; read < 2*1024*1024; {
		n, err := c.file.Read(buf[:])
		read += n
		for _, ch := range buf[:n] {
			if ch == '\n' {
				if c.dropping {
					c.dropping = false
					continue
				}
				event, parseErr := parseAudit(c.partial, c.mode)
				c.partial = nil
				if parseErr != nil {
					if firstErr == nil {
						firstErr = parseErr
					}
					continue
				}
				if err := c.writer.append(event); err != nil {
					return err
				}
			} else if !c.dropping {
				c.partial = append(c.partial, ch)
				if len(c.partial) >= maxHistoryLine {
					c.partial, c.dropping = nil, true
					firstErr = errors.New("oversized network audit event")
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return firstErr
		}
		if err != nil {
			return err
		}
	}
	c.more = true
	return firstErr
}

func parseAudit(line []byte, mode string) (HistoryEvent, error) {
	var event HistoryEvent
	if mode == "ask" {
		var raw struct {
			Timestamp int64  `json:"timestampMs"`
			Machine   string `json:"machineId"`
			Protocol  string `json:"transport"`
			Target    string `json:"destination"`
			Action    string `json:"action"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			return event, errors.New("invalid structured network audit event")
		}
		id, err := hex.DecodeString(raw.Machine)
		if err != nil || len(id) != 16 || raw.Timestamp <= 0 || raw.Action != "allow" && raw.Action != "deny" {
			return event, errors.New("invalid network audit identity or decision")
		}
		event = HistoryEvent{Time: time.UnixMilli(raw.Timestamp).UTC(), MachineID: raw.Machine, Protocol: raw.Protocol, Target: strings.TrimPrefix(raw.Target, "to "), Decision: "denied", Reason: raw.Reason}
		if raw.Action == "allow" {
			event.Decision = "allowed"
		}
	} else {
		text := string(line)
		const marker = "egress policy denied "
		i := strings.Index(text, marker)
		if i < 0 || !strings.HasPrefix(text, "[") {
			return event, errors.New("invalid network denial line")
		}
		end := strings.IndexByte(text, ']')
		if end < 1 {
			return event, errors.New("missing network denial timestamp")
		}
		stamp, err := time.Parse(time.RFC3339Nano, text[1:end])
		if err != nil {
			return event, errors.New("invalid network denial timestamp")
		}
		op, target, ok := strings.Cut(text[i+len(marker):], " ")
		if !ok {
			return event, errors.New("missing network denial destination")
		}
		protocol := map[string]string{"connect": "tcp", "sendto": "udp", "resolve": "dns", "icmp": "icmp"}[op]
		event = HistoryEvent{Time: stamp.UTC(), Protocol: protocol, Target: strings.TrimPrefix(target, "to "), Decision: "denied", Reason: "local_policy"}
	}
	if event.Protocol != "tcp" && event.Protocol != "udp" && event.Protocol != "icmp" && event.Protocol != "dns" || !validMetadata(event.Target, 512) || !validMetadata(event.Reason, 128) {
		return event, fmt.Errorf("invalid network event metadata")
	}
	return event, nil
}
