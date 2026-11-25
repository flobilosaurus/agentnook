// Package fleet discovers existing agentnook VMs through smolvm's public CLI.
// It never registers instances, starts guests, or reads profile credentials.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/flobilosaurus/agentnook/internal/agents"
	"github.com/flobilosaurus/agentnook/internal/config"
	"github.com/flobilosaurus/agentnook/internal/paths"
	"github.com/flobilosaurus/agentnook/internal/telemetry"
)

const maxOutput = 4 << 20

// Instance contains observed machine data, not the project's mutable defaults.
// AgentVersion and BaseImage are catalog metadata, not observations inside the VM.
type Instance struct {
	Name, State, Profile, Image string
	Project                     string // Exact workspace mount; never replaced by Git grouping.
	Workspace                   Workspace
	ImageName, BaseImage        string
	Agent, AgentVersion         string
	Network                     string // Launch label; empty for older machines.
	CPUs, MemoryMiB             int
	PID                         *int
	StorageGiB                  *int
	CreatedAt                   time.Time
	Ephemeral                   bool
	Mounts                      []config.Mount
	Activity                    telemetry.Sample
}

type Snapshot struct {
	Instances []Instance
	Warning   string
}

type Source struct {
	Agents     agents.Catalog
	Binary     string
	Paths      paths.Paths
	readers    map[string]*telemetry.Reader
	workspaces map[string]workspaceObservation
	mu         sync.Mutex // Discovery can still be unwinding when watch exits.
}

// Load uses two public CLI views because smolvm 1.33's JSON only includes a
// mount count. A machine exiting between views is omitted until the next poll.
func (s *Source) Load(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	binary := s.Binary
	if binary == "" {
		binary = "smolvm"
	}
	data, err := command(ctx, binary, "machine", "ls", "--json")
	if err != nil {
		return Snapshot{}, err
	}
	machines, err := parseMachines(data)
	if err != nil {
		return Snapshot{}, err
	}
	if len(machines) == 0 {
		s.closeReaders()
		return Snapshot{}, nil
	}
	verbose, err := command(ctx, binary, "machine", "ls", "--verbose")
	if err != nil {
		return Snapshot{}, err
	}
	mounts, err := parseMounts(verbose, machines)
	if err != nil {
		return Snapshot{}, err
	}
	var result Snapshot
	usedReaders := make(map[string]bool)
	for _, machine := range machines {
		shares, found := mounts[machine.Name]
		if !found { // Exited between JSON and verbose snapshots.
			continue
		}
		profile, agent, project := "", "", ""
		workspaces := 0
		for _, mount := range shares {
			if mount.Guest == "/workspace" {
				project = mount.Host
				workspaces++
			}
			if mount.Guest == "/root" {
				name, kind := filepath.Base(filepath.Dir(mount.Host)), filepath.Base(mount.Host)
				_, valid := s.Agents.Lookup(kind)
				if config.ValidateProfileName(name) == nil && valid == nil && mount.Host == s.Paths.AgentDir(name, kind) {
					profile, agent = name, kind
				}
			}
		}
		if workspaces != 1 || profile == "" {
			continue // Not an unambiguous agentnook instance of this scope/data home.
		}
		if len(shares) != machine.MountCount {
			return Snapshot{}, errors.New("smolvm mount count changed or verbose format is unsupported; retrying")
		}
		created := time.Time{}
		if machine.CreatedAt > 0 {
			created = time.Unix(machine.CreatedAt, 0)
		}
		image := machine.Image
		if artifact := machine.Labels["agentnook.image"]; filepath.IsAbs(artifact) && filepath.Clean(artifact) == artifact && strings.HasSuffix(artifact, ".smolmachine") && !unsafeText(artifact) {
			image = artifact
		}
		network := machine.Labels["agentnook.network"]
		if network != "ask" && network != "full" {
			network = ""
		}
		result.Instances = append(result.Instances, Instance{
			Name: machine.Name, State: machine.State, Profile: profile, Image: image, Agent: agent, Project: project, Network: network,
			CPUs: machine.CPUs, MemoryMiB: machine.MemoryMiB, PID: machine.PID,
			StorageGiB: machine.StorageGiB, CreatedAt: created, Ephemeral: machine.Ephemeral || machine.Labels["agentnook.ephemeral"] == "true", Mounts: shares,
			Activity: s.activitySample(machine.Name, shares, usedReaders),
		})
	}
	for key, reader := range s.readers {
		if !usedReaders[key] {
			_ = reader.Close()
			delete(s.readers, key)
		}
	}
	if len(result.Instances) == 0 {
		return result, nil
	}
	s.observeWorkspaces(ctx, result.Instances)
	cfg, err := config.Load(s.Paths.ConfigFile())
	if err != nil {
		result.Warning = "Image catalog unavailable; image metadata is unknown."
	} else {
		s.imageMetadata(ctx, result.Instances, cfg.Images)
	}
	sort.Slice(result.Instances, func(i, j int) bool {
		left, right := result.Instances[i], result.Instances[j]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.Name < right.Name
	})
	return result, nil
}

// RunningMachineNames is the lightweight runtime check for network history.
// Its caller scopes the names by matching them against its own data home's history.
func RunningMachineNames(ctx context.Context) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := command(ctx, "smolvm", "machine", "ls", "--json")
	if err != nil {
		return nil, err
	}
	machines, err := parseMachines(data)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, machine := range machines {
		if machine.State == "running" {
			names[machine.Name] = true
		}
	}
	return names, nil
}

