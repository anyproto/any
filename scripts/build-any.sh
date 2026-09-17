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
#
# A `-<variant>` suffix on a platform selects the same platform built with
# different FLAGS ONLY — the staged payload stays identical, so consumers can
# swap one for the other. Don't use it for capability differences.
#
# ANY_SKIP_SWAG=1 skips the swagger regen (platform-invariant; a caller
# building several platforms in a row need only pay for it once).
set -euo pipefail

PLATFORM="${1:?usage: build-any.sh <platform> <outdir>}"
OUTDIR="${2:?usage: build-any.sh <platform> <outdir>}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Split the optional variant suffix off FIRST, so it composes with `host`
# too (`host-sandbox` is the natural local invocation on a Mac). A variant
# never duplicates its platform's row — see the build step for what
# `sandbox` does.
case "$PLATFORM" in
*-sandbox) VARIANT=sandbox ;;
*) VARIANT="" ;;
esac
BASE="${PLATFORM%-sandbox}"

if [ "$BASE" = host ]; then
    case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) BASE=darwin-arm64 ;;
    Darwin-x86_64) BASE=darwin-x64 ;;
    Linux-x86_64) BASE=linux-x86_64 ;;
    *)
        echo "build-any: cannot detect host; pass an explicit platform" >&2
        exit 1
        ;;
    esac
    PLATFORM="$BASE${VARIANT:+-$VARIANT}"
fi

# platform → GOOS GOARCH llama.cpp-token exe-suffix os-label arch-label
# llama.cpp tokens are the GPU-capable archives (Metal on macOS arm64,
# Vulkan+CPU-fallback on Linux/Windows) — see fetch-llamacpp.sh.
case "$BASE" in
darwin-arm64) GOOS=darwin GOARCH=arm64 LLAMA=macos-arm64 EXE="" OS=darwin ARCH=arm64 ;;
darwin-x64) GOOS=darwin GOARCH=amd64 LLAMA=macos-x64 EXE="" OS=darwin ARCH=x86_64 ;;
linux-x86_64) GOOS=linux GOARCH=amd64 LLAMA=ubuntu-vulkan-x64 EXE="" OS=linux ARCH=x86_64 ;;
windows-x86_64) GOOS=windows GOARCH=amd64 LLAMA=win-vulkan-x64 EXE=".exe" OS=windows ARCH=x86_64 ;;
*)
    echo "build-any: unknown platform '$PLATFORM'" >&2
    exit 1
    ;;
esac

LLAMACPP_VERSION="$(scripts/llamacpp-version.sh)"
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

# Swagger docs are a build input; generate once via the Makefile target so
# ONE command owns the spec flags (--v3.1 — a divergent regen here would
# embed a Swagger 2.0 spec while the committed, drift-gated spec is OpenAPI
# 3.1). The output is platform-invariant and takes ~6s, so a caller looping
# over platforms sets ANY_SKIP_SWAG=1 after the first.
if [ "${ANY_SKIP_SWAG:-}" = 1 ]; then
    echo "build-any: skipping swagger regen (ANY_SKIP_SWAG=1)"
else
    make swagger >/dev/null
fi

LDFLAGS="-s -w -X $PKG/internal/version.Version=$VERSION -X $PKG/internal/version.Commit=$COMMIT -X $PKG/internal/version.BuildDate=$DATE"

# `llamacpp` compiles in the local embedder (docs/13-index.md § Builds and
# the local embedder). It stays pure Go — the llama.cpp libs are loaded at
# runtime via purego from the llamacpp/ dir staged below — so CGO_ENABLED=0
# cross-builds keep working. With it, the default `auto` embedder falls
# back to the local model, downloading its GGUF (~639 MB) into the data dir
# on first use.
TAGS=llamacpp

# The sandbox variant: same legs, different libffi. Without these two flags
# jupiterrider/ffi (reached via yzma) extracts an unsigned libffi.8.dylib into
# the user Caches dir at package init and dlopens it, which macOS library
# validation denies — the process dies before main. FFI_NO_EMBED=1 at runtime
# is NOT equivalent: it leaves the bare "libffi.8.dylib" name, which macOS
# does not ship. Full story: docs/18-ci.md § The darwin `-sandbox` variants.
if [ "$VARIANT" = sandbox ]; then
    [ "$GOOS" = darwin ] || {
        echo "build-any: the sandbox variant is darwin-only (got '$PLATFORM')" >&2
        exit 1
    }
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
# Fetch into a per-token dir that OUTLIVES this build so fetch-llamacpp.sh's
# VERSION short-circuit fires across builds (a CI run builds 6 platforms; the
# sandbox variants share their base platform's payload byte-for-byte), then
# copy into the stage. -a keeps symlinks; llamacpp/VERSION ships in the
# tarball either way.
LIBDIR="third_party/llamacpp/staged/$LLAMA"
scripts/fetch-llamacpp.sh "$LLAMACPP_VERSION" "$LIBDIR" "$LLAMA"
mkdir -p "$STAGE/llamacpp"
cp -a "$LIBDIR/." "$STAGE/llamacpp/"


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
