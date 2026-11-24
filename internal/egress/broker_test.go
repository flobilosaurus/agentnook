package egress

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func prelude(token []byte, machine byte, target string, payload []byte) []byte {
	p := append([]byte("SMOLMEG2"), token...)
	id := make([]byte, 16)
	id[0] = machine
	p = append(p, id...)
	p = append(p, make([]byte, 16)...)
	addr := netip.MustParseAddrPort(target)
	family := byte(6)
	if addr.Addr().Is4() {
		family = 4
	}
	p = append(p, family)
	p = binary.BigEndian.AppendUint16(p, addr.Port())
	p = append(p, addr.Addr().AsSlice()...)
	p = binary.BigEndian.AppendUint16(p, uint16(len(payload)))
	return append(p, payload...)
}

func hello(name string) []byte {
	ext := binary.BigEndian.AppendUint16(nil, uint16(3+len(name)))
	ext = append(ext, 0)
	ext = binary.BigEndian.AppendUint16(ext, uint16(len(name)))
	ext = append(ext, name...)
	ext = append(binary.BigEndian.AppendUint16([]byte{0, 0}, uint16(len(ext))), ext...)
	body := append([]byte{3, 3}, make([]byte, 32)...)
	body = append(body, 0, 0, 2, 0x13, 1, 1, 0)
	body = binary.BigEndian.AppendUint16(body, uint16(len(ext)))
	body = append(body, ext...)
	handshake := append([]byte{1, 0, byte(len(body) >> 8), byte(len(body))}, body...)
	return append(binary.BigEndian.AppendUint16([]byte{22, 3, 1}, uint16(len(handshake))), handshake...)
}

func TestReadFlow(t *testing.T) {
	token := bytes.Repeat([]byte{7}, 32)
	for _, target := range []string{"203.0.113.8:443", "[2001:db8::8]:8443"} {
		f, err := readFlow(bytes.NewReader(prelude(token, 1, target, hello("api.example.com"))), token)
		if err != nil || f.target != target || f.hint != "api.example.com" || len(f.machine) != 32 {
			t.Fatal(f, err)
		}
	}
	valid := prelude(token, 1, "203.0.113.8:443", nil)
	for _, mutation := range []func([]byte) []byte{
		func(p []byte) []byte { p[0] = 0; return p },
		func(p []byte) []byte { p[8] ^= 1; return p },
		func(p []byte) []byte { p[40] = 0; return p },
		func(p []byte) []byte { p[72] = 9; return p },
		func(p []byte) []byte { p[79], p[80] = 0xff, 0xff; return p },
		func(p []byte) []byte { return p[:len(p)-1] },
	} {
		if _, err := readFlow(bytes.NewReader(mutation(bytes.Clone(valid))), token); err == nil {
			t.Fatal("malformed or unauthenticated prelude accepted")
		}
	}
}

func TestServerNameIsBoundedAndDisplayOnly(t *testing.T) {
	for _, name := range []string{"api.example.com", "evil\x1b[31m.com", "bad\nname", strings.Repeat("a", 254)} {
		got := serverName(hello(name))
		if name == "api.example.com" && got != name || name != "api.example.com" && got != "" {
			t.Fatal(name, got)
		}
	}
	p := hello("api.example.com")
	for i := range p {
		_ = serverName(p[:i]) // Truncation must not panic.
	}
}

func TestHTTPHost(t *testing.T) {
	const request = "GET /path HTTP/1.1\r\n"
	for _, tt := range []struct{ label, input, want string }{
		{"host", request + "Host: github.com\r\n\r\n", "github.com"},
		{"mixed case and port", request + "hOsT:\tExample.COM.:8080 \r\n\r\n", "Example.COM."},
		{"HTTP/1.0", "GET / HTTP/1.0\r\nHost: example.com\r\n\r\n", "example.com"},
		{"first request only", request + "Host: first.example\r\n\r\n" + request + "Host: other.example\r\n\r\n", "first.example"},
		{"not in body", request + "Content-Length: 17\r\n\r\nHost: example.com", ""},
		{"incomplete headers", request + "Host: example.com\r\n", ""},
		{"duplicate host", request + "Host: a.example\r\nhost: b.example\r\n\r\n", ""},
		{"folded host", request + "Host:\r\n example.com\r\n\r\n", ""},
		{"ip literal", request + "Host: 203.0.113.8:80\r\n\r\n", ""},
		{"ipv6 literal", request + "Host: [2001:db8::8]:80\r\n\r\n", ""},
		{"invalid port", request + "Host: example.com:65536\r\n\r\n", ""},
		{"invalid hostname", request + "Host: example..com\r\n\r\n", ""},
		{"oversize label", request + "Host: " + strings.Repeat("a", 64) + ".example\r\n\r\n", ""},
		{"control character", request + "Host: bad\x1bname.example\r\n\r\n", ""},
		{"absolute target", "GET http://first.example/ HTTP/1.1\r\nHost: other.example\r\n\r\n", ""},
		{"CONNECT", "CONNECT first.example:443 HTTP/1.1\r\nHost: other.example\r\n\r\n", ""},
		{"other protocol", "SOMETHING ELSE\r\nHost: example.com\r\n\r\n", ""},
	} {
		t.Run(tt.label, func(t *testing.T) {
			if got := httpHost([]byte(tt.input)); got != tt.want {
				t.Fatalf("httpHost = %q, want %q", got, tt.want)
			}
		})
	}
}

