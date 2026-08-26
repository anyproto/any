#!/bin/sh
# clangwrap-iossim.sh — CC wrapper for cgo cross-compiling to the
# ios/arm64 *simulator*. Used to build the anylib c-archive.
#
# Device and arm64-simulator share GOOS=ios GOARCH=arm64, so cgo's own
# flags can't tell them apart (golang/go#57442). ONLY this wrapper
# disambiguates: it points clang at the iphonesimulator SDK and sets the
# explicit `-simulator` triple so the emitted Mach-O carries the
# IOSSIMULATOR platform load command, not IOS. Without it the object is
# tagged as a device binary and the linker rejects it for the simulator.
# SDK path is resolved at runtime (no hardcoded paths).
#
# DRIFT POINT: the minimum iOS version in the triple must match the Xcode
# project's deployment target (root CLAUDE.md: 26.0 today). It is overridable
# via ANYSERVER_IOS_MIN_VERSION so a build harness can pass the project
# setting instead of editing this script; bump the default when the target
# moves.
MIN_VERSION="${ANYSERVER_IOS_MIN_VERSION:-26.0}"
SDK=$(xcrun --sdk iphonesimulator --show-sdk-path)
exec xcrun --sdk iphonesimulator clang \
  -isysroot "$SDK" \
  -target "arm64-apple-ios${MIN_VERSION}-simulator" \
  "$@"
