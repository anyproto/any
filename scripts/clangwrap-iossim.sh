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
# DRIFT POINT: the minimum iOS version in the triple must be at or below the
# lowest deployment target of the iOS client that links this library. The
# client's internal builds target iOS 18.4. ANYSERVER_IOS_MIN_VERSION
# overrides the default. Keep the default the same in clangwrap-ios.sh.
MIN_VERSION="${ANYSERVER_IOS_MIN_VERSION:-18.4}"
SDK=$(xcrun --sdk iphonesimulator --show-sdk-path)
exec xcrun --sdk iphonesimulator clang \
  -isysroot "$SDK" \
  -target "arm64-apple-ios${MIN_VERSION}-simulator" \
  "$@"
