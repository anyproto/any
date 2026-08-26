#!/bin/sh
# clangwrap-ios.sh — CC wrapper for cgo cross-compiling to ios/arm64 (device).
# Used to build the anylib c-archive.
# Sets the iphoneos sysroot + ios deployment target on every cgo clang
# invocation. SDK path is resolved at runtime (no hardcoded paths).
#
# DRIFT POINT: the minimum iOS version below must match the Xcode project's
# deployment target (root CLAUDE.md: 26.0 today). It is overridable via
# ANYSERVER_IOS_MIN_VERSION so a build harness can pass the project setting
# instead of editing this script; bump the default when the target moves.
MIN_VERSION="${ANYSERVER_IOS_MIN_VERSION:-26.0}"
SDK=$(xcrun --sdk iphoneos --show-sdk-path)
exec xcrun --sdk iphoneos clang \
  -isysroot "$SDK" \
  -target "arm64-apple-ios${MIN_VERSION}" \
  "$@"
