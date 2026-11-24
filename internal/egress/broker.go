package egress

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ApprovalTimeout leaves headroom under smolvm's 15-second verdict deadline.
const ApprovalTimeout = 12 * time.Second
const maxPending = 128

type Info struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Profile string `json:"profile"`
	Agent   string `json:"agent"`
}

type Request struct {
	ID       string    `json:"id"`
	Machine  string    `json:"machine"`
	Target   string    `json:"target"`
	Hint     string    `json:"hint,omitempty"`
	HTTPHost string    `json:"http_host,omitempty"`
	Deadline time.Time `json:"deadline"`
}

type Snapshot struct {
	Info     Info      `json:"info"`
	Requests []Request `json:"requests"`
}

type Decision struct {
	ID     string `json:"id"`
	Choice string `json:"choice"` // once, session, deny
}

type pending struct {
	request Request
	verdict chan byte
}

type registration struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

// Broker owns one VM launch. Grants cannot outlive it or apply to another VM.
// Its control token and mediation token are separate and never enter the guest.
type Broker struct {
	info         Info
	token        []byte
	controlToken string
	listener     net.Listener
	control      net.Listener
	server       *http.Server
	file         string
	done         chan struct{}
	once         sync.Once
	wg           sync.WaitGroup
	slots        chan struct{}
	mu           sync.Mutex
	pending      map[string]*pending
	allowed      map[string]bool
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("network storage %q must be an owner-only directory owned by this user", path)
	}
	return nil
}

func safeRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("network data root must be owned by this user and not writable by group or others")
	}
	return nil
}

func Start(root string, info Info) (*Broker, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := safeRoot(root); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "network")
	if err := privateDir(dir); err != nil {
		return nil, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	info.Session = strings.ToLower(rand.Text())
	b := &Broker{info: info, token: token, controlToken: rand.Text() + rand.Text(), done: make(chan struct{}),
		slots: make(chan struct{}, maxPending), pending: make(map[string]*pending), allowed: make(map[string]bool)}
	var err error
	b.listener, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b.control, err = net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		b.listener.Close()
		return nil, err
	}
	b.server = &http.Server{Handler: http.HandlerFunc(b.handleControl), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		_ = b.server.Serve(b.control)
	}()
	b.wg.Add(1)
	go b.accept()
	// Publish only after both listeners are ready. The registry is host-only and
	// carries no guest-controlled paths or payloads.
	data, err := json.Marshal(registration{Address: b.control.Addr().String(), Token: b.controlToken})
	if err == nil {
		path := filepath.Join(dir, info.Session+".json")
		var file *os.File
		file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			b.file = path
			_, err = file.Write(data)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *Broker) Session() string { return b.info.Session }
func (b *Broker) Address() string { return b.listener.Addr().String() }
func (b *Broker) Token() string   { return hex.EncodeToString(b.token) }

func (b *Broker) Close() {
	b.once.Do(func() {
		close(b.done)
		b.listener.Close()
		b.server.Close()
		b.wg.Wait()
		if b.file != "" {
			_ = os.Remove(b.file)
		}
	})
}

func (b *Broker) accept() {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return
		}
		select {
		case b.slots <- struct{}{}:
			b.wg.Add(1)
			go func() {
				defer b.wg.Done()
				defer func() { <-b.slots }()
				b.handleFlow(conn)
			}()
		default:
			conn.Close() // Exhaustion fails closed, without unbounded goroutines.
		}
	}
}

func (b *Broker) handleFlow(conn net.Conn) {
	defer conn.Close()
	deadline := time.Now().Add(ApprovalTimeout)
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	f, err := readFlow(conn, b.token)
	if err != nil {
		return
	}
	_ = conn.SetDeadline(deadline)
	key := f.machine + "/" + f.target
	b.mu.Lock()
	if b.allowed[key] {
		b.mu.Unlock()
		_, _ = conn.Write([]byte{0})
		return
	}
	p := &pending{request: Request{
		ID: rand.Text(), Machine: f.machine, Target: f.target, Hint: f.hint, HTTPHost: f.httpHost, Deadline: deadline,
	}, verdict: make(chan byte, 1)}
	b.pending[p.request.ID] = p
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, p.request.ID)
		b.mu.Unlock()
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	verdict := byte(1)
	select {
	case verdict = <-p.verdict:
	case <-timer.C:
	case <-b.done:
	}
	// A delayed UI decision must never authorize an expired flow.
	if !time.Now().Before(deadline) {
		verdict = 1
	}
	// The approval deadline has expired on timeout. Give the single deny byte
	// a separate bounded write window, still below smolvm's 15-second limit.
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, _ = conn.Write([]byte{verdict})
}

func (b *Broker) snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := Snapshot{Info: b.info, Requests: []Request{}}
	for _, p := range b.pending {
		if time.Now().Before(p.request.Deadline) {
			s.Requests = append(s.Requests, p.request)
		}
	}
	sort.Slice(s.Requests, func(i, j int) bool { return s.Requests[i].Deadline.Before(s.Requests[j].Deadline) })
	return s
}

func (b *Broker) decide(d Decision) error {
	if d.Choice != "once" && d.Choice != "session" && d.Choice != "deny" {
		return errors.New("invalid decision")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.pending[d.ID]
	if !ok || !time.Now().Before(p.request.Deadline) {
		return errors.New("request expired or already decided")
	}
	verdict := byte(1)
	if d.Choice != "deny" {
		verdict = 0
	}
	delete(b.pending, d.ID) // Only one controller can decide a request.
	if d.Choice == "session" {
		key := p.request.Machine + "/" + p.request.Target
		b.allowed[key] = true
		// Cover already waiting flows to this exact VM/IP/port as well.
		for id, other := range b.pending {
			if other.request.Machine+"/"+other.request.Target == key && time.Now().Before(other.request.Deadline) {
				other.verdict <- 0
				delete(b.pending, id)
			}
		}
	}
	p.verdict <- verdict
	return nil
}

func (b *Broker) handleControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.controlToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/requests":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(b.snapshot())
	case r.Method == http.MethodPost && r.URL.Path == "/decisions":
		var d Decision
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&d); err != nil {
			http.Error(w, "invalid decision", http.StatusBadRequest)
			return
		}
		if err := b.decide(d); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}
