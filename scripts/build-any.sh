#!/usr/bin/env bash
# Build one any payload for a target platform.
#
# Usage: scripts/build-any.sh <platform> <outdir>
#   platform ∈ darwin-arm64 | darwin-x64 | linux-x86_64 | windows-x86_64 |
#              darwin-arm64-sandbox | darwin-x64-sandbox | host
#   (the any backend is CGO-free, so every target cross-builds
#    from any host; only the prebuilt llama.cpp libs are platform-specific.)
#
# Produces: <outdir>/any-<version>-<os>-<arch>[-sandbox].tar.gz
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
#
# The darwin -sandbox variants carry the SAME payload as their plain siblings
# — same tags, same llama.cpp libs, full vector leg including the in-process
# local embedder. They differ only in where libffi comes from (see the build
# step below): they are for consumers that run `any` as an App-Sandboxed or
# hardened-runtime helper without the disable-library-validation entitlement
# (anyproto/any-swift). docs/18-ci.md § Tarball layout.
VARIANT=""
case "$PLATFORM" in
darwin-arm64) GOOS=darwin GOARCH=arm64 LLAMA=macos-arm64 EXE="" OS=darwin ARCH=arm64 ;;
darwin-x64) GOOS=darwin GOARCH=amd64 LLAMA=macos-x64 EXE="" OS=darwin ARCH=x86_64 ;;
darwin-arm64-sandbox) GOOS=darwin GOARCH=arm64 LLAMA=macos-arm64 EXE="" OS=darwin ARCH=arm64 VARIANT=sandbox ;;
darwin-x64-sandbox) GOOS=darwin GOARCH=amd64 LLAMA=macos-x64 EXE="" OS=darwin ARCH=x86_64 VARIANT=sandbox ;;
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
# below — so CGO_ENABLED=0 cross-builds keep working. With `vector` in,
# the default `auto` embedder falls back to the local model, downloading
# its GGUF (~639 MB) into the data dir on first use.
TAGS=fts,vector

# The sandbox variants: same legs, different libffi. `vector` pulls in
# yzma → jupiterrider/ffi, which has TWO package-level inits that run before
# main — embed.go extracts an ad-hoc-signed libffi.8.dylib into the user
# Caches dir and points ffi's `filename` at it, then init.go dlopens
# `filename` and panics if it fails. macOS library validation (the App
# Sandbox, or a hardened runtime without
# com.apple.security.cs.disable-library-validation) denies that load, so the
# process dies before main. `ffi_no_embed` compiles embed.go out, and the -X
# pins `filename` (a plain unexported package var) to Apple's libffi, a
# platform binary in the dyld shared cache that always validates. FFI_NO_EMBED=1
# at runtime is NOT equivalent — it leaves the bare "libffi.8.dylib" name,
# which macOS does not ship. docs/13-index.md § build tags.
if [ "$VARIANT" = sandbox ]; then
    TAGS="$TAGS,ffi_no_embed"
    LDFLAGS="$LDFLAGS -X github.com/jupiterrider/ffi.filename=/usr/lib/libffi.dylib"
fi

CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -trimpath -tags "$TAGS" -ldflags "$LDFLAGS" -o "$STAGE/any$EXE" ./cmd/any

# Guard both halves of the sandbox build. It has to be two-sided: `-X` against
# a symbol that no longer exists is silently ignored by the linker, so an
# upstream rename of ffi's `filename` would quietly restore the Caches path
# and panic under the sandbox again. grep -a, not `go tool nm` — these
# binaries are -s -w stripped, so the edge is invisible in the symbol table.
if [ "$VARIANT" = sandbox ]; then
    if grep -aq 'github.com/jupiterrider/ffi/libffi' "$STAGE/any$EXE"; then
        echo "build-any: $PLATFORM carries the embedded-libffi cache path — the ffi_no_embed tag did not take" >&2
        exit 1
    fi
    if ! grep -aq '/usr/lib/libffi.dylib' "$STAGE/any$EXE"; then
        echo "build-any: $PLATFORM has no /usr/lib/libffi.dylib string — the -X ffi.filename override did not take (symbol renamed upstream?)" >&2
        exit 1
    fi
fi

# Per-platform llama.cpp libs into llamacpp/ (embed_local.go's default lookup
# dir: <dir-of-any-exe>/llamacpp). Stage B overrides via YZMA_LIB in-bundle.
# Sandbox variants stage them too — a consumer bundling this tarball re-signs
# the dylibs with its own team ID, which is exactly what library validation
# wants; only the runtime-extracted libffi was unsignable.
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
# `variant` marks the artifact so a consumer can tell the two darwin builds
# apart in-band; it is absent (not empty) on the plain ones.
jq -n \
    --arg version "$VERSION" --arg os "$OS" --arg arch "$ARCH" --arg llama "$LLAMACPP_VERSION" \
    --arg variant "$VARIANT" \
    --rawfile sums "$SUMS" \
    '{version:$version, os:$os, arch:$arch, llamacpp_version:$llama,
      sha256: ($sums | rtrimstr("\n") | split("\n") | map(split("  ")) | map({(.[1]): .[0]}) | add // {})}
     + (if $variant != "" then {variant:$variant} else {} end)' \
    >"$STAGE/manifest.json"
rm -f "$SUMS"

mkdir -p "$OUTDIR"
TARBALL="$OUTDIR/any-$VERSION-$OS-$ARCH${VARIANT:+-$VARIANT}.tar.gz"
tar -czf "$TARBALL" -C "$STAGE" .
echo "build-any: → $TARBALL"
