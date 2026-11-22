// Package vm builds smolvm argv without shell interpolation of paths or agent arguments.
package vm

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
)

func Args(project, agentDir string, options config.Options, adapter agents.Agent, tty bool, pass []string, mounts ...config.Mount) []string {
	args := []string{
		"machine", "run", "--net", "--interactive",
		"--from", options.Image,
		"--cpus", strconv.Itoa(options.CPUs),
		"--mem", strconv.Itoa(options.Memory),
		"--storage", strconv.Itoa(options.Storage),
		"--user", "root",
		"--workdir", "/workspace",
		"--env", "HOME=" + agents.GuestHome,
		"--volume", project + ":/workspace",
		"--volume", agentDir + ":" + agents.GuestHome,
	}
	for _, env := range adapter.Environment() {
		args = append(args, "--env", env)
	}
	for _, mount := range mounts {
		volume := mount.Host + ":" + mount.Guest
		if mount.ReadOnly {
			volume += ":ro"
		}
		args = append(args, "--volume", volume)
	}
	if tty {
		args = append(args, "--tty", "--env", "TERM=xterm-256color")
	}
	args = append(args, "--")
	return append(args, adapter.Command(pass)...)
}

type IO struct {
	In       io.Reader
	Out, Err io.Writer
}

// Session handles signals across a multi-command lifecycle, including gaps
// between create/start/exec. A canceled session never starts another workload.
type Session struct {
	signals     chan os.Signal
	interrupted os.Signal
}

func NewSession() *Session {
	s := &Session{signals: make(chan os.Signal, 8)}
	signal.Notify(s.signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return s
}
func (s *Session) Close() { signal.Stop(s.signals) }
func signalCode(sig os.Signal) int {
	if unix, ok := sig.(syscall.Signal); ok {
		return 128 + int(unix)
	}
	return 1
}
func Run(binary string, args []string, streams IO) int {
	s := NewSession()
	defer s.Close()
	return s.Run(binary, args, streams)
}
func (s *Session) Run(binary string, args []string, streams IO) int {
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	select {
	case sig := <-s.signals:
		s.interrupted = sig
	default:
	}
	if s.interrupted != nil {
		return signalCode(s.interrupted)
	}
	cmd := exec.Command(binary, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.In, streams.Out, streams.Err
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(streams.Err, "nook: start smolvm:", err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-s.signals:
			s.interrupted = sig
			_ = cmd.Process.Signal(sig)
		case err := <-done:
			if s.interrupted != nil {
				return signalCode(s.interrupted)
			}
			if err == nil {
				return 0
			}
			if exit, ok := err.(*exec.ExitError); ok {
				if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					return 128 + int(status.Signal())
				}
				return exit.ExitCode()
			}
			fmt.Fprintln(streams.Err, "nook: smolvm:", err)
			return 1
		}
	}
}
