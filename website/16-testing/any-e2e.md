---
title: any end-to-end
description: The server's test layout — unit tests, the real-binary e2e suite, the two-peer harness, and which tests are gated on external infrastructure.
order: 10
---
# any end-to-end

The server's confidence comes from running the compiled binary. Unit tests cover the packages; the `internal/e2e` suite builds `cmd/any`, starts it against a temporary data dir, and drives every shipped endpoint over real HTTP — one process for the API surface, two for anything that only exists between replicas.

## Unit tests

```bash
make test        # go test ./..., then the local-embedder packages with -tags llamacpp
make vet
```

A bare `go test ./...` runs every unit test except the local-embedder ones, which carry the `llamacpp` tag; `make test` runs the untagged suite and then those packages with the tag. In-process handler tests boot a real SDK and the handlers without a binary or a socket — chat, editor, search, links, bundles, the catalog, local store, modify-scope and property surfaces — against the same staging fixture as the e2e suite, and skip without it. Tests that only need a server to boot and serve (the embedded host, the desktop-shell and managed-lifecycle contracts) use the sanitized **placeholder** node configuration instead, which joins no network.

## The staging fixture rule

End-to-end tests boot against the **staging** network. The fixture is a `staging.yml` node configuration at the repository root — gitignored, so each developer copies their own in. Every e2e test resolves it to an absolute path, writes a `config.yaml` pinning `network.nodeconfPath` to it (with `index.embedder: none`, to stay hermetic), and **skips** rather than fails when the file is absent.

> **Note.** With no node configuration at all the binary joins production. That is why the harness always writes an explicit config, and why a test must never rely on the embedded default. Point `ANY_NETWORK_NODECONF_PATH` at staging or a local network for any ad-hoc run too.

## The single-binary suite

```bash
go test ./internal/e2e -run TestE2E_FullFlow -v
```

`TestE2E_FullFlow` boots the binary and walks the endpoint catalog: health, auth, spaces (create/list/get/update/delete/derived), objects, the data plane (query, modify, subscribe), types and properties, chat, editor blocks and the markdown bridge, files, members and invites, sync status, debug — each as a subtest, so a failure names the surface. Focused single-binary tests sit beside it: auth and `any status`, chat, editor blocks, files (raw-body attach, Range downloads, pin/retry/offload), modules and parts, collections (filing, column writes, the type/collection slot guards), runtime dataset schemas and upsert, property PATCH and validation, derived spaces, the local store, the `modifiedAt` / `modifiedBy` stamps, the managed lifecycle (token-gated login, switch, sign-out, shutdown) and the desktop-shell contract (`run --mode managed --addr 127.0.0.1:0` printing `LISTENING <addr>` then `CONTROL_TOKEN <hex>` from an arbitrary working directory).

| Env var | Effect |
|---|---|
| `ANY_E2E_KEEP=1` | keep the temp data dir on success for inspection |
| `ANY_E2E_DUMP=1` | dump the server log even on success |
| `ANY_E2E_HTTP_TIMEOUT` | override the per-request client timeout |

## The multi-peer harness

Some paths cannot be reproduced in one process: invite, join and accept; ACL changes taking effect on the other side; CRDT convergence between replicas; read tracking (your own writes are born read, so an unread message needs a second author). The harness starts two `any` binaries — distinct data dirs, distinct ports, distinct account keys — peered through staging:

```
   peer A (owner)                    peer B (joiner)
   any run --addr :A --data-dir a    any run --addr :B --data-dir b
        │  create space, mint invite       │
        │ ─────── share token ──────────▶  │ POST /v1/spaces/join
        │  accept join request             │
        │ ◀──── sync (staging) ─────────▶  │ write / read / subscribe
```

Tests poll for convergence and call `POST /v1/spaces/:id/sync` on writer then reader each tick to collapse the ~30 s head-sync timer. They take 60–120 s each and are skipped under `-short`.

```bash
go test ./internal/e2e -run 'TestE2E_Multipeer' -v -timeout 30m
```

