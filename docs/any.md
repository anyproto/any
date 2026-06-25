# any — the embeddable `any` backend artifact

`any` is the versioned, per-platform artifact the **Any** desktop app
(`anyproto/any-ui`) embeds. CI builds it on tag (release) + nightly
(prerelease); the publish fires a `repository_dispatch` that triggers the
`any-ui` build. Build it locally with `make any` (or `scripts/build-any.sh`).

## Tarball layout (`any-<version>-<os>-<arch>.tar.gz`)

- `any[.exe]` — the backend (built from `cmd/any`). The binary is `any`;
  `any` is the _artifact_ name.
- `bobrik-watch[.exe]` — the bao agent (`cmd/bobrik-watch`).
- `llamacpp/` — prebuilt llama.cpp shared libs for this `(os,arch)`
  (`fetch-llamacpp.sh`). `internal/indexer/embed_local.go` looks for them at
  `<dir-of-any-exe>/llamacpp` by default, or `$YZMA_LIB`.
- `agent/` — bobrik's read-from-disk asset tree (`anyHelper.js`, `programs/`,
  `skills/`, `tool-descriptions/`). Pass `bobrik-watch --programs-dir
  <…>/agent/programs`.
- `manifest.json` — `{ version, os, arch, llamacpp_version, sha256: {path: hash} }`.
  Consumers verify every file against `sha256` before use.

The default embedding model (`Qwen3-Embedding-0.6B-Q8_0.gguf`) is **not**
bundled — it downloads at runtime into `<data-dir>/index/models/`.

## Platforms

Currently published: `darwin-arm64`, `darwin-x64`, `linux-x86_64`.
`windows-x86_64` is temporarily withheld from CI releases until
`bobrik-watch` replaces its Unix signal-based refresh controls with a
cross-platform control channel. The backend is CGO-free (purego `dlopen`), so
one Linux job cross-builds every published target and cross-fetches each
platform's libs.

## Required CI secret

`ANY_UI_DISPATCH_TOKEN` — a fine-grained PAT (or GitHub App token) with
permission to send repository dispatches to `anyproto/any-ui`, used only to
`POST /repos/anyproto/any-ui/dispatches`. Without it, builds publish but never
trigger the desktop build. Store it in this repo's Actions secrets
(`gh secret set ANY_UI_DISPATCH_TOKEN --repo anyproto/any`). The built-in
`GITHUB_TOKEN` cannot dispatch across repos, hence a dedicated token.
