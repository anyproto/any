# Unified all-platform build workflow (desktop + iOS + Android)

## Overview

Fold the three separate "build `any` for embedding in a client" paths into **one
reusable GitHub Actions workflow** that builds every platform and publishes a
**single GitHub Release** carrying all assets:

- 4 desktop tarballs (`any-<ver>-{darwin-arm64,darwin-x64,linux-x86_64,windows-x86_64}.tar.gz`)
- `any.xcframework.zip` (iOS, + sha256)
- `any.aar` (Android, + sha256)

Today there are two disconnected pipelines: the reusable `_build-any.yml`
(desktop only, called by `release-any.yml` on `v*` tags and `nightly-any.yml` on
cron) and a standalone `xcframework.yml` (iOS, manual dispatch with a bare-SemVer
`0.1.0`, self-tags + self-publishes). Android has only Makefile scaffolding
(`makefiles/android.mk`) and no CI at all.

After this change: one workflow, fan-out build jobs per runner, fan-in to one
publish job. iOS rides the `v*` tag (no self-tag, no separate version scheme).
Android is added as a first-class platform. The publish job notifies all three
client repos (any-ui, anytype-swift, anytype-kotlin2) via `repository_dispatch`.

**Problem it solves:** one source of truth for "what version of `any` shipped to
clients," atomic single-release publishing, per-platform failure visibility, and
Android coverage that didn't exist. Removes the iOS/desktop version-scheme split.

## Context (from discovery)

Files/components involved:
- `.github/workflows/_build-any.yml` — current single `build-and-publish` job (desktop x4 + publish + any-ui dispatch). The thing being restructured.
- `.github/workflows/xcframework.yml` — current standalone iOS build. Folded in + deleted.
- `.github/workflows/release-any.yml`, `nightly-any.yml` — callers of the reusable workflow. **Must keep working unchanged.**
- `scripts/build-any.sh` — desktop build (CGO-free cross-compile, all 4 platforms). Pattern to mirror for the iOS script. Unchanged.
- `scripts/clangwrap-ios.sh`, `scripts/clangwrap-iossim.sh` — CC wrappers the iOS c-archive build needs. Unchanged.
- `makefiles/android.mk` — current `build-android`/`setup-gomobile`/`install-dev-android`. Reworked.
- `mobile/mobile.go` (`//go:build android || gomobile`), `mobile/tools.go` (pins `x/mobile/bind`) — gomobile bind surface. `mobile/mobile_test.go` (`//go:build gomobile`) is a real test suite.
- `cmd/anyserver/anyserver.go`, `cmd/anyserver/module.modulemap` — iOS c-archive entry. `cmd/anyserver/anyserver_test.go` is a real host-build test suite. Unchanged.
- `internal/version/version.go` — exports `Version`/`Commit`/`BuildDate` vars (default `"dev"`/`"none"`/`"unknown"`), the `-ldflags -X` targets.
- `go.mod` — `go 1.26.2`; pins `golang.org/x/mobile v0.0.0-20260508232728-bebd421c7fa8`; already uses `tool github.com/swaggo/swag/cmd/swag` (precedent for go.mod tool pinning).

Patterns found:
- Build logic lives in scripts/makefiles; workflow YAML stays thin (`build-any.sh` for desktop). Mirror this for iOS (new `build-xcframework.sh`) and Android (existing `android.mk`).
- Private-module auth (`GOPRIVATE=github.com/anyproto/*` + `git config --global url.insteadOf`) is duplicated across both current workflows — extract to a composite action.
- Version resolution (release tag vs computed `v<base>-nightly.<YYYYMMDD>.<n>`) currently a step inside the desktop job — extract to its own job so all platforms stamp one string.

Dependencies identified:
- `ANY_CI_TOKEN` (classic PAT, read across anyproto + write this repo + dispatch to the 3 client repos) — already the reusable workflow's only secret.
- Ubuntu runner ships the Android NDK under `$ANDROID_HOME/ndk/` (heart pins `28.x`). macOS runner (`macos-15`) ships Xcode 26 / iOS 26 SDK.