| Test | Proves |
|---|---|
| `multipeer` | invite → join → accept, permissions, convergence |
| `multipeer_chat`, `_chat_mentions`, `_chat_reading` | messages replicate, derived mentions, unread flags across authors |
| `multipeer_bundles`, `multipeer_onetoone` | the joiner adopts the owner's bundle root; both sides of a direct space install the derived chat on first attempt |
| `multipeer_events`, `multipeer_processes` | space-scope events and process progress cross peers with the sender's verified identity |
| `multipeer_identities` | the identities directory populates once peers share a space |
| `multipeer_markdown`, `multipeer_realtime` | joiner-side writes and write→visible latency without forced sync |
| `multipeer_modified_by` | both peers converge on the signer of the object's latest change, whichever member wrote it |
| `multipeer_catalog` | the joiner's catalog setup adopts every root the owner installed, with the same property ids |
| `multipeer_links` | each peer builds its own link index: a block link the owner writes appears in the joiner's backlinks and leaves when the block is deleted |
| `multipeer_dataview`, `multipeer_bin` | shared view records sync both ways while `localSettings` stays on its device; a bin move and its stamps travel in one change, and a restore clears them everywhere |
| `multipeer_bundle_type` | a bundle-declared type installed on both peers while apart converges on one definition per property handle |
| `multipeer_cancel_join` | the joiner withdraws a request, the row reads `deleted`, and the same token re-requests |
| `multidevice_techspace`, `multidevice_techbundle`, `multipeer_devices` | two devices on **one** mnemonic: tech-space convergence, account-level bundles, device registry, active-app election |

## Gated tests

Some suites need infrastructure a laptop does not have, and gate on an environment variable — skipped otherwise:

| Gate | Test |
|---|---|
| `ANY_E2E_P2P=1` | real-mDNS discovery between two servers on this host (needs a multicast-capable interface) |
| `ANY_PUSH_E2E_PEER_ID` + `ANY_PUSH_E2E_ADDRS` | push-notification flow against a reachable push node |
| `ANY_E2E_FILES_NODECONF` (+ `ANY_E2E_FILES_SIZES`) | Alice→Bob file latency over a local network with a real object store |
| `ANY_TEST_LOCAL_EMBEDDER=1` + `ANY_INDEX_LOCAL_MODEL_PATH` | the local llama.cpp embedder against the real model (needs `make llamacpp`) |
| `ANY_EVAL_*`, `ANY_BEIR_DIR`, `ANY_VEC_BENCH=1` | search evaluation harnesses ([Evaluation](../search/evaluation.html)) |

## Contract checks

- **OpenAPI drift.** `make swagger` regenerates `/v1/openapi.json` from handler annotations; the PR check fails if the committed spec differs. The runtime's own drift check pins against the published spec, so a shape change is visible on both sides.
- **Error mapping.** The spec carries routes, shapes and status codes but not `error.code` strings; dedicated tests pin the mapping from SDK sentinels to codes (files, history, datasets) so a code rename cannot slip through unnoticed.
- **Golden wire shapes.** Push topics and payloads are pinned by golden tests for byte-compatibility with the mobile clients.
- **Catalog.** `make catalog-validate` compiles the embedded usecase catalog through the server's own gate and prints every problem with its YAML path; the PR check and the release build fail on any.

## Running a scratch server by hand

For a manual pass the same rule applies — a temp root, an explicit network, an unused port:

```bash
ANY_DATA_DIR=/tmp/any-scratch bin/any init
ANY_DATA_DIR=/tmp/any-scratch ANY_NETWORK_NODECONF_PATH=./staging.yml \
  bin/any run --addr 127.0.0.1:7009
bin/any --addr 127.0.0.1:7009 status
```

Build the binary with `make build` first — it writes `bin/any` — and run it through `nix develop -c` where the flake shell is available, so the local embedder finds `libffi` — see [Builds and CI](../operations/builds-and-ci.html).
