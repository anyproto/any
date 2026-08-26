#!/usr/bin/env bash
# Build the `any` engine into an iOS xcframework (AnyLib) and zip it.
#
# Usage: scripts/build-xcframework.sh <outdir>
#   Produces: <outdir>/any.xcframework.zip
#
# Mirrors scripts/build-any.sh: reads ANY_BUILD_VERSION (the workflow passes the
# resolved release version — a real tag, or a v<base>-nightly.<date>.<n>
# prerelease — so the embedded version matches the release; standalone runs
# fall back to git describe), takes the output dir as an arg, set -euo pipefail.
#
# Requires macOS with Xcode providing the iOS SDK (the caller verifies the SDK
# version; this script only resolves the SDK path at runtime via xcrun). The
# c-archive build is cgo, so the two clangwrap-ios{,sim}.sh CC wrappers
# disambiguate the device vs simulator slice (golang/go#57442).
#
# App consumption: the Swift app fetches the published GitHub Release asset via
# tools/fetch-anyserver-xcframework.sh — bump ANYSERVER_VERSION + ANYSERVER_SHA256
# there to adopt a new release. (SwiftPM consumption was removed: a
# binaryTarget(url:) cannot download from a PRIVATE GitHub Release, so there is
# no Package.swift / url+checksum pin. To restore SPM if this repo ever goes
# public, see git history.)
set -euo pipefail

OUTDIR="${1:?usage: build-xcframework.sh <outdir>}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${ANY_BUILD_VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
PKG="github.com/anyproto/any"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

echo "build-xcframework: any $VERSION (commit $COMMIT)"

LDFLAGS="-s -w -X $PKG/internal/version.Version=$VERSION -X $PKG/internal/version.Commit=$COMMIT -X $PKG/internal/version.BuildDate=$DATE"

# Clean the per-slice staging dir so stale slices from a prior local run can't
# leak into the assembled xcframework (CI runners start fresh; this matters only
# for repeated local runs). The assembled any.xcframework is removed below.
rm -rf build

# Build the device + simulator c-archive slices. Both slices use the identical
# go build — only the cgo CC wrapper differs (device sysroot/triple vs the
# simulator's -simulator triple, golang/go#57442). One function keeps the build
# flags in a single place so the two slices can't drift apart.
#
# Tags `mobile fts`: `mobile` selects the iOS embedded build (pidlock no-op,
# swaggo-free openapi stub, vector/embedding off); `fts` turns on the BM25
# full-text leg (capFTS=true). Building without `fts` would ship an
# FTS-disabled archive, contradicting the Phase B caps contract. `-trimpath`
# for path-leak parity with build-any.sh.
#
# cgo names the generated header after the -o argument, not after the package
# path, so `-o anylib.a` is what makes the header `anylib.h` — the name
# mobile/ios/module.modulemap points `module AnyLib` at. Renaming -o renames
# the header the Swift `import AnyLib` resolves through.
build_slice() {
    name="$1"
    cc="$2"
    echo "== building $name slice (CC=$cc) =="
    mkdir -p "build/$name/headers"
    CGO_ENABLED=1 GOOS=ios GOARCH=arm64 CC="$ROOT/scripts/$cc" \
        go build -trimpath -tags 'mobile fts' -buildmode=c-archive -ldflags "$LDFLAGS" \
        -o "build/$name/anylib.a" ./mobile/ios
    cp "build/$name/anylib.h" "build/$name/headers/anylib.h"
    cp mobile/ios/module.modulemap "build/$name/headers/module.modulemap"
}

build_slice device clangwrap-ios.sh
build_slice sim clangwrap-iossim.sh

echo "== assembling any.xcframework =="
rm -rf any.xcframework
xcodebuild -create-xcframework \
    -library build/device/anylib.a -headers build/device/headers \
    -library build/sim/anylib.a -headers build/sim/headers \
    -output any.xcframework
# Keep the per-slice Info.plist (do not strip — spike 0.5).
cat any.xcframework/Info.plist

echo "== zipping → $OUTDIR/any.xcframework.zip =="
mkdir -p "$OUTDIR"
ditto -c -k --keepParent any.xcframework "$OUTDIR/any.xcframework.zip"
echo "build-xcframework: → $OUTDIR/any.xcframework.zip"
