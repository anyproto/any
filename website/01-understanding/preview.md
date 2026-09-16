---
title: Preview status & license
description: The current development status, license decision, and boundaries to understand before adopting Any.
order: 5
---
# Preview status and license

Any is a **developer preview**. Use it to explore the data model, build applications and give feedback. API shapes and behavior can change during the preview.

## License

Any is intended to be open source. **The open-source license type is still to be decided.**

## What runs on your device

The `any` binary contains the local database server and CLI. The companion `anyrt` runtime runs programs, agents and scheduled work. The standalone setup uses a separate executable; application hosts can also embed the runtime. See [Install](../quickstart/install.html) and [Run anyrt](../quickstart/anyrt.html) for their separate setup steps.

The server binds to loopback. Its API serves the account loaded into that process and has no per-caller authentication. It is designed to be used by software on the same device. [Security model →](../operations/security-model.html)

## Offline behavior

Database operations work against local state. Sync needs a reachable peer. Fetching an uncached file needs a source that holds its bytes. Calling an external model or connector needs that service to be available. Scheduled programs need a device with the runtime running.

For local semantic search, configure local embeddings. Model files and the embedding libraries must first be available on the device. [Embedding setup →](../search/embedders.html)

## Before using important data

Understand the [account recovery process](../auth/accounts.html), the [data directory](../operations/data-dir.html), [network selection](../quickstart/networks.html), and [encryption boundaries](encryption.html). The recovery phrase restores your identity; availability of the data still depends on the copies held by devices and the sync network.

The [HTTP reference](../reference/http-api.html) lists the current endpoints. A registered endpoint that is not implemented returns `501 sdk.not_implemented`; its presence alone does not mean the operation is available.
