#!/bin/sh
# Cross-compile eg2bench for Android arm64 against the LiteRT-LM C API (litert_lm_c_api-1.0.0.zip, unzipped to $CAPI).
: ${NDK:=$HOME/Library/Android/sdk/ndk/29.0.14206865} ${CAPI:?unzipped litert_lm_c_api dir}
CC=$NDK/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android28-clang
$CC -O2 -Wall -o eg2bench "$(dirname "$0")/eg2bench.c" -I"$CAPI/include" -ldl
