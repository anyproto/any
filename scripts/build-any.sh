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

# platform → GOOS GOARCH llama.cpp-token exe-suffix os-label arch-label search-tags
# llama.cpp tokens are the GPU-capable archives (Metal on macOS arm64,
# Vulkan+CPU-fallback on Linux/Windows) — see fetch-llamacpp.sh.
# TAGS is per-platform on purpose — see the build comment below.
case "$PLATFORM" in
darwin-arm64) GOOS=darwin GOARCH=arm64 LLAMA=macos-arm64 EXE="" OS=darwin ARCH=arm64 TAGS=fts ;;
darwin-x64) GOOS=darwin GOARCH=amd64 LLAMA=macos-x64 EXE="" OS=darwin ARCH=x86_64 TAGS=fts ;;
linux-x86_64) GOOS=linux GOARCH=amd64 LLAMA=ubuntu-vulkan-x64 EXE="" OS=linux ARCH=x86_64 TAGS=fts,vector ;;
windows-x86_64) GOOS=windows GOARCH=amd64 LLAMA=win-vulkan-x64 EXE=".exe" OS=windows ARCH=x86_64 TAGS=fts,vector ;;
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

# Both search legs are compile-time opt-in (docs/13-index.md § build tags):
# `fts` = the BM25 full-text index, `vector` = the embedding + IVF-SQ ANN
# leg with the embedder implementations. Both are pure Go — the llama.cpp
# libs are loaded at runtime via purego from the llamacpp/ dir staged
# below — so CGO_ENABLED=0 cross-builds keep working. With `vector` in,
# the default `auto` embedder falls back to the local model, downloading
# its GGUF (~639 MB) into the data dir on first use.
#
# DARWIN SHIPS `fts` ONLY. `vector` links internal/indexer/embed_local.go →
# yzma → jupiterrider/ffi, whose package init unconditionally extracts an
# ad-hoc-signed libffi.8.dylib into os.UserCacheDir() and dlopens it — on
# *link*, before main. macOS library validation (App Sandbox, or the
# hardened runtime without com.apple.security.cs.disable-library-validation)
# denies that load because the extracted dylib carries no Team ID, and the
# process panics at startup. FFI_NO_EMBED=1 is not an out: it falls back to
# dlopen("libffi.8.dylib"), and macOS ships only /usr/lib/libffi.dylib.
# This is the same load-time-crash class as the gomobile carve-out — a
# runtime toggle can't prevent it, so the leg is excluded at compile time.
# Consumers that DO set that entitlement (any-ui) would be fine either way;
# one tarball per platform has to serve the strictest consumer (any-swift
# ships App Sandbox / Mac App Store). Restoring the leg on darwin is
# tracked separately; see docs/13-index.md § build tags.
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -tags "$TAGS" -ldflags "$LDFLAGS" -o "$STAGE/any$EXE" ./cmd/any

# Guard the carve-out above: the ffi edge is invisible in a stripped binary's
# symbol table, so match on the embedded cache path instead (grep -a, not
# `strings`, so this needs no binutils on the runner).
if [ "$GOOS" = darwin ] && grep -qa 'jupiterrider/ffi/libffi' "$STAGE/any$EXE"; then
    echo "build-any: darwin binary links jupiterrider/ffi — sandboxed hosts panic at startup (docs/13-index.md § build tags)" >&2
    exit 1
fi

# Per-platform llama.cpp libs into llamacpp/ (embed_local.go's default lookup
# dir: <dir-of-any-exe>/llamacpp). Stage B overrides via YZMA_LIB in-bundle.
# Staged on darwin too, where it is inert (no `vector`, so nothing dlopens
# it) — dropping it there would be a consumer-visible manifest change.
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
