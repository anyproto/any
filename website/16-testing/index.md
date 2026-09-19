---
title: Testing
description: How the server and the agent runtime are tested — real-binary end-to-end suites for any, and a layered, replay-deterministic harness for anybao.
order: 0
---
# Testing

Two codebases, two doctrines that fit their subject. The `any` server is tested by running the **real binary against a real network** — unit tests for the parts, then end-to-end suites that boot one or two servers and drive every endpoint over HTTP. The anybao runtime is tested around one property: **everything nondeterministic is an effect**, so a recorded trace replays the whole system deterministically, and every test layer exists to keep that cheap to assert.

The `any` commands run in the server repository, the anybao / `anyrt` ones in the runtime repository. The server's e2e suites skip silently without a test nodeconf — a green run without one proves little.

## Testing against any

```bash
make test                                   # unit tests, local embedder included
go test ./internal/e2e -run TestE2E_FullFlow # boot the binary, drive every endpoint
go test ./internal/e2e -run Multipeer        # two peers, invite/join, CRDT convergence
```

- Unit tests live next to the code; `make test` runs the full suite, and a bare `go test ./...` skips only the local-embedder tests (`-tags llamacpp`). It is also a PR check.
- End-to-end tests build the binary, run `any run` against a temporary data dir on an ephemeral port, and talk JSON over real TCP. They need a **staging** node configuration and skip without one — a test must never join production.
- Multi-peer tests start two servers with distinct accounts (or the same mnemonic on two device keys) and prove what a single process cannot: invites, membership, cross-replica convergence, LAN discovery.

## Testing against anybao

```bash
cargo test --manifest-path runtime/Cargo.toml   # runtime logic + replay determinism
uv run pytest                                   # guest programs under the real kernel, offline
make test-integration                           # opt-in, against a live any server
```

- The Rust runtime's policy — effect boundary, capabilities, trace and replay, deploy, triggers — is unit-tested with fakes.
- Guest programs run under the **real guest kernel** host-side, with only the effect boundary faked, so an import that the sandbox forbids fails in the test exactly as it would in the wasm guest.
- The runtime binary is driven end-to-end as a subprocess against a stdlib fake that plays both the any server and the LLM provider.
- Integration and live-eval layers are opt-in and point at a real server.

> **Why it matters.** A local-first system's hardest bugs are convergence bugs — two replicas, one network partition, one merge. Faking the sync layer would hide exactly those, so the server suites pay for real peers. And an agent's hardest bugs are non-reproducible ones — so the runtime makes every run a trace that replays bit-for-bit.

## What to write when

| You changed | Write |
|---|---|
| a server handler or SDK wrapper | a unit test in the package; an e2e subtest if the wire shape changed |
| anything that crosses replicas (ACL, sync, derived objects, bundles) | a multi-peer e2e test |
| an OpenAPI-visible shape | regenerate the spec (`make swagger`) — the PR check diffs it |
| an error code | a test pinning the sentinel-to-code mapping — the spec carries status codes, not `error.code` strings |
| the usecase catalog | `make catalog-validate` — the PR check and the release build both run it |
| a guest program or module | a kernel-fidelity pytest with fake effects |
| a new effect | a broker registration, a catalog entry and a read/mutate classification test |
| an LLM adapter | one recorded provider fixture; the loop itself replays from traces |

<div class="cards">
<a href="any-e2e.html"><strong>any end-to-end</strong><span>The real-binary suites, the staging fixture rule, multi-peer harness, gated tests</span></a>
<a href="anybao-harness.html"><strong>anybao harness</strong><span>Layers L0–L6, the kernel-fidelity harness, fixtures, the scratch rig</span></a>
</div>