## Reference: anytype-heart (previous project, separate repo)

`/Users/mordan/Projects/anytype-heart` — `makefiles/ci-compile-android-lib.mk` +
`.github/workflows/{nightly,build}.yml`. Adopt: all-4-ABI `-target=android`,
NDK `28.x` resolution, `-androidapi 26`. **Avoid:** `|| true` after `gomobile
bind` (swallows failures, ships stale/missing aar) and `gomobile@latest`
(version drift). Its Maven/GitHub-Packages publish is intentionally **not**
carried over — `any` uses a GitHub Release asset + sha (parallel to iOS).

## Development Approach

- **Testing approach: Regular** (infra change; validation-first, not TDD).
- This is CI/build infrastructure — **there are no unit tests for YAML
  workflows.** "Tests" in each task below means the validation that is actually
  meaningful for that artifact:
  - **Go source touched** (none expected beyond `mobile/tools.go` module-graph
    touch-up): `go vet ./...`, existing suites `go test ./cmd/anyserver/` and
    `go test -tags gomobile ./mobile/...`, `go build ./...`.
  - **Shell scripts:** `bash -n <script>` (syntax) + `shellcheck` if available +
    a real local run where the toolchain exists (the iOS script runs on the
    macOS dev box; the Android bind needs an NDK so its full run is CI-only).
  - **Workflow / composite-action YAML:** `actionlint` if available, else a
    careful structural review; the true integration test is a **CI dry-run**
    (push branch → `workflow_dispatch` the nightly → inspect the single release).
- Complete each task fully before the next. Keep this plan in sync if scope shifts.
- Maintain backward compatibility: the `workflow_call` interface
  (`tag` + `channel` inputs, `ANY_CI_TOKEN` secret) and the two caller workflows
  must not change.

## Testing Strategy

- **Per-task validation:** as above — syntax/lint + the relevant Go suite + local
  run when the toolchain is present.
- **No project e2e harness** applies to CI YAML. The integration equivalent is
  the CI dry-run in the verification task: trigger `nightly-any` manually on the
  feature branch and confirm one release appears with **all 6 assets** and the
  3 dispatches fire (or warn-skip cleanly if the token is scoped out).
- **Toolchain note:** `actionlint`/`shellcheck` are not installed locally
  (`which` returns nothing). Install them (`brew install actionlint shellcheck`)
  or rely on the CI dry-run. Do not claim a lint passed if the tool was absent —
  state which validation actually ran.

## Progress Tracking

- mark completed items `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document blockers with ⚠️ prefix
- update this plan if implementation deviates from scope

## Solution Overview

Approach A — **fan-out / fan-in** (chosen in brainstorm over a single mixed-OS
matrix job and over chaining three separate workflows):

```
_build-any.yml  (reusable: workflow_call {tag, channel} + secret ANY_CI_TOKEN — UNCHANGED interface)
│
├─ job version   (ubuntu)                 resolve vX.Y.Z | v<base>-nightly.<date>.<n> → output `version`
├─ job desktop   (ubuntu, needs version)  build-any.sh x4 → upload-artifact tarballs
├─ job android   (ubuntu, needs version)  NDK + make build-android → upload-artifact any.aar
├─ job ios       (macos-15, needs version) build-xcframework.sh → upload-artifact any.xcframework.zip
└─ job publish   (ubuntu, needs [desktop,android,ios], contents:write)
                  download all → sha256 the 2 mobile assets → gh release create (one release, all assets)
                  → repository_dispatch any-ui + anytype-swift + anytype-kotlin2 (best-effort)
```

Key design decisions & rationale:
- **One `version` job, threaded to all builds** → every artifact stamps an
  identical version; no per-platform drift.
- **iOS as a sibling job, not a step** → it needs macOS while desktop/android
  need ubuntu; one job = one runner OS. Runs in **parallel** (faster wall-clock;
  flip to gated-behind-desktop with a one-line `needs:` later if macOS minutes
  matter).
- **Build logic stays in scripts/makefiles**, YAML stays thin and mirrors the
  existing `build-any.sh` pattern.
- **Composite action for private-module auth** → the `GOPRIVATE` + `insteadOf`
  block lives once, used by 3 jobs.
- **sha computed centrally in publish** (one ubuntu place) rather than threaded
  as cross-job outputs.

## Technical Details

- **Version stamping (all platforms):** `-ldflags "-X
  github.com/anyproto/any/internal/version.Version=$VER -X ...Commit=$COMMIT -X
  ...BuildDate=$DATE"` (desktop already does this in `build-any.sh`; add to the
  new iOS script and to `android.mk`).
