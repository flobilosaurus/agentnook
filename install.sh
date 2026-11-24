#!/bin/sh
set -eu

fail() {
    printf 'nook installer: %s\n' "$*" >&2
    exit 1
}

download() {
    curl -q --proto '=https' --proto-redir '=https' --fail --silent --show-error \
        --location --retry 3 --output "$2" "$1"
}

main() {
    releases=https://github.com/flobilosaurus/agentnook/releases
    version_pattern='v[0-9]+[.][0-9]+[.][0-9]+(-[0-9A-Za-z.-]+)?([+][0-9A-Za-z.-]+)?'
    [ "$#" -le 1 ] || fail 'Usage: sh install.sh [VERSION]'
    version=${1-}
    if [ "$#" -eq 1 ]; then
        case "$version" in
            *[!0-9A-Za-z.+-]*) fail 'Expected a version such as v0.1.0.' ;;
        esac
        printf '%s\n' "$version" | grep -Eq "^$version_pattern$" || fail 'Expected a version such as v0.1.0.'
        checksums_url="$releases/download/$version/checksums.txt"
    else
        checksums_url="$releases/latest/download/checksums.txt"
    fi

    os=$(uname -s)
    arch=$(uname -m)
    case "$os/$arch" in
        Darwin/arm64) platform=darwin_arm64 ;;
        Linux/x86_64) platform=linux_amd64 ;;
        Linux/aarch64|Linux/arm64) platform=linux_arm64 ;;
        *) fail "Unsupported platform: $os/$arch (requires Apple Silicon macOS, Linux AMD64 or Linux ARM64)." ;;
    esac

    install_dir=${NOOK_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}
    case "$install_dir" in
        /*) ;;
        *) fail 'NOOK_INSTALL_DIR must be an absolute path.' ;;
    esac
    destination="${install_dir%/}/nook"
    command -v curl >/dev/null 2>&1 || fail 'Install curl to download the release.'

    work=$(mktemp -d "${TMPDIR:-/tmp}/nook-install.XXXXXX")
    staged=
    trap 'rm -rf -- "$work"; if [ -n "$staged" ]; then rm -f -- "$staged"; fi' 0
    trap 'exit 1' HUP INT TERM

    download "$checksums_url" "$work/checksums.txt" \
        || fail "Cannot download checksums for ${version:-the latest release}. Check that the release is published and public."

    # Select exactly one archive. Its name also pins the resolved latest release.
    entry=$(LC_ALL=C awk -v platform="$platform" -v requested="$version" -v tag_pattern="$version_pattern" '
        NF == 2 && length($1) == 64 && $1 ~ /^[0-9a-fA-F]+$/ {
            file = $2
            sub(/^\.\//, "", file)
            if (file !~ ("^nook_" tag_pattern "_" platform "[.]tar[.]gz$")) next
            if (requested != "" && file != ("nook_" requested "_" platform ".tar.gz")) next
            checksum = tolower($1)
            archive = file
            count++
        }
        END {
            if (count != 1) exit 1
            print checksum, archive
        }
    ' "$work/checksums.txt") || fail "Cannot identify a unique release archive for $platform."
    expected=${entry%% *}
    archive=${entry#* }
    tag=${archive#nook_}
    tag=${tag%_"$platform".tar.gz}
    download "$releases/download/$tag/$archive" "$work/$archive" \
        || fail "Cannot download $archive."

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum < "$work/$archive")
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 < "$work/$archive")
    else
        fail 'Install sha256sum or shasum to verify the download.'
    fi
    [ "${actual%% *}" = "$expected" ] || fail "SHA-256 mismatch for $archive."

    # Extract only the executable to stdout; never unpack an archive over user files.
    tar -xOzf "$work/$archive" nook > "$work/nook" || fail 'Cannot extract nook from the release archive.'
    [ -s "$work/nook" ] || fail 'Release archive contains an empty executable.'

    mkdir -p -- "$install_dir"
    [ ! -d "$destination" ] || fail "$destination is a directory."
    staged=$(mktemp "${install_dir%/}/.nook.XXXXXX")
    cp -- "$work/nook" "$staged"
    chmod 755 "$staged"
    mv -f -- "$staged" "$destination"
    staged=

    printf 'Installed nook %s to %s\n' "$tag" "$destination"
    if [ "$(command -v nook || true)" != "$destination" ]; then
        printf 'If nook is not found or another version runs, put %s first in your PATH.\n' "$install_dir" >&2
    fi
    printf 'Verify with: nook --version\n'
}

main "$@"
