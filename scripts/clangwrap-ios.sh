#!/bin/sh
# clangwrap-ios.sh — CC wrapper for cgo cross-compiling to ios/arm64.
# Used by SPIKE 0.2/0.3 (IOS-6169) to build the libany c-archive.
# Sets the iphoneos sysroot + ios deployment target on every cgo clang
# invocation. SDK path is resolved at runtime (no hardcoded paths).
SDK=$(xcrun --sdk iphoneos --show-sdk-path)
exec xcrun --sdk iphoneos clang \
  -isysroot "$SDK" \
  -target arm64-apple-ios26.0 \
  "$@"