- **⚠️ Version-threading bridge (the env-var trap):** the top-level `Makefile`
  assigns `VERSION := $(shell git describe …)` and builds `LDFLAGS` from it with
  `:=`; `android.mk` is `include`d and inherits that `LDFLAGS`. **A `make`
  variable assigned with `:=` is NOT overridable by an environment variable** —
  so exporting `ANY_BUILD_VERSION`/`VERSION` in the job env would be silently
  ignored and the aar would stamp `git describe`, not the CI-resolved version.
  `build-any.sh` escapes this only because it is a *shell* script reading
  `${ANY_BUILD_VERSION:-…}`. **Fix:** the Android job passes the resolved values
  as **make command-line overrides** — `make build-android VERSION=$VER
  COMMIT=$SHA DATE=$DATE` — which DO override `:=` (command-line vars are set
  before parse, so the `:=` `LDFLAGS` expansion picks them up). Do not rely on
  env for the Makefile path.
- **Stamp consistency across platforms:** `Version` is identical everywhere (the
  single `version`-job output, threaded as `ANY_BUILD_VERSION` to
  desktop/iOS-script and as a `make VERSION=` override to Android). `Commit` is
  identical (all jobs check out the same SHA). `BuildDate` may differ by a few
  seconds (each job stamps its own wall-clock) — **acceptable; stated here so
  it's a conscious choice, not an accident.** All build jobs therefore need git
  available (see fetch-depth below).
- **Android bind (4 ABIs, no FTS):**
  `gomobile bind -tags 'gomobile' -ldflags "$LDFLAGS" -target=android
  -androidapi 26 -javapkg=io.anyproto.any -o dist/android/any.aar
  github.com/anyproto/any/mobile` — bare `-target=android` ⇒ armeabi-v7a +
  arm64-v8a + x86 + x86_64. No `vector`/`fts` (Android stays search-free).
- **NDK resolution (export all three):** `NDK=$(ls -d $ANDROID_HOME/ndk/28.* |
  sort -V | tail -1)`; error if empty; export **`ANDROID_NDK`,
  `ANDROID_NDK_HOME`, and `ANDROID_NDK_ROOT`** (all three, matching heart —
  different gomobile/NDK versions probe different vars). The NDK env MUST be
  exported **before** `make build-android` runs, because `gomobile init` (run on
  the build path) needs it to locate the toolchain.
- **Pinned gomobile toolchain:** build `gomobile` + `gobind` from the
  go.mod-pinned `golang.org/x/mobile` (`go build -o "$GOBIN"
  golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind`), then
  `gomobile init`. No `@latest`. The real invariant: `x/mobile` is already
  pinned in `go.mod` and `mobile/tools.go`'s `bind` import keeps that `require`
  from being tidied away — `go build <cmd-pkg>` then resolves the cmds at the
  pinned version (no module-graph entry for the cmds is required; heart proves
  this). `go mod tidy` must not drop the `x/mobile` require.
- **iOS slices:** two `CGO_ENABLED=1 GOOS=ios GOARCH=arm64 go build -trimpath
  -tags 'mobile fts' -buildmode=c-archive` runs (add `-trimpath` for path-leak
  parity with `build-any.sh`), CC = `clangwrap-ios.sh` (device) /
  `clangwrap-iossim.sh` (sim), then `xcodebuild -create-xcframework` + `ditto -c
  -k --keepParent`.
