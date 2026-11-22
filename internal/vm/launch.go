package vm

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Find rejects unsupported runtimes before creating profiles or build storage.
func Find() (string, error) {
	binary, err := exec.LookPath("smolvm")
	if err != nil {
		return "", fmt.Errorf("smolvm >= 1.33.0 is required: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--version").Output()
	parts := strings.Fields(string(output))
	if err == nil && len(parts) == 2 && parts[0] == "smolvm" {
		version := strings.Split(parts[1], ".")
		if len(version) == 3 {
			major, e1 := strconv.Atoi(version[0])
			minor, e2 := strconv.Atoi(version[1])
			patch, e3 := strconv.Atoi(version[2])
			if e1 == nil && e2 == nil && e3 == nil && patch >= 0 && minor >= 0 && (major > 1 || major == 1 && minor >= 33) {
				return binary, nil
			}
		}
	}
	return "", fmt.Errorf("smolvm >= 1.33.0 is required; update smolvm (reported %q)", strings.TrimSpace(string(output)))
}

// Launch realizes Args as a disposable named machine. Unlike pack-run in smolvm
// 1.33, named machines remain visible to watch. No working state is reused.
func Launch(binary string, args []string, streams IO) (code int) {
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	if len(args) < 3 || args[0] != "machine" || args[1] != "run" {
		fmt.Fprintln(streams.Err, "nook: invalid launch plan")
		return 1
	}
	session := NewSession()
	defer session.Close()
	name := "nook-run-" + strings.ToLower(rand.Text())
	create := []string{"machine", "create", "--name", name}
	execute := []string{"machine", "exec", "--name", name}
	source := ""
	split := -1
	for i := 2; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			split = i
			break
		}
		if arg == "--interactive" || arg == "--tty" {
			execute = append(execute, arg)
			continue
		}
		if arg == "--net" {
			create = append(create, arg)
			continue
		}
		if i+1 >= len(args) {
			fmt.Fprintln(streams.Err, "nook: incomplete launch option")
			return 1
		}
		value := args[i+1]
		i++
		create = append(create, arg, value)
		if arg == "--from" {
			source = value
		}
		// --workdir on create --from is ignored by smolvm 1.33; exec honors it.
		if arg == "--workdir" || arg == "--user" || arg == "--env" {
			execute = append(execute, arg, value)
		}
	}
	if source == "" || split < 0 || split+1 == len(args) {
		fmt.Fprintln(streams.Err, "nook: missing artifact or agent command")
		return 1
	}
	create = append(create, "--label", "agentnook.image="+source,
		"--label", "agentnook.ephemeral=true", "--", "sleep", "infinity")
	execute = append(execute, "--")
	execute = append(execute, args[split+1:]...)
	quiet := IO{Out: streams.Err, Err: streams.Err}
	defer func() {
		if cleanup := Run(binary, []string{"machine", "delete", "--name", name, "--force"}, IO{Out: io.Discard, Err: streams.Err}); cleanup != 0 {
			fmt.Fprintf(streams.Err, "nook: could not remove temporary machine %s; run smolvm machine delete --name %s --force\n", name, name)
			if code == 0 {
				code = 1
			}
		}
	}()
	if code = session.Run(binary, create, quiet); code != 0 {
		return code
	}
	if code = session.Run(binary, []string{"machine", "start", "--name", name}, quiet); code != 0 {
		return code
	}
	return session.Run(binary, execute, streams)
}
