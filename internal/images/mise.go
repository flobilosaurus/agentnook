package images

import (
	"slices"

	"github.com/flobilosaurus/agentnook/internal/config"
)

// MiseDir is outside the profile HOME and the mounted workspace.
const MiseDir = "/opt/agentnook/mise"
const miseExec = MiseDir + "/exec"

// WithTools wraps the final workload, including any telemetry command. No
// project mise config is evaluated to construct the agent's startup PATH.
func WithTools(args []string, image config.BuiltImage) []string {
	if i := slices.Index(args, "--"); len(image.Tools) > 0 && i >= 0 && i+1 < len(args) {
		return slices.Insert(slices.Clone(args), i+1, miseExec)
	}
	return args
}

// Tool specs are positional arguments, not shell source. Cache and build state
// are disposable; the pinned config, mise binary and tools live in the image.
const installMise = `
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export HOME="$tmp" XDG_CONFIG_HOME="$tmp/config" XDG_DATA_HOME="$tmp/data"
export MISE_CACHE_DIR="$tmp/cache" MISE_STATE_DIR="$tmp/state"
export DEBIAN_FRONTEND=noninteractive
cd "$tmp"

if ! command -v curl >/dev/null; then
  apt-get update
  apt-get install -y --no-install-recommends curl
  rm -rf /var/lib/apt/lists/*
fi

version=2026.4.1
case "$(uname -m)" in
  aarch64|arm64) arch=arm64; sha=2a66fdcd2244cafbe79fa72d30419dfbbf082aa1f995205a5b45a69f0e725821 ;;
  x86_64) arch=x64; sha=df910da773d5c58ecb06c95cdf90ee448135a7b5cb00f188d5d217108c789112 ;;
  *) echo 'nook: no mise binary for this guest architecture' >&2; exit 1 ;;
esac
archive="mise-v$version-linux-$arch.tar.gz"
curl -qfsSL --retry 3 --proto '=https' "https://github.com/jdx/mise/releases/download/v$version/$archive" -o "$archive"
printf '%s  %s\n' "$sha" "$archive" | sha256sum -c -

dir=` + MiseDir + `
mkdir -p "$dir"
tar -xzf "$archive" -C "$dir" --strip-components=1 mise/bin/mise
export MISE_DATA_DIR="$dir/data" MISE_SYSTEM_CONFIG_FILE="$dir/config.toml"
export PATH="$dir/bin:$PATH"
mise use --yes --pin --path "$MISE_SYSTEM_CONFIG_FILE" -- "$@"
mise bin-paths > "$dir/bin-paths"

cat > "$dir/exec" <<'EOF'
#!/bin/sh
set -eu
dir=` + MiseDir + `
export MISE_DATA_DIR="$dir/data" MISE_SYSTEM_CONFIG_FILE="$dir/config.toml"
# Snapshot paths at build time. Don't load workspace configuration or check
# for downloads when starting the agent.
paths=$(paste -sd : "$dir/bin-paths")
export PATH="$dir/bin${paths:+:$paths}:$PATH"
exec "$@"
EOF
chmod 0755 "$dir/exec"
`

// Runs without network in a fresh packed VM. Confirm both the resolved mise
// config and the directories used by the startup wrapper made it into the pack.
const verifyMise = `
test -s "$MISE_SYSTEM_CONFIG_FILE"
mise --version
missing=$(mise --cd "$HOME" ls --current --missing --no-header)
if [ -n "$missing" ]; then
  printf 'nook: tools missing from packed image:\n%s\n' "$missing" >&2
  exit 1
fi
while IFS= read -r bin; do
  test -d "$bin"
done < ` + MiseDir + `/bin-paths
`