func FuzzReadFlow(f *testing.F) {
	token := bytes.Repeat([]byte{7}, 32)
	f.Add(prelude(token, 1, "203.0.113.8:443", hello("example.com")))
	f.Add(prelude(token, 1, "203.0.113.8:80", []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, p []byte) {
		_, _ = readFlow(bytes.NewReader(p), token)
		_ = serverName(p)
	})
}

func startBroker(t *testing.T) (string, *Broker) {
	t.Helper()
	root := t.TempDir()
	b, err := Start(root, Info{Project: "/project", Profile: "work", Agent: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return root, b
}

func connectFlow(t *testing.T, b *Broker, machine byte, target string, payload []byte) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", b.Address(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	token, _ := hex.DecodeString(b.Token())
	if _, err := c.Write(prelude(token, machine, target, payload)); err != nil {
		t.Fatal(err)
	}
	return c
}

func waitEntries(t *testing.T, root string, n int) []Entry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Requests) == n {
			return s.Requests
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d pending entries", n)
	return nil
}

func verdict(t *testing.T, c net.Conn, want byte) {
	t.Helper()
	var p [1]byte
	if _, err := io.ReadFull(c, p[:]); err != nil || p[0] != want {
		t.Fatalf("verdict %d, want %d: %v", p[0], want, err)
	}
}

func TestBrokerOnceDenySessionAndLaunchIsolation(t *testing.T) {
	root, b := startBroker(t)
	target := "203.0.113.8:443"
	c := connectFlow(t, b, 1, target, hello("example.com"))
	entries := waitEntries(t, root, 1)
	if entries[0].Request.Hint != "example.com" || entries[0].Info.Project != "/project" {
		t.Fatal(entries)
	}
	if err := Decide(entries[0], "once"); err != nil {
		t.Fatal(err)
	}
	verdict(t, c, 0)
	if err := Decide(entries[0], "session"); err == nil {
		t.Fatal("request decided twice")
	}
	c = connectFlow(t, b, 1, target, nil)
	entries = waitEntries(t, root, 1)
	if err := Decide(entries[0], "deny"); err != nil {
		t.Fatal(err)
	}
	verdict(t, c, 1)
	c = connectFlow(t, b, 1, target, hello("example.com"))
	c2 := connectFlow(t, b, 1, target, hello("other.example"))
	entries = waitEntries(t, root, 2)
	if err := Decide(entries[0], "session"); err != nil {
		t.Fatal(err)
	}
	verdict(t, c, 0)
	verdict(t, c2, 0)
	// Session grants use the attested launch/IP/port, never guest SNI.
	verdict(t, connectFlow(t, b, 1, target, hello("another.example")), 0)
	for _, v := range []struct {
		machine byte
		target  string
	}{{2, target}, {1, "203.0.113.8:8443"}, {1, "203.0.113.9:443"}} {
		c := connectFlow(t, b, v.machine, v.target, hello("example.com"))
		entry := waitEntries(t, root, 1)[0]
		if err := Decide(entry, "deny"); err != nil {
			t.Fatal(err)
		}
		verdict(t, c, 1)
	}
	_, other := startBroker(t)
	c = connectFlow(t, other, 1, target, nil)
	deadline := time.Now().Add(time.Second)
	for len(other.snapshot().Requests) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(other.snapshot().Requests) != 1 {
		t.Fatal("grant leaked across launches")
	}
	other.Close()
	verdict(t, c, 1)
}

