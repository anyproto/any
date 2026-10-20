#!/bin/sh
# clangwrap-ios.sh — CC wrapper for cgo cross-compiling to ios/arm64 (device).
# Used to build the anylib c-archive.
# Sets the iphoneos sysroot and the device triple on every cgo clang
# invocation. SDK path is resolved at runtime (no hardcoded paths).
#
# The triple has no version. build-xcframework.sh sets the minimum iOS
# version in CGO_CFLAGS and CGO_LDFLAGS, because the Go build cache hashes
# those flags but not the contents of this script.
SDK=$(xcrun --sdk iphoneos --show-sdk-path)
exec xcrun --sdk iphoneos clang \
  -isysroot "$SDK" \
  -target arm64-apple-ios \
  "$@"
