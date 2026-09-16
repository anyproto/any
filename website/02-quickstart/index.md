---
title: Quickstart
description: Install any, create an account, run the server, and make your first space, object, query, and live subscription — from curl, the CLI, JavaScript, Python, Android, iOS, or the anyrt runtime.
order: 0
---
# Quickstart

Five minutes from nothing to a live subscription. Every path below drives the same local HTTP server; pick the client you will actually use.

## The shape of every quickstart

```
any init  ──▶  any run  ──▶  POST /v1/spaces  ──▶  POST …/objects  ──▶  POST …/objects/query  ──▶  POST …/objects/query/subscribe
 (account)     (server)      (a space)             (an object)          (a snapshot)               (snapshot + live deltas)
```

1. **Install** the binary and create an account. The mnemonic prints once — back it up.
2. **Run** the server: `any run` listens on `http://127.0.0.1:7001`.
3. **Create a space** — the unit of sharing and encryption.
4. **Create an object** with its type and a name.
5. **Query** the space's `objects` storage collection — an indexed read on your disk.
6. **Subscribe** — the same query, plus `added` / `updated` / `removed` frames over SSE for as long as the connection is open.

Everything you write is on your machine the moment the request returns; sync to other devices and members happens in the background ([Local-first](../understanding/local-first.html)).

## Before you start

```bash
any init                     # prints the mnemonic to stderr — save it
any run                      # foreground; leave it running in this terminal
any status                   # in another terminal: {"status":"ok", …}
```

> **Note.** A binary with no network configured joins the **production** any-sync network. To experiment without touching it, point `ANY_NETWORK_NODECONF_PATH` at a staging or local nodeconf first — see [Networks](networks.html).

## Pick a client

| Page | You get |
|------|---------|
| [Install](install.html) | Release tarballs, building from source, first-run account creation. |
| [curl](curl.html) | The four calls with raw JSON, including reading an SSE stream with `curl -N`. |
| [CLI](cli.html) | The same flow with `any …` subcommands and `jq`. |
| [JavaScript](javascript.html) | `fetch` + a streaming reader for the POST-based SSE. |
| [Python](python.html) | `urllib` / `http.client`, no third-party packages. |
| [Android](android.html) | Embedding the server as `any.aar` in an app process. |
| [iOS](ios.html) | Embedding as `any.xcframework`. |
| [anyrt](anyrt.html) | `anybao.toml`, `anyrt serve`, and a first trigger record. |
| [Networks](networks.html) | Production vs staging vs local nodeconf, and the placeholder that joins nothing. |

<div class="cards">
<a href="install.html"><strong>Install</strong><span>Get the binary and create an account.</span></a>
<a href="curl.html"><strong>curl</strong><span>Space → object → query → subscribe with raw HTTP.</span></a>
<a href="cli.html"><strong>CLI</strong><span>The same flow with the any command.</span></a>
<a href="javascript.html"><strong>JavaScript</strong><span>fetch, ReadableStream, and SSE frames.</span></a>
<a href="python.html"><strong>Python</strong><span>Standard library only.</span></a>
<a href="android.html"><strong>Android</strong><span>Embed the server with any.aar.</span></a>
<a href="ios.html"><strong>iOS</strong><span>Embed the server with any.xcframework.</span></a>
<a href="anyrt.html"><strong>anyrt</strong><span>Run the agent runtime and schedule a program.</span></a>
<a href="networks.html"><strong>Networks</strong><span>Which any-sync network you are talking to.</span></a>
</div>
