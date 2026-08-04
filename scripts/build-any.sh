#!/usr/bin/env bash
# Build one any payload for a target platform.
#
# Usage: scripts/build-any.sh <platform> <outdir>
#   platform ∈ darwin-arm64 | darwin-x64 | linux-x86_64 | windows-x86_64 | host
#   (the any backend is CGO-free, so every target cross-builds
#    from any host; only the prebuilt llama.cpp libs are platform-specific.)
#
# Produces: <outdir>/any-<version>-<os>-<arch>.tar.gz
set -euo pipefail

PLATFORM="${1:?usage: build-any.sh <platform> <outdir>}"
OUTDIR="${2:?usage: build-any.sh <platform> <outdir>}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if [ "$PLATFORM" = host ]; then
    case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) PLATFORM=darwin-arm64 ;;
    Darwin-x86_64) PLATFORM=darwin-x64 ;;
    Linux-x86_64) PLATFORM=linux-x86_64 ;;
    *)
        echo "build-any: cannot detect host; pass an explicit platform" >&2
        exit 1
        ;;
    esac
fi

# platform → GOOS GOARCH llama.cpp-token exe-suffix os-label arch-label
# llama.cpp tokens are the GPU-capable archives (Metal on macOS arm64,
# Vulkan+CPU-fallback on Linux/Windows) — see fetch-llamacpp.sh.
case "$PLATFORM" in
darwin-arm64) GOOS=darwin GOARCH=arm64 LLAMA=macos-arm64 EXE="" OS=darwin ARCH=arm64 ;;
darwin-x64) GOOS=darwin GOARCH=amd64 LLAMA=macos-x64 EXE="" OS=darwin ARCH=x86_64 ;;
linux-x86_64) GOOS=linux GOARCH=amd64 LLAMA=ubuntu-vulkan-x64 EXE="" OS=linux ARCH=x86_64 ;;
windows-x86_64) GOOS=windows GOARCH=amd64 LLAMA=win-vulkan-x64 EXE=".exe" OS=windows ARCH=x86_64 ;;
*)
    echo "build-any: unknown platform '$PLATFORM'" >&2
    exit 1
    ;;
esac

LLAMACPP_VERSION="$(sed -n 's/^LLAMACPP_VERSION := //p' Makefile)"
# The workflow passes the resolved release version (a real tag, or a
# v<base>-nightly.<date>.<n> prerelease) so the asset name + embedded version
# match the release; standalone runs fall back to git describe.
VERSION="${ANY_BUILD_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
PKG="github.com/anyproto/any"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "build-any: $PLATFORM  (any $VERSION, llama.cpp $LLAMACPP_VERSION)"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE" "${SUMS:-}"' EXIT

# Swagger docs are a build input (matches `make build`); generate once.
go tool swag init -g doc.go -d ./internal/server,./internal/api \
    -o internal/server/docs --parseDependency --parseInternal >/dev/null

LDFLAGS="-s -w -X $PKG/internal/version.Version=$VERSION -X $PKG/internal/version.Commit=$COMMIT -X $PKG/internal/version.BuildDate=$DATE"

# -tags fts: the BM25 search leg is compile-time opt-in (pure Go, CGO-free).
# Without it the /search endpoint compiles the index out and silently answers
# zero hits, so every SDK consumer (desktop sidecar, any-ui dev) shipped with
# search that looked broken. `vector` is deliberately NOT added here: with it
# compiled in, the default `auto` embedder auto-downloads the 639 MB GGUF on
# first boot (docs/13-index.md § local embedder) — shipping that to every
# desktop user is a product decision, not a build-script default.
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -tags fts -ldflags "$LDFLAGS" -o "$STAGE/any$EXE" ./cmd/any

# Per-platform llama.cpp libs into llamacpp/ (embed_local.go's default lookup
# dir: <dir-of-any-exe>/llamacpp). Stage B overrides via YZMA_LIB in-bundle.
scripts/fetch-llamacpp.sh "$LLAMACPP_VERSION" "$STAGE/llamacpp" "$LLAMA"


# manifest.json — sha256 of every staged file (relative paths) + metadata.
sha256_of() {
    if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
# NB: write the sums list OUTSIDE $STAGE so `find` doesn't enumerate it into
# the manifest (it is removed before the tarball is built).
SUMS="$(mktemp)"
(
    cd "$STAGE"
    find . -type f | sed 's|^\./||' | sort | while IFS= read -r rel; do
        printf '%s  %s\n' "$(sha256_of "$rel")" "$rel"
    done
) >"$SUMS"
jq -n \
    --arg version "$VERSION" --arg os "$OS" --arg arch "$ARCH" --arg llama "$LLAMACPP_VERSION" \
    --rawfile sums "$SUMS" \
    '{version:$version, os:$os, arch:$arch, llamacpp_version:$llama,
      sha256: ($sums | rtrimstr("\n") | split("\n") | map(split("  ")) | map({(.[1]): .[0]}) | add // {})}' \
    >"$STAGE/manifest.json"
rm -f "$SUMS"

mkdir -p "$OUTDIR"
TARBALL="$OUTDIR/any-$VERSION-$OS-$ARCH.tar.gz"
tar -czf "$TARBALL" -C "$STAGE" .
echo "build-any: → $TARBALL"