// Close releases read-only telemetry handles; it never removes guest files.
func (s *Source) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeReaders()
}

func (s *Source) closeReaders() {
	for key, reader := range s.readers {
		_ = reader.Close()
		delete(s.readers, key)
	}
}

func (s *Source) activitySample(name string, mounts []config.Mount, used map[string]bool) telemetry.Sample {
	dir := ""
	for _, mount := range mounts {
		if mount.Guest == telemetry.GuestDir {
			if dir != "" { // An ambiguous endpoint must not pick an arbitrary report.
				return telemetry.Sample{}
			}
			dir = mount.Host
		}
	}
	if dir == "" {
		return telemetry.Sample{}
	}
	key := name + "\x00" + dir
	reader := s.readers[key]
	if reader == nil {
		var err error
		reader, err = telemetry.Open(dir)
		if err != nil {
			return telemetry.Sample{}
		}
		if s.readers == nil {
			s.readers = make(map[string]*telemetry.Reader)
		}
		s.readers[key] = reader
	}
	used[key] = true
	return reader.Poll(time.Now())
}

type machine struct {
	Name       string            `json:"name"`
	State      string            `json:"state"`
	CPUs       int               `json:"cpus"`
	MemoryMiB  int               `json:"memory_mib"`
	PID        *int              `json:"pid"`
	MountCount int               `json:"mounts"`
	CreatedAt  int64             `json:"created_at"`
	StorageGiB *int              `json:"storage_gb"`
	Image      string            `json:"image"`
	Ephemeral  bool              `json:"ephemeral"`
	Labels     map[string]string `json:"labels"`
}

func parseMachines(data []byte) ([]machine, error) {
	var all []machine
	if len(data) > maxOutput || !utf8.Valid(data) || !strings.HasPrefix(strings.TrimSpace(string(data)), "[") || json.Unmarshal(data, &all) != nil {
		return nil, errors.New("invalid smolvm machine ls JSON (expected an array)")
	}
	var running []machine
	seen := make(map[string]bool)
	for _, item := range all {
		if item.Name == "" || strings.IndexFunc(item.Name, unicode.IsSpace) >= 0 || unsafeText(item.Name) || seen[item.Name] {
			return nil, errors.New("invalid or duplicate smolvm machine name")
		}
		seen[item.Name] = true
		switch item.State {
		case "running", "unreachable", "frozen":
		default:
			continue
		}
		if item.CPUs < 1 || item.MemoryMiB < 1 || item.MountCount < 0 || item.MountCount > 4096 || item.CreatedAt < 0 ||
			(item.PID != nil && *item.PID < 1) || (item.StorageGiB != nil && *item.StorageGiB < 1) {
			return nil, errors.New("invalid smolvm machine resource values")
		}
		running = append(running, item)
	}
	return running, nil
}

// parseMounts deliberately accepts only smolvm 1.33's indented Mount records.
// Ambiguous delimiters are rejected instead of guessing a workspace identity.
func parseMounts(data []byte, machines []machine) (map[string][]config.Mount, error) {
	if len(data) > maxOutput || !utf8.Valid(data) {
		return nil, errors.New("invalid smolvm verbose output")
	}
	known := make(map[string]bool, len(machines))
	for _, item := range machines {
		known[item.Name] = true
	}
	result := make(map[string][]config.Mount)
	current := ""
	recognized := strings.TrimSpace(string(data)) == "No machines found"
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" && !strings.HasPrefix(line, " ") {
			current = ""
			fields := strings.Fields(line)
			if len(fields) >= 5 && fields[0] == "NAME" && fields[1] == "STATE" && fields[4] == "MOUNTS" {
				recognized = true
			}
			if len(fields) > 1 && known[fields[0]] && (fields[1] == "running" || fields[1] == "unreachable" || fields[1] == "frozen") {
				current = fields[0]
				recognized = true
				if _, duplicate := result[current]; duplicate {
					return nil, errors.New("duplicate machine in smolvm verbose output")
				}
				result[current] = nil
			}
		}
		if current == "" || !strings.HasPrefix(line, "  Mount: ") {
			continue
		}
		value := strings.TrimPrefix(line, "  Mount: ")
		if strings.Count(value, " -> ") != 1 {
			return nil, errors.New("ambiguous mount path in smolvm verbose output")
		}
		host, guest, _ := strings.Cut(value, " -> ")
		readOnly := strings.HasSuffix(guest, " (ro)")
		if readOnly {
			guest = strings.TrimSuffix(guest, " (ro)")
		}
		if !filepath.IsAbs(host) || filepath.Clean(host) != host || !path.IsAbs(guest) || path.Clean(guest) != guest || unsafeText(host) || unsafeText(guest) {
			return nil, errors.New("invalid mount path in smolvm verbose output")
		}
		result[current] = append(result[current], config.Mount{Host: host, Guest: guest, ReadOnly: readOnly})
	}
	if !recognized {
		return nil, errors.New("unsupported smolvm machine ls --verbose format")
	}
	return result, nil
}

func unsafeText(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) > maxOutput-b.Len() {
		return 0, errors.New("smolvm output exceeds 4 MiB")
	}
	return b.Buffer.Write(data)
}

func command(ctx context.Context, binary string, args ...string) ([]byte, error) {
	return commandEnv(ctx, binary, nil, args...)
}

func commandEnv(ctx context.Context, binary string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 200 * time.Millisecond
	var out limitedBuffer
	cmd.Stdout = &out
	// Deliberately omit raw stderr: verbose output may contain host environment.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("%s %s: %w", filepath.Base(binary), strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}