- **fetch-depth: 0 (required):** the `version` job's `git tag -l`/`git describe`
  needs full history + all tags, and the build jobs recompute `COMMIT`/`DATE`
  (and the iOS script's `git describe` fallback) — so every job that touches git
  checks out with `fetch-depth: 0` (the current single-job workflow already
  does, `_build-any.yml:38`).
- **Single release (publish job):** release channel → `gh release create
  "$VERSION" <all assets> --title --notes`; prerelease channel → same asset list
  + `--prerelease --target "$GITHUB_SHA"`. Notes embed both mobile sha256s.
- **Dispatch payloads:** any-ui unchanged (`version`,`channel`); mobile two add
  `asset` + `sha256`. Guard: empty `ANY_CI_TOKEN` → `::warning::` + skip; a
  failed dispatch must not fail the (already-published) release.

## What Goes Where

- **Implementation Steps** (`[ ]`): everything in this repo — composite action,
  scripts, makefile, workflow restructure, deletion, doc touch-ups, CI dry-run.
- **Post-Completion** (no checkboxes): client-repo work in anytype-swift and
  anytype-kotlin2 (re-pin version, add fetch-script/listener) — separate repos.

## Implementation Steps

### Task 1: Composite action for private-module auth

**Files:**
- Create: `.github/actions/go-private-auth/action.yml`

- [ ] create a `composite` action that runs the auth block: append
      `GOPRIVATE=github.com/anyproto/*` to `$GITHUB_ENV` and
      `git config --global url."https://x-access-token:${TOKEN}@github.com/".insteadOf "https://github.com/"`
- [ ] take the token as an action `input` (e.g. `token`), not a hard-coded secret ref
- [ ] add a header comment explaining why global (not repo-local) git auth is
      needed (go fetches private `any-sync-sdk` from the module cache, a
      different dir than the checkout) — preserve the rationale from the current
      `xcframework.yml` comment
- [ ] validate: `actionlint` if installed; otherwise structural review + confirm
      `runs.using: composite` and `inputs.token` are well-formed
- [ ] (validation gate) action YAML parses / lints clean before next task

### Task 2: Rework `makefiles/android.mk` for the 4-ABI, pinned, stamped build

**Files:**
- Modify: `makefiles/android.mk`
- Modify: `mobile/tools.go` (or `go.mod`) — keep gomobile/gobind in module graph
- Modify: `go.mod` / `go.sum` — settle via `go mod tidy` if the touch-up adds deps

- [ ] `setup-gomobile`: replace `go install ...gomobile@latest` / `...gobind@latest`
      with building the **go.mod-pinned** versions
      (`go build -o "$(GOBIN)" golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind`);
      keep `gomobile init` on the `build-android` path (matches heart — init
      needs the NDK env, which the job exports before `make`), not in setup
- [ ] `build-android`: change `-target=android/arm64` → bare `-target=android`
      (all 4 ABIs); keep `-androidapi 26`, `-javapkg=io.anyproto.any`, tags
      `gomobile`; output `dist/android/any.aar`
- [ ] add version `-ldflags` using the **inherited** `$(VERSION)/$(COMMIT)/$(DATE)`
      from the top-level Makefile → so the CI override path is `make
      build-android VERSION=… COMMIT=… DATE=…` (command-line override, the ONLY
      thing that beats the Makefile's `:=` — see Technical Details ⚠️). Do NOT
      add an `ANY_BUILD_VERSION` env read to the makefile; it can't override `:=`
- [ ] ensure **no `|| true`** anywhere — a failed `gomobile bind` fails `make`
- [ ] keep `install-dev-android` working (uses the same `build-android` output path)
- [ ] verify `go mod tidy` keeps the `golang.org/x/mobile` require (held by
      `mobile/tools.go`'s `bind` import); the cmds need no module-graph entry —
      `go build <cmd-pkg>` resolves them at the pinned version
- [ ] validate: `bash -n`/`make -n build-android` (syntax + flag review);
      confirm `make build-android VERSION=v0.0.0-test` would expand the override
      into `LDFLAGS` (inspect with `make -n`/`--print-data-base` if unsure);
      `go test -tags gomobile ./mobile/...` (mobile shim unchanged, must still
      pass); `go vet ./...`. Full `gomobile bind` run is CI-only (needs NDK) —
      note this, don't claim a local aar build if NDK is absent
- [ ] (validation gate) makefile parses, VERSION override verified, mobile/anyserver suites green before next task

### Task 3: Extract `scripts/build-xcframework.sh` from `xcframework.yml`

**Files:**
- Create: `scripts/build-xcframework.sh`

- [ ] mirror `build-any.sh` shape: read `ANY_BUILD_VERSION` (fallback to `git
      describe`), take an output dir arg, `set -euo pipefail`, header comment
- [ ] port the two-slice `build_slice` function (device via `clangwrap-ios.sh`,
      sim via `clangwrap-iossim.sh`), tags `mobile fts`, `-buildmode=c-archive`,
      and add `-trimpath` (parity with `build-any.sh`; today's iOS build omits it)
- [ ] **add version `-ldflags`** (`-X .../internal/version.*`) on top of the
      existing `-s -w` (today's iOS build omits version stamping)
- [ ] copy headers + `module.modulemap`, run `xcodebuild -create-xcframework`,
      then `ditto -c -k --keepParent any.xcframework <outdir>/any.xcframework.zip`
- [ ] move the old workflow's "App consumption" note into this script's header
- [ ] make it executable (`chmod +x`)
- [ ] validate: `bash -n scripts/build-xcframework.sh` + `shellcheck` if
      installed; **local run on the macOS dev box** if Xcode 26 present
      (`ANY_BUILD_VERSION=v0.0.0-test scripts/build-xcframework.sh /tmp/xcf-test`)
      → confirm a non-empty `any.xcframework.zip`; otherwise note CI-only
- [ ] gate prerequisite still holds: `go test -count=1 ./cmd/anyserver/` passes
- [ ] (validation gate) script syntactically clean, anyserver suite green

### Task 4: Restructure `_build-any.yml` into 5 jobs

**Files:**
- Modify: `.github/workflows/_build-any.yml`

- [ ] keep `on: workflow_call` with the **same** `tag`/`channel` inputs +
      `ANY_CI_TOKEN` secret; move `permissions: contents: write` down to the
      `publish` job only
- [ ] `version` job (ubuntu): checkout with **`fetch-depth: 0`** (its `git tag
      -l`/`git describe` needs all tags + history); port the existing "Resolve
      release version" logic; expose `outputs.version`
- [ ] `desktop` job (ubuntu, `needs: version`): checkout **`fetch-depth: 0`**
      (build-any.sh recomputes COMMIT/DATE from git) + setup-go +
      `go-private-auth` (Task 1) + install jq/unzip + the `build-any.sh` x4 loop
      with `ANY_BUILD_VERSION=${{ needs.version.outputs.version }}`; replace the
      publish step with `actions/upload-artifact` of `dist/any-*.tar.gz`
- [ ] `android` job (ubuntu, `needs: version`): checkout **`fetch-depth: 0`** +
      setup-go (cache, `go-version-file: go.mod`) + `go-private-auth`; resolve
      NDK fail-loud (`NDK=$(ls -d $ANDROID_HOME/ndk/28.* | sort -V | tail -1)`;
      error if empty; export **all three** `ANDROID_NDK`/`ANDROID_NDK_HOME`/
      `ANDROID_NDK_ROOT` **before** the make step); invoke `make build-android
      VERSION=${{ needs.version.outputs.version }} COMMIT=… DATE=…` (command-line
      override — env won't beat the Makefile's `:=`, see Technical Details ⚠️);
      `upload-artifact dist/android/any.aar`
- [ ] `ios` job (macos-15, `needs: version`): checkout **`fetch-depth: 0`** +
      `go-private-auth` + setup-go; "Select Xcode + verify iOS 26 SDK" (fail loud
      on non-`26.*`); `go test -count=1 ./cmd/anyserver/`;
      `scripts/build-xcframework.sh` with `ANY_BUILD_VERSION=${{
      needs.version.outputs.version }}`; `upload-artifact any.xcframework.zip`
- [ ] `publish` job (ubuntu, `needs: [desktop, android, ios]`, `contents:write`):
      `download-artifact` all → one dir; compute `sha256sum` for `any.aar` +
      `any.xcframework.zip` into env; `gh release create` (release vs prerelease
      branch) with **all 6 assets**, notes embedding both shas
- [ ] add the 3 `repository_dispatch` calls in `publish` (any-ui unchanged;
      anytype-swift + anytype-kotlin2 add `asset` + `sha256`); keep the
      empty-token warn-skip guard; dispatch failure must not fail the job
- [ ] update the workflow header comment to describe the 5-job fan-out/fan-in
- [ ] validate: `actionlint` if installed, else structural review; confirm job
      graph (`needs:`) and `outputs` wiring; confirm no secret is referenced
      from a forked-PR-reachable trigger (keep the `if: github.repository ==
      'anyproto/any'` guard)
- [ ] (validation gate) workflow parses; callers still reference the unchanged interface

### Task 5: Delete `xcframework.yml`

**Files:**
- Delete: `.github/workflows/xcframework.yml`

- [ ] remove the file (its build logic now lives in `build-xcframework.sh`, its
      publish/tag duties in the `publish` job)
- [ ] grep the repo for stale references to `xcframework.yml` / the old manual
      dispatch and clean any docs/comments pointing at it
- [ ] validate: `git grep -n xcframework.yml` returns nothing meaningful

### Task 6: Verify callers unchanged + acceptance

**Files:**
- Verify (no change): `.github/workflows/release-any.yml`, `.github/workflows/nightly-any.yml`

- [ ] confirm `release-any.yml` + `nightly-any.yml` still `uses:
      ./.github/workflows/_build-any.yml` with `tag`/`channel`/`ANY_CI_TOKEN` and
      need **no edits**
- [ ] verify all Overview requirements: one release, 6 assets, both mobile shas
      in notes, 3 dispatches, iOS on `v*` tag (no self-tag/self-publish), nightly
      builds all 3 platforms
- [ ] run repo-wide static checks: `go vet ./...`, `go build ./...`,
      `go test ./...`, `go test -tags gomobile ./mobile/...`,
      `go test -count=1 ./cmd/anyserver/`
- [ ] run `actionlint` over `.github/workflows/` + the composite action (install
      if needed); record the actual result
- [ ] **CI dry-run (integration test):** push the branch, `workflow_dispatch` the
      `nightly-any` workflow, and confirm: one prerelease appears with all 6
      assets, the macos/ubuntu jobs all go green, and the 3 dispatches fire
      (or warn-skip cleanly). Capture the run URL in this plan
- [ ] ⚠️ if the CI dry-run can't run before merge (e.g. token scope), record that
      explicitly here rather than marking acceptance complete

### Task 7: [Final] Docs + plan housekeeping

**Files:**
- Modify: workflow header comments (done in Tasks 1/4/5), `CLAUDE.md` if a new pattern is worth recording

- [ ] confirm header-comment docs in `_build-any.yml` + the composite action +
      `build-xcframework.sh` are accurate
- [ ] update `CLAUDE.md` "Build / test / run" if the unified pipeline / Android
      build is worth a line (new `make build-android` semantics, single release)
- [ ] move this plan to `docs/plans/completed/` (`mkdir -p` first)

## Post-Completion
*External, no checkboxes — separate repos, not gated by this plan*

**Client-repo follow-ups:**
- **anytype-swift:** re-pin `ANYSERVER_VERSION` to the `vX.Y.Z` scheme and the
  shared release; the asset name/sha now arrive via the `repository_dispatch`
  payload — add a listener workflow to auto-bump (optional).
- **anytype-kotlin2:** add a fetch-script + sha pin for `any.aar` (parallel to
  the iOS fetch-script) and a `repository_dispatch` listener; decide whether to
  consume the raw release asset or republish to Maven locally.

**Manual verification:**
- After a real `v*` release, smoke-test that each client can fetch + link its
  asset and that `Version()` reports the stamped `vX.Y.Z` (not `dev`) on iOS and
  Android.
- Consider whether macOS-runner cost on every nightly is acceptable; if not,
  gate the `ios` job behind `desktop`/`android` (`needs:`) — one-line change.
