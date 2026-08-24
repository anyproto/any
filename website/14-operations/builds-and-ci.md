---
title: Builds and CI
description: Building the server with the search legs compiled in, the nix dev shell, and the release artifacts — desktop tarballs, the Android .aar and the iOS .xcframework.
order: 60
---
# Builds and CI

The server is a CGO-free Go binary, but the search index legs are selected at compile time by build tags, and the local embedder loads shared libraries at runtime. `make build` handles all of it; a bare `go build` produces a server whose `/search` silently returns nothing.

## Building

```bash
make build            # any (+ companion binaries) with -tags 'fts vector'
make llamacpp         # prebuilt llama.cpp libs into bin/llamacpp (also part of make build)
make test             # go test -tags 'fts vector' ./...
make vet
make swagger          # regenerate the OpenAPI spec (make build runs it too)
```

`make build` passes the `fts vector` tags and fetches the pinned llama.cpp release into `bin/llamacpp/` — a fetch failure there only warns, so an offline build still succeeds without the local embedder.

> **Note.** On Linux the local embedder's bindings need a loadable system `libffi.so.8` at startup. On NixOS run both the build and the binary through the repo's dev shell — `nix develop -c make build`, `nix develop -c ./any run` — which puts libffi and the C++ runtime on the library path. Never hand-wire `LD_LIBRARY_PATH`.

## Build tags

The two search legs are independent, positive tags; a build opts each in.

| Build | Tags | FTS | Vector |
|---|---|---|---|
| desktop / server (`make build`) | `fts vector` | on | on |
| darwin `-sandbox` tarball | `fts vector ffi_no_embed` | on | on (full, incl. local embedder) |
| FTS-only | `fts` | on | off |
| plain `go build` | none | off | off |
| mobile (gomobile) | `fts` | on | **off, forced** |

- `vector` is always off on mobile regardless of tags: the local embedder's bindings resolve a libffi symbol at package load that Android does not provide, and a runtime toggle cannot prevent a load-time crash. No embedder is constructed and no model is downloaded there.
- `fts` works everywhere, including mobile — a few KB of binary.
- `ffi_no_embed` is a packaging flag, not a capability one: it changes where libffi comes from (the system `/usr/lib/libffi.dylib` instead of a copy extracted into the user Caches directory), which is what macOS library validation requires. Nothing is compiled out.

Tags decide what is *compiled*; `index.enabled` and `index.embedder` decide what *runs*. A server built with neither leg but `index.enabled` logs a warning at startup so the empty-result state is observable.

## Release artifacts

One reusable workflow builds every platform on tag (release) and nightly (prerelease), then a single publish job ships them all in one GitHub Release and notifies the client repositories.

### Desktop tarballs

`any-<version>-<os>-<arch>[-sandbox].tar.gz` for `darwin-arm64`, `darwin-x64`, `linux-x86_64`, `windows-x86_64`, plus `darwin-arm64-sandbox` and `darwin-x64-sandbox`:

```
any[.exe]         the server, built with -tags fts,vector
llamacpp/         prebuilt llama.cpp shared libs for this (os, arch)
manifest.json     { version, os, arch, llamacpp_version, sha256: {path: hash} }
```

Consumers verify every file against `sha256` before use. The embedding model is **not** bundled — it downloads at runtime into the data root. Because the backend is CGO-free, one Linux job cross-builds every target and cross-fetches each platform's libraries.

### The darwin `-sandbox` variants

For hosts that run `any` as an App-Sandboxed or hardened-runtime helper without the disable-library-validation entitlement. The payload is identical to the plain darwin tarball; the only difference is the libffi source (`ffi_no_embed` plus a linker flag pinning `/usr/lib/libffi.dylib`), so the binary never writes an unsigned dylib to Caches. `manifest.json` gains `"variant": "sandbox"`. A post-build guard fails the build unless the binary lost the Caches path *and* gained the `/usr/lib` one, and a smoke job runs both sandbox binaries on a macOS runner (arm64 natively, x64 under Rosetta 2) asserting they start without creating the extraction directory. The plain tarballs are the desktop app's sidecar; the app signs them itself.

### Mobile

| Artifact | Build | Notes |
|---|---|---|
| `any.aar` | `make build-android` — gomobile bind, `arm64-v8a`, tags `gomobile fts` | needs an Android NDK; version-stamped via ldflags with `VERSION= COMMIT= DATE=` passed as make variables |
| `any.xcframework.zip` | gomobile bind, tags `mobile fts` | the iOS share extension can keep the indexer dormant per engine instance |

Both are sha256-pinned in the release notes. On the embedded path the host drives `index.enabled` as a start argument, and `index.embedder` is forced to `none`.

## CI

- **Build workflow** — fans out per-platform jobs (six desktop tarballs, `.aar`, `.xcframework`), the macOS smoke job, then fans in to `publish`, which creates the release and fires a `repository_dispatch` to the desktop, iOS and Android client repositories. The dispatch is best-effort: a failure warns but never unpublishes.
- **PR checks** — `go vet` over the module, and **swagger drift**: the OpenAPI spec is regenerated and the PR fails if the committed spec differs. The spec is a published contract (the runtime's drift check pins against it), so a handler change must come with a regenerated spec. The spec captures routes, shapes and status codes, not `error.code` strings.
- **Secret** — one classic PAT with read/write across the organization, used to fetch the private SDK module in every job and to dispatch to the client repos. The built-in token can do neither.

## Versions

Dependencies (the SDK, any-sync, the storage engine, the agent runtime) are published Go modules pinned in `go.mod` — the single source of truth for versions. `any version` prints the binary's version alongside the running server's.
