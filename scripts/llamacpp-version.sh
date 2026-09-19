#!/usr/bin/env bash
# Print the llama.cpp release the local embedder pins
# (internal/indexer/llamacpp_release.go). The Makefile, build-any.sh and CI
# read the pin through here; an unreadable pin fails instead of printing
# nothing. The pattern tolerates a CRLF checkout.
set -euo pipefail
cd "$(dirname "$0")/.."
v="$(sed -n 's/^const llamaCppRelease = "\([^"]*\)".*/\1/p' internal/indexer/llamacpp_release.go)"
if [ -z "$v" ]; then
    echo "llamacpp-version: no llamaCppRelease const in internal/indexer/llamacpp_release.go" >&2
    exit 1
fi
printf '%s\n' "$v"
