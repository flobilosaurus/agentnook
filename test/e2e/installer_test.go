package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseInstaller(t *testing.T) {
	const tag = "v0.1.0"
	for _, tc := range []struct {
		name, os, arch, platform, wantError string
		pinned, badChecksum, duplicate      bool
		inPath, shadowed                    bool
	}{
		{name: "latest_mac", os: "Darwin", arch: "arm64", platform: "darwin_arm64"},
		{name: "pinned_linux_amd64", os: "Linux", arch: "x86_64", platform: "linux_amd64", pinned: true},
		{name: "latest_linux_arm64", os: "Linux", arch: "aarch64", platform: "linux_arm64", inPath: true},
		{name: "shadowed", os: "Darwin", arch: "arm64", platform: "darwin_arm64", inPath: true, shadowed: true},
		{name: "bad_checksum", os: "Darwin", arch: "arm64", platform: "darwin_arm64", badChecksum: true, wantError: "SHA-256 mismatch"},
		{name: "duplicate_checksum", os: "Linux", arch: "aarch64", platform: "linux_arm64", duplicate: true, wantError: "unique release archive"},
		{name: "unsupported", os: "Darwin", arch: "x86_64", wantError: "Unsupported platform"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin, temp := filepath.Join(root, "stubs"), filepath.Join(root, "tmp")
			home := filepath.Join(root, "home with spaces")
			installDir := filepath.Join(home, ".local", "bin")
			if tc.pinned {
				installDir = filepath.Join(root, `custom bin's "quoted"`)
			}
			for _, dir := range []string{bin, temp} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path string, content []byte) {
				t.Helper()
				if err := os.WriteFile(path, content, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(bin, "uname"), []byte(`#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$TEST_OS" ;;
    -m) printf '%s\n' "$TEST_ARCH" ;;
    *) exit 1 ;;
esac
`))
			write(filepath.Join(bin, "curl"), []byte(`#!/bin/sh
set -eu
[ "$#" -eq 14 ]
[ "$1 $2 $3 $4 $5 $6 $7 $8 $9 ${10} ${11} ${12}" = \
    '-q --proto =https --proto-redir =https --fail --silent --show-error --location --retry 3 --output' ]
base=https://github.com/flobilosaurus/agentnook/releases
lookup=latest/download
if [ -n "$TEST_REQUESTED" ]; then lookup=download/$TEST_REQUESTED; fi
case "${14}" in
    "$base/$lookup/checksums.txt") file=checksums.txt ;;
    "$base/download/$TEST_TAG/$TEST_ARCHIVE") file=$TEST_ARCHIVE ;;
    *) exit 1 ;;
esac
cp "$TEST_FIXTURES/$file" "${13}"
`))

			archive := "nook_" + tag + "_" + tc.platform + ".tar.gz"
			binary := []byte("#!/bin/sh\necho nook " + tag + "\n")
			data := installerArchive(t, binary)
			sum := sha256.Sum256(data)
			if tc.badChecksum {
				sum = sha256.Sum256([]byte("different archive"))
			}
			entry := fmt.Sprintf("%x  ./%s\n", sum, archive)
			checklist := fmt.Sprintf("%x  ./nook_v9.9.9_other.tar.gz\n", sum) + entry
			if tc.duplicate {
				checklist += entry
			}
			write(filepath.Join(root, archive), data)
			write(filepath.Join(root, "checksums.txt"), []byte(checklist))

			destination := filepath.Join(installDir, "nook")
			if tc.badChecksum {
				if err := os.MkdirAll(installDir, 0o700); err != nil {
					t.Fatal(err)
				}
				write(destination, []byte("existing installation"))
			}
			args := []string{"../../install.sh"}
			requested := ""
			if tc.pinned {
				requested = tag
				args = append(args, requested)
			}
			path := bin + ":/usr/bin:/bin"
			if tc.inPath {
				path += ":" + installDir + "/"
			}
			if tc.shadowed {
				write(filepath.Join(bin, "nook"), []byte("#!/bin/sh\nexit 1\n"))
			}
			cmd := exec.Command("/bin/sh", args...)
			// Use real host utilities, but no real curl, user config or network.
			cmd.Env = []string{
				"PATH=" + path, "HOME=" + home, "TMPDIR=" + temp,
				"TEST_OS=" + tc.os, "TEST_ARCH=" + tc.arch, "TEST_FIXTURES=" + root,
				"TEST_ARCHIVE=" + archive, "TEST_REQUESTED=" + requested, "TEST_TAG=" + tag,
			}
			if tc.pinned {
				cmd.Env = append(cmd.Env, "NOOK_INSTALL_DIR="+installDir)
			}
			output, err := cmd.CombinedOutput()
			if dirs, err := os.ReadDir(temp); err != nil || len(dirs) != 0 {
				t.Fatalf("temporary files leaked: %v, %v", dirs, err)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(string(output), tc.wantError) {
					t.Fatalf("want %q, got %v: %s", tc.wantError, err, output)
				}
				content, err := os.ReadFile(destination)
				if tc.badChecksum && (err != nil || string(content) != "existing installation") ||
					!tc.badChecksum && !os.IsNotExist(err) {
					t.Fatalf("failed install changed destination: %q, %v", content, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("install: %v\n%s", err, output)
			}
			version, err := exec.Command(destination, "--version").CombinedOutput()
			if err != nil || string(version) != "nook "+tag+"\n" {
				t.Fatalf("installed version: %v: %s", err, version)
			}
			message := string(output)
			wantStatus := " is not in PATH."
			if tc.inPath {
				wantStatus = " is already in PATH."
			}
			if !strings.Contains(message, installDir+wantStatus) {
				t.Fatalf("unexpected PATH status: %s", message)
			}
			if tc.shadowed && !strings.Contains(message, "another nook first: "+filepath.Join(bin, "nook")) {
				t.Fatalf("missing shadowed nook warning: %s", message)
			}
			_, hints, hasHint := strings.Cut(message, "  Bash/Zsh/sh: ")
			if tc.inPath && !tc.shadowed {
				if hasHint || !strings.Contains(message, "Verify with: nook --version") {
					t.Fatalf("unexpected PATH hint: %s", message)
				}
				return
			}
			if !hasHint || !strings.Contains(hints, "fish_add_path --path --move") || !strings.Contains(hints, "$env.PATH = ") {
				t.Fatalf("missing shell hints: %s", message)
			}
			hint, _, _ := strings.Cut(hints, "\n")
			verify := exec.Command("/bin/sh", "-ec", hint+"\ncommand -v nook")
			verify.Env = cmd.Env
			found, err := verify.CombinedOutput()
			if err != nil || strings.TrimSpace(string(found)) != destination {
				t.Fatalf("PATH hint %q found wrong nook: %v: %s", hint, err, found)
			}
		})
	}
}

func installerArchive(t *testing.T, payload []byte) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	archive := tar.NewWriter(gz)
	if err := archive.WriteHeader(&tar.Header{Name: "nook", Size: int64(len(payload)), Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
