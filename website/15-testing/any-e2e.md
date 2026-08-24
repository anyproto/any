---
title: any end-to-end
description: The server's test layout — unit tests behind build tags, the real-binary e2e suite, the two-peer harness, and which tests are gated on external infrastructure.
order: 10
---
# any end-to-end

The server's confidence comes from running the compiled binary. Unit tests cover the packages; the `internal/e2e` suite builds `cmd/any`, starts it against a temporary data dir, and drives every shipped endpoint over real HTTP — one process for the API surface, two for anything that only exists between replicas.

## Unit tests

```bash
make test        # go test -tags 'fts vector' ./...
make vet
```

The search legs are compile-time tags, and tests covering either leg are tagged to match — a bare `go test ./...` compiles but skips them. In-process handler tests boot a real SDK against a sanitized **placeholder** node configuration that serves but joins no network; they cover the chat, editor, search, bundles, modify-scope and property surfaces without a binary or a socket.

## The staging fixture rule

End-to-end tests boot against the **staging** network. The fixture is a `staging.yml` node configuration at the repository root — gitignored, so each developer copies their own in. Every e2e test resolves it to an absolute path, writes a `config.yaml` pinning `network.nodeconfPath` to it (with `index.embedder: none`, to stay hermetic), and **skips** rather than fails when the file is absent.

> **Note.** With no node configuration at all the binary joins production. That is why the harness always writes an explicit config, and why a test must never rely on the embedded default. Point `ANY_NETWORK_NODECONF_PATH` at staging or a local network for any ad-hoc run too.

## The single-binary suite

```bash
go test ./internal/e2e -run TestE2E_FullFlow -v
```

`TestE2E_FullFlow` boots the binary and walks the endpoint catalog: health, auth, spaces (create/list/get/update/delete/derived), objects, the data plane (query, modify, subscribe), types and properties, chat, editor blocks and the markdown bridge, files, members and invites, sync status, debug — each as a subtest, so a failure names the surface. Focused single-binary tests sit beside it: chat, editor blocks, files (raw-body attach, Range downloads, pin/retry/offload), runtime dataset schemas and upsert, property PATCH and validation, derived spaces, `modifiedAt` stamps, and the desktop-shell contract (`--addr 127.0.0.1:0` printing `LISTENING <addr>` from an arbitrary working directory).

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
| `multidevice_techspace`, `multipeer_devices` | two devices on **one** mnemonic: tech-space convergence, device registry, active-app election |

## Gated tests

Some suites need infrastructure a laptop does not have, and gate on an environment variable — skipped otherwise:

| Gate | Test |
|---|---|
| `ANY_E2E_P2P=1` | real-mDNS discovery between two servers on this host (needs a multicast-capable interface) |
| `ANY_PUSH_E2E_PEER_ID` + `ANY_PUSH_E2E_ADDRS` | push-notification flow against a reachable push node |
| `ANY_E2E_FILES_NODECONF` (+ `ANY_E2E_FILES_SIZES`) | Alice→Bob file latency over a local network with a real object store |
| `ANY_TEST_LOCAL_EMBEDDER=1` + `ANY_INDEX_LOCAL_MODEL_PATH` | the in-process embedder against the real model |
| `ANY_EVAL_*`, `ANY_BEIR_DIR`, `ANY_VEC_BENCH=1` | search evaluation harnesses ([Evaluation](../search/evaluation.html)) |

## Contract checks

- **OpenAPI drift.** `make swagger` regenerates `/v1/openapi.json` from handler annotations; the PR check fails if the committed spec differs. The runtime's own drift check pins against the published spec, so a shape change is visible on both sides.
- **Error mapping.** The spec carries routes, shapes and status codes but not `error.code` strings; dedicated tests pin the mapping from SDK sentinels to codes (files, history, datasets) so a code rename cannot slip through unnoticed.
- **Golden wire shapes.** Push topics and payloads are pinned by golden tests for byte-compatibility with the mobile clients.

## Running a scratch server by hand

For a manual pass the same rule applies — a temp root, an explicit network, an unused port:

```bash
ANY_DATA_DIR=/tmp/any-scratch ./any init
ANY_DATA_DIR=/tmp/any-scratch ANY_NETWORK_NODECONF_PATH=./staging.yml \
  ./any run --addr 127.0.0.1:7009
./any --addr 127.0.0.1:7009 status
```

Build the binary with `make build` first (and through `nix develop -c` where the flake shell is available) so the search legs are compiled in — see [Builds and CI](../operations/builds-and-ci.html).
