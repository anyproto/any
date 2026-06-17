#!/usr/bin/env bash
# Fetch prebuilt llama.cpp shared libraries for a target platform.
#
# Usage: scripts/fetch-llamacpp.sh <llama.cpp release tag> <dest dir> [platform]
#   platform ∈ macos-arm64 | macos-x64 | ubuntu-x64 | win-cpu-x64
#   default: host-detected (so `make llamacpp` keeps working with no 3rd arg).
#
# Release archives are cached under third_party/llamacpp/cache/ so repeated
# runs (and a warm CI cache) need no network. The dest dir gets the shared
# libs the local embedder dlopens (yzma), the upstream LICENSE, and a VERSION
# stamp; a bumped pin/platform replaces stale libs on the next run.
set -euo pipefail

VER="${1:?usage: fetch-llamacpp.sh <version> <dest> [platform]}"
DEST="${2:?usage: fetch-llamacpp.sh <version> <dest> [platform]}"
PLAT="${3:-}"
CACHE="third_party/llamacpp/cache"

if [ -z "$PLAT" ]; then
    case "$(uname -s)-$(uname -m)" in
    Darwin-arm64) PLAT="macos-arm64" ;;
    Darwin-x86_64) PLAT="macos-x64" ;;
    Linux-x86_64) PLAT="ubuntu-x64" ;;
    *)
        echo "fetch-llamacpp: cannot host-detect $(uname -s)-$(uname -m); pass an explicit platform" >&2
        exit 1
        ;;
    esac
fi

case "$PLAT" in
macos-arm64 | macos-x64 | ubuntu-x64) EXT="tar.gz" ;;
win-cpu-x64) EXT="zip" ;;
*)
    echo "fetch-llamacpp: unknown platform '$PLAT'" >&2
    exit 1
    ;;
esac

STAMP="$VER-$PLAT"
if [ -f "$DEST/VERSION" ] && [ "$(cat "$DEST/VERSION")" = "$STAMP" ]; then
    echo "fetch-llamacpp: $DEST already at $STAMP"
    exit 0
fi

ARCHIVE="llama-$VER-bin-$PLAT.$EXT"
URL="https://github.com/ggml-org/llama.cpp/releases/download/$VER/$ARCHIVE"

mkdir -p "$CACHE"
if [ ! -f "$CACHE/$ARCHIVE" ]; then
    echo "fetch-llamacpp: downloading $URL"
    curl -fL --retry 3 -o "$CACHE/$ARCHIVE.part" "$URL"
    mv "$CACHE/$ARCHIVE.part" "$CACHE/$ARCHIVE"
else
    echo "fetch-llamacpp: using cached $CACHE/$ARCHIVE"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
case "$EXT" in
tar.gz) tar -xzf "$CACHE/$ARCHIVE" -C "$TMP" ;;
zip) unzip -q "$CACHE/$ARCHIVE" -d "$TMP" ;;
esac

rm -rf "$DEST"
mkdir -p "$DEST"
# Prune tool/multimodal libs the embedder never loads, then take every shared
# lib wherever it sits (release layout varies: lib/ subdir vs flat build/bin).
find "$TMP" \( -name '*-impl*' -o -name '*mtmd*' \) -delete
find "$TMP" \( -name '*.so*' -o -name '*.dylib' -o -name '*.dll' \) -exec cp -P {} "$DEST/" \;
LICENSE_FILE="$(find "$TMP" -name 'LICENSE*' | head -1 || true)"
[ -n "$LICENSE_FILE" ] && cp "$LICENSE_FILE" "$DEST/LICENSE"
echo "$STAMP" >"$DEST/VERSION"

echo "fetch-llamacpp: $(find "$DEST" \( -name 'lib*' -o -name '*.dll' \) | wc -l | tr -d ' ') libs in $DEST ($STAMP)"
