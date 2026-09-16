---
title: Quickstart
description: Start the local Any server, create a page, and watch it change. Then choose a client or embed the server in your app.
order: 0
---
# Quickstart

Create a page, read it back, and watch a rename arrive over a live subscription. Start with **[Install](install.html)**, then **[Your first page with curl](curl.html)**. You need a terminal, `curl`, and `jq`.

Any is in developer preview. Use an account and data directory for experiments; APIs and data formats can change. The release license is still TBD.

## The shape of every quickstart

Your client talks to the Any HTTP server on the same device. The server owns the data, handles writes, and syncs with other devices when they are reachable.

| Step | What you get |
|------|--------------|
| `any init` | An account and a recovery phrase. |
| `any run` | A server listening on `127.0.0.1:7001`. |
| Create a space | A place to keep and share objects. |
| Create a `page` | A named object with a document body. |
| Query | The matching records now. |
| Subscribe | An initial snapshot, then changes to that result window. |

A type says what an object **is**. Every object has exactly one. Collections are optional groups you file it under. The first example needs only the built-in `page` type; the [tutorial](../tutorial/index.html) adds your own properties and datasets later.

## Before you start

[Install](install.html) sets up a dedicated data directory and explicitly selects local embeddings. Keep its server running while following a client page.

Local HTTP, sync, and model calls are separate choices. The default sync network is production; [Networks](networks.html) shows how to choose a separate network before starting. Local embeddings compute search vectors on your device. An agent can still call whichever language-model provider you configure.

## Pick a client

<div class="cards">
<a href="install.html"><strong>1. Install</strong><span>Get the binary, create an account, and start the server.</span></a>
<a href="curl.html"><strong>2. Create your first page</strong><span>Create, read, and watch a change using raw HTTP.</span></a>
<a href="javascript.html"><strong>JavaScript</strong><span>A complete fetch client with a live, ordered result window.</span></a>
<a href="python.html"><strong>Python</strong><span>A runnable standard-library client.</span></a>
<a href="cli.html"><strong>CLI</strong><span>Use the any command for everyday operations.</span></a>
<a href="anyrt.html"><strong>Programs and agents</strong><span>Run anyrt beside the server and schedule a program.</span></a>
</div>

## Embed the server

The HTTP contract stays the same when the server runs inside your application.

- [Android](android.html): add `any.aar`, start it, and use its bound address.
- [iOS](ios.html): add `any.xcframework` and call it from Swift.
- [Networks](networks.html): choose sync peers and isolate experiments.

Next: **[Install](install.html)**. Already running? Go directly to **[curl](curl.html)**.
