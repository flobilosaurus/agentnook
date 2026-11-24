package e2e

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

func (f realVMFixture) testPiNetworkApproval(t *testing.T) {
	ctx := context.Background()
	network := startTestTerminal(t, f.command(ctx, "network"))
	network.expect(t, 8*time.Second, `\[Approvals\]`)
	network.expect(t, 5*time.Second, `No pending connections`)

	pi := startTestTerminal(t, f.command(ctx, "--agent", "pi", "--profile", "smoke", "--network", "ask", "--", "--offline", "--no-session"))
	defer func() {
		if t.Failed() {
			t.Logf("Pi terminal:\n%s\nNetwork terminal:\n%s", pi.text(), network.text())
		}
	}()
	pi.expect(t, 90*time.Second, `Pi can explain its own features`)

	// Use plain HTTP and don't follow GitHub's HTTPS redirect: approve one
	// connection, not multiple. The actual status isn't in the echoed input.
	pi.send(t, `!curl -q -4 -fsS --noproxy '*' --max-time 20 -o /dev/null -w 'GitHub HTTP %{http_code}\n' http://github.com/`+"\r")
	const response = `GitHub HTTP [23][0-9]{2}`
	network.expect(t, 8*time.Second, `└─ Profile smoke · 1 pending`)
	network.expect(t, 3*time.Second, `└─ VM pi · [a-f0-9]{32} · 1 pending`)
	row := network.expect(t, 3*time.Second, `└─ +github\.com \(HTTP Host\)\* · [0-9.]+:80`)
	target := strings.Fields(row)[5]
	network.send(t, "j")
	network.expect(t, 3*time.Second, `TCP destination: `+regexp.QuoteMeta(target))
	network.expect(t, 3*time.Second, `HTTP Host \(unverified\): github\.com`)
	if regexp.MustCompile(response).MatchString(pi.text()) {
		t.Fatal("curl received an HTTP response before approval")
	}
	network.send(t, "a")
	network.expect(t, 5*time.Second, `Decision sent: once`)
	pi.expect(t, 20*time.Second, response)

	network.send(t, "q")
	network.wait(t)
	pi.send(t, "/quit\r")
	pi.wait(t)
}

// Observe text rendered to a real PTY and send the same keys a user would.
// The mutex protects the transcript while the child writes asynchronously.
type testTerminal struct {
	file *os.File
	done chan struct{}
	err  error // Published by closing done.
	mu   sync.Mutex
	out  bytes.Buffer
}

func startTestTerminal(t *testing.T, cmd *exec.Cmd) *testTerminal {
	t.Helper()
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 140})
	if err != nil {
		t.Fatal(err)
	}
	term := &testTerminal{file: file, done: make(chan struct{})}
	go func() { _, _ = io.Copy(term, file) }()
	go func() {
		term.err = cmd.Wait()
		close(term.done)
	}()
	t.Cleanup(func() {
		defer file.Close()
		select {
		case <-term.done:
			return
		default:
		}
		// Give nook time to stop/delete its VM before falling back to a kill.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-term.done:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			<-term.done
			t.Errorf("terminal process did not stop after SIGTERM: %v", cmd.Args)
		}
	})
	return term
}

func (p *testTerminal) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(data)
}

func (p *testTerminal) text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ansi.Strip(p.out.String())
}

func (p *testTerminal) send(t *testing.T, keys string) {
	t.Helper()
	if _, err := io.WriteString(p.file, keys); err != nil {
		t.Fatalf("send terminal input: %v", err)
	}
}

func (p *testTerminal) expect(t *testing.T, timeout time.Duration, pattern string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		if match := re.FindString(p.text()); match != "" {
			return match
		}
		select {
		case <-tick.C:
		case <-p.done:
			t.Fatalf("terminal exited before rendering %q: %v\n%s", pattern, p.err, p.text())
		case <-deadline.C:
			t.Fatalf("terminal did not render %q within %s\n%s", pattern, timeout, p.text())
		}
	}
}

func (p *testTerminal) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatalf("terminal did not exit cleanly: %v", p.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("terminal did not exit")
	}
}
