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

	"github.com/flobilosaurus/agentnook/internal/config"
)

// Find rejects unsupported runtimes before creating profiles or build storage.
func Find() (string, error) { return findVersion(1, 36, 1) }

// FindMediated requires the current protected runtime as well as SMOLMEG2.
func FindMediated() (string, error) { return Find() }

func findVersion(wantMajor, wantMinor, wantPatch int) (string, error) {
	required := fmt.Sprintf("%d.%d.%d", wantMajor, wantMinor, wantPatch)
	binary, err := exec.LookPath("smolvm")
	if err != nil {
		return "", fmt.Errorf("smolvm >= %s is required: %w", required, err)
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
			if e1 == nil && e2 == nil && e3 == nil && patch >= 0 && minor >= 0 && (major > wantMajor || major == wantMajor && (minor > wantMinor || minor == wantMinor && patch >= wantPatch)) {
				return binary, nil
			}
		}
	}
	return "", fmt.Errorf("smolvm >= %s is required; update smolvm (reported %q)", required, strings.TrimSpace(string(output)))
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
	var envNames []string
	mediated := false
	privateExceptions := false
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
		if arg == "--mediated-egress" {
			mediated = true
			continue
		}
		if arg == "--net" || arg == "--block-private" {
			create = append(create, arg)
			continue
		}
		if i+1 >= len(args) {
			fmt.Fprintln(streams.Err, "nook: incomplete launch option")
			return 1
		}
		value := args[i+1]
		if arg == "--allow-private" {
			privateExceptions = true
		}
		i++
		// Forward values only to the agent exec. Watch gets validated names, never values.
		if arg == "--secret-env" {
			guest, host, ok := strings.Cut(value, "=")
			if !ok || host == "" {
				fmt.Fprintln(streams.Err, "nook: invalid environment forward")
				return 1
			}
			envNames = append(envNames, guest)
			execute = append(execute, arg, value)
			continue
		}
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
	start := []string{"machine", "start", "--name", name}
	var startEnv []string
	if mediated {
		if privateExceptions {
			fmt.Fprintln(streams.Err, "nook: private-network exceptions would bypass mediated approvals; refusing launch")
			return 1
		}
		if streams.Mediation == nil || streams.Mediation.Address == "" || streams.Mediation.Token == "" {
			fmt.Fprintln(streams.Err, "nook: mediated network requires a host broker; refusing unrestricted launch")
			return 1
		}
		start = append(start, "--mediated-egress", "--egress-interceptor", streams.Mediation.Address)
		startEnv = []string{"SMOLVM_INTERCEPTOR_TOKEN=" + streams.Mediation.Token}
	}
	networkMode := "full"
	if mediated {
		networkMode = "ask"
	}
	envNames, err := config.NormalizeEnvNames(envNames)
	if err != nil {
		fmt.Fprintln(streams.Err, "nook: invalid environment forward")
		return 1
	}
	create = append(create, "--label", "agentnook.image="+source,
		"--label", "agentnook.network="+networkMode,
		"--label", "agentnook.env="+strings.Join(envNames, ","),
		"--label", "agentnook.ephemeral=true", "--", "sleep", "infinity")
	execute = append(execute, "--")
	execute = append(execute, args[split+1:]...)
	quiet := IO{Out: streams.Err, Err: streams.Err}
	var finishAudit func(bool) error
	started := false
	defer func() {
		if finishAudit != nil {
			stopped := !started
			if started {
				stopped = Run(binary, []string{"machine", "stop", "--name", name}, quiet) == 0
			}
			if err := finishAudit(stopped); err != nil {
				fmt.Fprintln(streams.Err, "nook: network history incomplete:", err)
				if code == 0 {
					code = 1
				}
			}
		}
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
	if streams.Observe != nil {
		var err error
		finishAudit, err = streams.Observe(binary, name)
		if err != nil {
			fmt.Fprintln(streams.Err, "nook: network history:", err)
			return 1
		}
	}
	started = true // A failed/interrupted start may still have spawned a VMM.
	if code = session.run(binary, start, quiet, startEnv); code != 0 {
		return code
	}
	return session.Run(binary, execute, streams)
}
