package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Entry binds an immutable request identity to the broker that published it.
// A UI refresh must never transfer an approval keypress to a different request.
type Entry struct {
	Info    Info    `json:"info"`
	Request Request `json:"request"`
	control registration
}

func (e Entry) Key() string { return e.Info.Session + "/" + e.Request.ID }

type Survey struct {
	Sessions int         `json:"live_brokers"`
	Requests []Entry     `json:"pending"`
	History  HistoryView `json:"history"`
	Warning  string      `json:"live_warning,omitempty"`
}

func controlClient() *http.Client {
	return &http.Client{
		Timeout:       800 * time.Millisecond,
		Transport:     &http.Transport{DialContext: (&net.Dialer{Timeout: 400 * time.Millisecond}).DialContext, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func readRegistration(path string) (registration, error) {
	// Do not follow symlinks or block on a FIFO planted in the registry.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return registration{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return registration{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) || info.Size() > 4096 {
		return registration{}, errors.New("unsafe broker registration")
	}
	var r registration
	if err := json.NewDecoder(io.LimitReader(f, 4096)).Decode(&r); err != nil {
		return registration{}, err
	}
	addr, err := netip.ParseAddrPort(r.Address)
	if err != nil || !addr.Addr().IsLoopback() || addr.Port() == 0 || len(r.Token) < 32 || len(r.Token) > 128 || strings.ContainsAny(r.Token, "\r\n") {
		return registration{}, errors.New("invalid broker registration")
	}
	return r, nil
}

func controlRequest(c *http.Client, r registration, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+r.Address+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.Do(req)
}

// Load polls only this data home's owner-only registrations. Dead brokers are
// ignored; abrupt launcher termination can leave harmless stale registrations.
func Load(root string) (Survey, error) {
	dir := filepath.Join(root, "network")
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Survey{}, nil
	}
	if err != nil {
		return Survey{}, err
	}
	if err := safeRoot(root); err != nil {
		return Survey{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return Survey{}, errors.New("network registry is not an owner-only directory")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return Survey{}, err
	}
	registrations := 0
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			registrations++
		}
	}
	if registrations > 256 {
		return Survey{}, errors.New("too many network registrations; remove stale files in " + dir + " while nook is stopped")
	}
	var result Survey
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	client := controlClient()
	defer client.CloseIdleConnections()
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		reg, err := readRegistration(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			response, err := controlRequest(client, reg, http.MethodGet, "/requests", nil)
			if err != nil {
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return
			}
			var snapshot Snapshot
			if err := json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&snapshot); err != nil || snapshot.Info.Session == "" || len(snapshot.Requests) > maxPending {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			result.Sessions++
			for _, request := range snapshot.Requests {
				if time.Now().Before(request.Deadline) {
					result.Requests = append(result.Requests, Entry{Info: snapshot.Info, Request: request, control: reg})
				}
			}
		}()
	}
	wg.Wait()
	sort.Slice(result.Requests, func(i, j int) bool {
		a, b := result.Requests[i], result.Requests[j]
		if a.Request.Deadline.Equal(b.Request.Deadline) {
			return a.Key() < b.Key()
		}
		return a.Request.Deadline.Before(b.Request.Deadline)
	})
	return result, nil
}

func Decide(entry Entry, choice string) error {
	if !time.Now().Before(entry.Request.Deadline) {
		return errors.New("request expired; retry the guest connection")
	}
	data, err := json.Marshal(Decision{ID: entry.Request.ID, Choice: choice})
	if err != nil {
		return err
	}
	client := controlClient()
	defer client.CloseIdleConnections()
	response, err := controlRequest(client, entry.control, http.MethodPost, "/decisions", bytes.NewReader(data))
	if err != nil {
		return errors.New("broker unavailable; decision outcome unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("decision rejected (HTTP %d); connection not approved", response.StatusCode)
	}
	return nil
}
