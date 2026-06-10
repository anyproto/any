#!/usr/bin/env bash
# Fetch prebuilt llama.cpp shared libraries for the host platform.
#
# Usage: scripts/fetch-llamacpp.sh <llama.cpp release tag> <dest dir>
# e.g.:  scripts/fetch-llamacpp.sh b9590 bin/llamacpp
#
# Release tarballs are cached under third_party/llamacpp/cache/ so
# repeated runs (and `make llamacpp` in CI with a warm cache) need no
# network. The dest dir gets the shared libs the local embedder needs
# (yzma dlopens them at runtime), the upstream MIT LICENSE, and a
# VERSION stamp — a bumped pin replaces stale libs on the next run.
set -euo pipefail

VER="${1:?usage: fetch-llamacpp.sh <version> <dest>}"
DEST="${2:?usage: fetch-llamacpp.sh <version> <dest>}"
CACHE="third_party/llamacpp/cache"

case "$(uname -s)-$(uname -m)" in
Darwin-arm64) PLAT="macos-arm64" ;;
Linux-x86_64) PLAT="ubuntu-x64" ;;
*)
    echo "fetch-llamacpp: unsupported platform $(uname -s)-$(uname -m) (want darwin-arm64 or linux-amd64)" >&2
    exit 1
    ;;
esac

if [ -f "$DEST/VERSION" ] && [ "$(cat "$DEST/VERSION")" = "$VER-$PLAT" ]; then
    echo "fetch-llamacpp: $DEST already at $VER ($PLAT)"
    exit 0
fi

TARBALL="llama-$VER-bin-$PLAT.tar.gz"
URL="https://github.com/ggml-org/llama.cpp/releases/download/$VER/$TARBALL"

mkdir -p "$CACHE"
if [ ! -f "$CACHE/$TARBALL" ]; then
    echo "fetch-llamacpp: downloading $URL"
    curl -fL --retry 3 -o "$CACHE/$TARBALL.part" "$URL"
    mv "$CACHE/$TARBALL.part" "$CACHE/$TARBALL"
else
    echo "fetch-llamacpp: using cached $CACHE/$TARBALL"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
tar -xzf "$CACHE/$TARBALL" -C "$TMP"

rm -rf "$DEST"
mkdir -p "$DEST"
# Release layout varies (lib/ subdir vs flat build/bin); take every
# shared lib wherever it sits. Prune tool/multimodal libs the embedder
# never loads.
find "$TMP" \( -name 'libllama-*-impl*' -o -name 'libmtmd*' \) -delete
find "$TMP" \( -name '*.so*' -o -name '*.dylib' \) -exec cp -P {} "$DEST/" \;
LICENSE_FILE="$(find "$TMP" -name 'LICENSE*' | head -1 || true)"
[ -n "$LICENSE_FILE" ] && cp "$LICENSE_FILE" "$DEST/LICENSE"
echo "$VER-$PLAT" >"$DEST/VERSION"

echo "fetch-llamacpp: $(find "$DEST" -name 'lib*' | wc -l | tr -d ' ') libs in $DEST ($VER, $PLAT)"