func TestBrokerHTTPHostIsOnlyADisplayHint(t *testing.T) {
	root, b := startBroker(t)
	payload := func(host string) []byte {
		return []byte("GET /private-path HTTP/1.1\r\nHost: " + host + "\r\nCookie: secret-cookie\r\n\r\n")
	}
	const target = "203.0.113.8:80"
	c := connectFlow(t, b, 1, target, payload("first.example"))
	e := waitEntries(t, root, 1)[0]
	if e.Request.Target != target || e.Request.HTTPHost != "first.example" || e.Request.Hint != "" {
		t.Fatal(e.Request)
	}
	if data, err := json.Marshal(e.Request); err != nil || bytes.Contains(data, []byte("private-path")) || bytes.Contains(data, []byte("secret-cookie")) {
		t.Fatal("request snapshot contains extra HTTP metadata", err)
	}
	if err := Decide(e, "session"); err != nil {
		t.Fatal(err)
	}
	verdict(t, c, 0)
	verdict(t, connectFlow(t, b, 1, target, payload("other.example")), 0)
	c = connectFlow(t, b, 1, "203.0.113.9:80", payload("first.example"))
	e = waitEntries(t, root, 1)[0]
	if err := Decide(e, "deny"); err != nil {
		t.Fatal(err)
	}
	verdict(t, c, 1)
}

func TestBrokerFailsClosedOnCloseAndExpiry(t *testing.T) {
	root, b := startBroker(t)
	c := connectFlow(t, b, 1, "203.0.113.8:443", nil)
	entry := waitEntries(t, root, 1)[0]
	b.mu.Lock()
	p := b.pending[entry.Request.ID]
	p.request.Deadline = time.Now().Add(-time.Second)
	b.mu.Unlock()
	if err := Decide(entry, "session"); err == nil {
		t.Fatal("expired decision accepted")
	}
	b.Close()
	verdict(t, c, 1)
	if s, err := Load(root); err != nil || s.Sessions != 0 {
		t.Fatal(s, err)
	}
	if _, err := os.Stat(b.file); !os.IsNotExist(err) {
		t.Fatal("registration not removed", err)
	}
}

func TestUnansweredFlowTimesOutWithoutGrant(t *testing.T) {
	t.Parallel()
	root, b := startBroker(t)
	c := connectFlow(t, b, 1, "203.0.113.8:443", nil)
	_ = c.SetDeadline(time.Now().Add(ApprovalTimeout + 2*time.Second))
	entry := waitEntries(t, root, 1)[0]
	verdict(t, c, 1)
	if err := Decide(entry, "session"); err == nil {
		t.Fatal("timeout accepted a late grant")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.allowed) != 0 {
		t.Fatal("timeout created grant")
	}
}

func TestBrokerAuthenticatesBothInterfaces(t *testing.T) {
	_, b := startBroker(t)
	c, err := net.Dial("tcp", b.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	_, _ = c.Write(prelude(make([]byte, 32), 1, "203.0.113.8:443", nil))
	var v [1]byte
	if _, err := c.Read(v[:]); err == nil {
		t.Fatal("unauthenticated mediation received a verdict")
	}
	reg := registration{Address: b.control.Addr().String(), Token: b.controlToken}
	client := controlClient()
	defer client.CloseIdleConnections()
	for _, v := range []struct{ token, origin string }{{"wrong", ""}, {b.Token(), ""}, {b.controlToken, "https://evil.example"}} {
		r, _ := http.NewRequest(http.MethodGet, "http://"+reg.Address+"/requests", nil)
		r.Header.Set("Authorization", "Bearer "+v.token)
		r.Header.Set("Origin", v.origin)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal(response.StatusCode)
		}
	}
	if len(b.snapshot().Requests) != 0 {
		t.Fatal("unauthenticated peer created pending approval")
	}
}

func TestPrivateRegistrationsAndDataHomeIsolation(t *testing.T) {
	root, b := startBroker(t)
	for _, path := range []string{filepath.Join(root, "network"), b.file} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatal(path, err)
		}
	}
	if _, err := readRegistration(b.file); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(b.file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistration(link); err == nil {
		t.Fatal("symlink registration accepted")
	}
	if err := os.Chmod(b.file, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistration(b.file); err == nil {
		t.Fatal("world-readable control token accepted")
	}
	if s, err := Load(t.TempDir()); err != nil || s.Sessions != 0 {
		t.Fatal(s, err)
	}
	outside := t.TempDir()
	root2 := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root2, "network")); err != nil {
		t.Fatal(err)
	}
	if b, err := Start(root2, Info{}); err == nil {
		b.Close()
		t.Fatal("symlink registry accepted")
	}
}

func TestRejectsWritableDataRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o777); err != nil {
		t.Fatal(err)
	}
	if b, err := Start(root, Info{}); err == nil {
		b.Close()
		t.Fatal("broker registry could be replaced by another user")
	}
}

func TestRejectsNonLoopbackControlAddress(t *testing.T) {
	for _, address := range []string{"example.com:80", "192.0.2.1:80", "127.0.0.1:0"} {
		file := filepath.Join(t.TempDir(), "registration.json")
		p, _ := json.Marshal(registration{Address: address, Token: strings.Repeat("x", 32)})
		if err := os.WriteFile(file, p, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readRegistration(file); err == nil {
			t.Fatal(address)
		}
	}
}
