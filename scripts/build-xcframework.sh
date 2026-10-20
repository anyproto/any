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

# The minimum iOS version of every slice. It must be at or below the lowest
# deployment target of the iOS client that links this library. It goes in
# CGO_CFLAGS and CGO_LDFLAGS, not in the CC wrappers: the Go build cache keys
# a cgo compile on those flags, but not on the wrapper contents. A version in
# a wrapper lets a cached runtime/cgo object keep the old minimum.
IOS_MIN_VERSION="${ANYSERVER_IOS_MIN_VERSION:-18.4}"

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
# Tag `mobile` selects the iOS embedded build (pidlock no-op, swaggo-free
# openapi stub). The local embedder never builds for GOOS=ios, whatever the
# tags. `-trimpath` for path-leak parity with build-any.sh.
#
# cgo names the generated header after the -o argument, not after the package
# path, so `-o anylib.a` is what makes the header `anylib.h` — the name
# mobile/ios/module.modulemap points `module AnyLib` at. Renaming -o renames
# the header the Swift `import AnyLib` resolves through.
build_slice() {
    name="$1"
    cc="$2"
    minflag="$3=$IOS_MIN_VERSION"
    echo "== building $name slice (CC=$cc, $minflag) =="
    mkdir -p "build/$name/headers"
    CGO_ENABLED=1 GOOS=ios GOARCH=arm64 CC="$ROOT/scripts/$cc" \
        CGO_CFLAGS="-O2 -g $minflag" CGO_LDFLAGS="-O2 -g $minflag" \
        go build -trimpath -tags mobile -buildmode=c-archive -ldflags "$LDFLAGS" \
        -o "build/$name/anylib.a" ./mobile/ios
    cp "build/$name/anylib.h" "build/$name/headers/anylib.h"
    cp mobile/ios/module.modulemap "build/$name/headers/module.modulemap"
}

build_slice device clangwrap-ios.sh -miphoneos-version-min
build_slice sim clangwrap-iossim.sh -mios-simulator-version-min

# Fail when any archive member has a different minimum iOS version, for
# example a stale object from the build cache.
for name in device sim; do
    bad="$(otool -l "build/$name/anylib.a" | awk -v v="$IOS_MIN_VERSION" '$1 == "minos" && $2 != v')"
    if [ -n "$bad" ]; then
        echo "build-xcframework: $name slice has members with a minos other than $IOS_MIN_VERSION:" >&2
        echo "$bad" | sort | uniq -c >&2
        exit 1
    fi
    echo "== $name slice: every member has minos $IOS_MIN_VERSION =="
done

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
