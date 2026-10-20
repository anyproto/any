#!/bin/sh
# clangwrap-ios.sh — CC wrapper for cgo cross-compiling to ios/arm64 (device).
# Used to build the anylib c-archive.
# Sets the iphoneos sysroot + ios deployment target on every cgo clang
# invocation. SDK path is resolved at runtime (no hardcoded paths).
#
# DRIFT POINT: the minimum iOS version below must be at or below the lowest
# deployment target of the iOS client that links this library. The client's
# internal builds target iOS 18.4. ANYSERVER_IOS_MIN_VERSION overrides the
# default. Keep the default the same in clangwrap-iossim.sh.
MIN_VERSION="${ANYSERVER_IOS_MIN_VERSION:-18.4}"
SDK=$(xcrun --sdk iphoneos --show-sdk-path)
exec xcrun --sdk iphoneos clang \
  -isysroot "$SDK" \
  -target "arm64-apple-ios${MIN_VERSION}" \
  "$@"
