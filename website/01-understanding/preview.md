---
title: Preview status and license
description: What "developer preview" means for the API, the MIT license, what runs on your device, and what still needs a network.
order: 5
---
# Preview status and license

`any` is a **developer preview**. The data model, the HTTP API and the on-disk format can still change between releases, and a breaking wire change is absorbed by bumping the `/v1/` prefix rather than by a migration. Use a separate account and data root for experiments.

## License

`any` is open source under the [**MIT license**](https://github.com/anyproto/any/blob/main/LICENSE). You can use, modify and distribute it in personal and commercial applications, provided you retain the copyright and license notice. This license applies to developer preview releases too.

The companion runtime [anyrt](https://github.com/anyproto/anybao/blob/main/LICENSE) and the SDKs ([any-sync](https://github.com/anyproto/any-sync/blob/main/LICENSE.md), [any-store](https://github.com/anyproto/any-store/blob/main/LICENSE.md), [any-sync-sdk](https://github.com/anyproto/any-sync-sdk/blob/main/LICENSE.md)) are also MIT licensed.

Third-party dependencies and models retain their respective licenses. For example, the yzma bindings and the default Qwen3 embedding model use Apache-2.0; see the [local embedder's license notes](https://github.com/anyproto/any/blob/main/third_party/llamacpp/README.md#licenses).

## What runs on your device

The `any` binary is the server and the CLI: `any run` owns the any-store database, the search index and the sync engine; every other `any <cmd>` is an HTTP client of it. The companion `anyrt` runtime executes programs, agent sessions and triggers in a wasm cage against that server. Standalone, they are two processes; a host app can embed both ([Install](../quickstart/install.html), [anyrt](../quickstart/anyrt.html), [Embedding anyrt](../agents/embedding-anyrt.html)).

The server binds to loopback. Its API serves the account loaded into that process and currently has no per-caller authentication — with the plan to ship proper auth and access capabilities support. It is designed to be used by software on the same device. [Security model →](../operations/security-model.html)

## Offline behavior

Database operations work against local state. Sync needs a reachable peer. Fetching an uncached file needs a source that holds its bytes. Calling an external model or connector needs that service to be available. Scheduled programs need a device with the `anyrt` runtime running. [Local-first →](local-first.html)
