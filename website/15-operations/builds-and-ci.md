---
title: Builds and CI
description: Building the server with or without the local embedder, the nix dev shell, and the release artifacts — desktop tarballs, the Android .aar and the iOS .xcframework.
order: 60
---
# Builds and CI

The server is a CGO-free Go binary. Full-text and vector search compile into every build; one build tag, `llamacpp`, adds the local embedder, which loads llama.cpp shared libraries at runtime. `make build` passes the tag and fetches the libraries; a plain `go build` or `go install` gives a server that embeds only through an online provider named by `index.openai.*`. Everything here runs in the `any` repository; the runtime's build order is under [Embedding anyrt](../agents/embedding-anyrt.html#build-order).

## Building

```bash
make build            # bin/any with -tags llamacpp
make llamacpp         # prebuilt llama.cpp libs into bin/llamacpp (also part of make build)
make test             # go test ./..., then the local-embedder packages with -tags llamacpp
make vet
make check-deps       # the untagged and mobile builds must not link libffi
make swagger          # regenerate the OpenAPI spec (make build runs it too)
make catalog-validate # check the embedded usecase catalog (FILES=… adds candidate files)
```

`make build` regenerates the spec, passes the `llamacpp` tag, writes `bin/any` and fetches the pinned llama.cpp release into `bin/llamacpp/` — a fetch failure there only warns, so an offline build still succeeds without the local embedder.

> **Note.** On Linux the local embedder's bindings need a loadable system `libffi.so.8` at startup. On NixOS run both the build and the binary through the repo's dev shell — `nix develop -c make build`, `nix develop -c bin/any run` — which puts libffi and the C++ runtime on the library path. Never hand-wire `LD_LIBRARY_PATH`.

## The local embedder

Search needs no build tag. The `llamacpp` tag adds the local llama.cpp embedder; `index.enabled` and `index.embedder` decide what runs.

| Build | Tags | Local embedder |
|---|---|---|
| desktop / server (`make build`), release tarballs | `llamacpp` | yes |
| darwin `-sandbox` tarball | `llamacpp ffi_no_embed` | yes |
| `go install` / `go build` | none | no |
| mobile — `.aar` / `.xcframework` | `gomobile` / `mobile` | no |

- Without the local embedder, `index.embedder: auto` embeds through an online primary alone once `index.openai.apiKey` names one — no child process, no model download — and is full-text only without a key. `index.embedder: local` fails at boot with an error naming the tag.
- The tag is opt-in because the embedder's bindings load libffi at process start and crash without it. `make check-deps`, a PR check, fails if the untagged or a mobile build links them.
- Mobile never builds it: the local-embedder files exclude `GOOS` android and ios, whatever the tags.
- `ffi_no_embed` is a packaging flag, not a capability one: it changes where libffi comes from (the system `/usr/lib/libffi.dylib` instead of a copy extracted into the user Caches directory), which is what macOS library validation requires. Nothing is compiled out.

## Release artifacts

One reusable workflow builds every platform on tag (release) and nightly (prerelease), then a single publish job ships them all in one GitHub Release and notifies the client repositories.

### Desktop tarballs

`any-<version>-<os>-<arch>[-sandbox].tar.gz` for `darwin-arm64`, `darwin-x64`, `linux-x86_64`, `windows-x86_64`, plus `darwin-arm64-sandbox` and `darwin-x64-sandbox`:

```
any[.exe]         the server, built with -tags llamacpp
llamacpp/         prebuilt llama.cpp shared libs for this (os, arch)
manifest.json     { version, os, arch, llamacpp_version, sha256: {path: hash} }
```

Consumers verify every file against `sha256` before use. The embedding model is **not** bundled — it downloads at runtime into the data root. Because the backend is CGO-free, one Linux job cross-builds every target and cross-fetches each platform's libraries.

### The darwin `-sandbox` variants

For hosts that run `any` as an App-Sandboxed or hardened-runtime helper without the disable-library-validation entitlement. The payload is identical to the plain darwin tarball; the only difference is the libffi source (`ffi_no_embed` plus a linker flag pinning `/usr/lib/libffi.dylib`), so the binary never writes an unsigned dylib to Caches. `manifest.json` gains `"variant": "sandbox"`. A post-build guard fails the build unless the binary lost the Caches path *and* gained the `/usr/lib` one, and a smoke job runs both sandbox binaries on a macOS runner (arm64 natively, x64 under Rosetta 2) asserting they start without creating the extraction directory. The plain tarballs are the desktop app's sidecar; the app signs them itself.

### Mobile

| Artifact | Build | Notes |
|---|---|---|
| `any.aar` | `make build-android` — gomobile bind, `arm64-v8a`, tag `gomobile` | needs an Android NDK; version-stamped via ldflags with `VERSION= COMMIT= DATE=` passed as make variables |
| `any.xcframework.zip` | `scripts/build-xcframework.sh` — device and simulator c-archive slices (`go build -buildmode=c-archive`), tag `mobile` | needs macOS with Xcode's iOS SDK; the header is `anylib.h`, the Swift module `AnyLib` |

Both are sha256-pinned in the release notes. On the embedded path the index runs full-text only — it is not a host parameter — `index.embedder` is forced to `none`, and the `/ui` harness is off.

## CI

- **Build workflow** — fans out per-platform jobs (six desktop tarballs, `.aar`, `.xcframework`), the macOS smoke job, then fans in to `publish`, which creates the release and fires a `repository_dispatch` to the desktop, iOS and Android client repositories. The desktop job runs `make catalog-validate` before building, and `publish` depends on it, so a broken catalog cannot ship. The dispatch is best-effort: a failure warns but never unpublishes.
- **PR checks** — `make test`, `go vet` over the module, `make check-deps`, `make catalog-validate`, and **swagger drift**: the OpenAPI spec is regenerated and the PR fails if the committed spec differs. The spec is a published contract (the runtime's drift check pins against it), so a handler change must come with a regenerated spec. The spec captures routes, shapes and status codes, not `error.code` strings.
- **Windows** — every release artifact is cross-compiled on Linux, so a separate workflow builds the tree and runs the server and config unit tests on a Windows runner on every push to `main` and daily before the nightly publishes.
- **Docs** — `make docs-check` runs on every PR. It builds the site, runs renderer vet/tests, checks internal links and search targets, and tests the downloadable JavaScript and Python clients with mocked responses and streams.
- **Secret** — one classic PAT with read/write across the organization, used to fetch the private SDK module in every job and to dispatch to the client repos. The built-in token can do neither.

## Versions

The server's Go dependencies — the SDK, any-sync and the storage engine — are published modules pinned in `go.mod`, the single source of truth for their versions. The agent runtime is not among them: anyrt is a separate Rust crate that the desktop app builds against. `any version` prints the binary's version alongside the running server's.
