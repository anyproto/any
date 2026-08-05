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

# Both search legs are compile-time opt-in (docs/13-index.md § build tags):
# `fts` = the BM25 full-text index, `vector` = the embedding + IVF-SQ ANN
# leg with the embedder implementations. Both are pure Go — the llama.cpp
# libs are loaded at runtime via purego from the llamacpp/ dir staged
# below — so CGO_ENABLED=0 cross-builds keep working. With `vector` in
# (linux/windows), the default `auto` embedder falls back to the local
# model, downloading its GGUF (~639 MB) into the data dir on first use.
#
# Darwin ships `fts` only: `vector` links embed_local.go → yzma →
# jupiterrider/ffi, whose package init dlopens an ad-hoc-signed
# libffi.8.dylib before main, and macOS library validation denies that
# load — the process panics at startup. (Full mechanism, and why
# FFI_NO_EMBED is no escape: docs/13-index.md § build tags.) It is a
# build policy rather than a `capVector` term because the hazard belongs
# to the artifact, not the platform: `make build` on macOS is unsigned
# and fine, and consumers that disable library validation (any-ui) would
# be fine too — but one tarball per platform has to serve the strictest
# consumer, and any-swift ships App Sandbox / Mac App Store.
case "$GOOS" in
darwin) TAGS=fts ;;
*) TAGS=fts,vector ;;
esac

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -tags "$TAGS" -ldflags "$LDFLAGS" -o "$STAGE/any$EXE" ./cmd/any

# Assert what actually got linked, both ways: never an ffi edge in a
# darwin tarball (it panics sandboxed hosts at startup), always one
# elsewhere (a silently vector-less tarball is a degraded index that
# nothing else catches). The expectation is keyed on the platform, NOT
# on $TAGS — deriving it from the tags would let a well-meaning edit
# move both sides together and assert nothing, which is exactly the
# change that shipped the bug this guard exists to prevent.
#
# `go version -m` reads the build info Go embeds in every binary — `-s -w`
# doesn't strip it and it reads cross-compiled output — so this names the
# real dependency rather than a vendor string that a bump could rename.
# Matched without a pipe on purpose: `grep -q` exits on first match, and
# under `pipefail` the resulting SIGPIPE would report "no ffi" for the
# very builds the guard exists to catch.
case "$GOOS" in darwin) WANT_FFI=no ;; *) WANT_FFI=yes ;; esac
BUILDINFO="$(go version -m "$STAGE/any$EXE")"
case "$BUILDINFO" in *github.com/jupiterrider/ffi*) GOT_FFI=yes ;; *) GOT_FFI=no ;; esac
if [ "$GOT_FFI" != "$WANT_FFI" ]; then
    echo "build-any: $PLATFORM built -tags '$TAGS' links jupiterrider/ffi=$GOT_FFI, want $WANT_FFI" >&2
    if [ "$GOOS" = darwin ]; then
        echo "build-any: a darwin binary linking ffi panics at startup in any sandboxed or hardened host" >&2
    else
        echo "build-any: this tarball would ship with vector search silently compiled out" >&2
    fi
    echo "build-any: see docs/13-index.md § build tags" >&2
    exit 1
fi

# Per-platform llama.cpp libs into llamacpp/ (embed_local.go's default lookup
# dir: <dir-of-any-exe>/llamacpp). Stage B overrides via YZMA_LIB in-bundle.
# Still staged on darwin, where nothing can load it without `vector` — it
# costs ~4 MiB of every darwin tarball, but any-ui's release.sh copies
# llamacpp/ out of the extracted tarball unconditionally, so omitting the
# dir would break that build rather than just slim the download.
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
