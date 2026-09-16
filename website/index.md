---
title: Any docs
description: Build apps and agents with a local document database, live queries and encrypted peer-to-peer sync.
---
<div class="hero">

# Any

Any is a **local document database server** for apps and agents. It runs on your users’ devices, with live queries and end-to-end encrypted peer-to-peer (P2P) sync. Apps connect through a local HTTP API and can read and write local data offline.

The companion runtime, **[anyrt](quickstart/anyrt.html)**, runs programs and agents whose code and persistent state live alongside the data they work with. Its harness is designed for long-lived sessions over structured data, with memory, scheduling and reusable programs.

<div class="pills"><span class="pill">local-first</span><span class="pill cyan">encrypted P2P sync</span><span class="pill amber">CRDTs</span><span class="pill magenta">document queries</span><span class="pill">live queries</span><span class="pill cyan">programs &amp; agents</span><span class="pill amber">local embeddings</span></div>

**Developer preview.** APIs may change. Any is intended to be open source; the license type is TBD. [Preview status](understanding/preview.html).

</div>

## Get started

Start with [Install](quickstart/install.html), then [your first page](quickstart/curl.html). The example below requires the `any` binary, curl and jq. It uses a dedicated data root and local embeddings. [Choose your sync network](quickstart/networks.html) before starting; the standalone server defaults to the production network.

<pre class="term"><code class="language-sh">$ export ANY_DATA_DIR="$HOME/.any-demo"
$ export ANY_INDEX_EMBEDDER=local
$ any init
<span class="out">  A new wallet was generated. Write down the recovery phrase below.
  You will NOT see it again. Anyone with this phrase owns the account.
&lt;twelve words — your only way to restore the account&gt;
{
  "accountId": "A8tR…",
  "created": true
}</span>
$ any run &
<span class="out">LISTENING 127.0.0.1:7001</span>
$ until any status >/dev/null 2>&1; do sleep 0.2; done
$ API=http://127.0.0.1:7001/v1
$ S=$(curl -fsS $API/spaces -H 'content-type: application/json' -d '{"name":"notes"}' | jq -er .id)
$ curl -fsS $API/spaces/$S/objects -H 'content-type: application/json' \
    -d '{"type":"page","initialProperties":{"any":{"name":"hello"}}}'
<span class="out">{
  "objectId": "bafyrei…"
}</span>
$ curl -fsSN $API/spaces/$S/objects/query/subscribe -H 'content-type: application/json' \
    -d '{"sort":["-modifiedAt"],"limit":20}'
<span class="out">event: ready
data: {}

event: snapshot
data: {"records":[{"id":"bafyrei…","any":{"type":"page","name":"hello"},…}]}

event: changes
data: [{"versionId":"…","updated":[…]}]</span></code></pre>

<div class="tiers">
<div class="tier"><h3>Database</h3><p>Store documents and structured records. Query, filter and aggregate them locally, and keep views current with live updates.</p><a href="database/index.html">Learn more</a></div>
<div class="tier"><h3>Sync &amp; collaboration</h3><p>Share spaces with permissions. Devices exchange encrypted changes and merge concurrent edits using CRDTs.</p><a href="realtime/index.html">Learn more</a></div>
<div class="tier"><h3>Programs &amp; agents</h3><p>Use anyrt for programs, persistent agent memory and scheduled work. Inspect effects and traces to understand each run.</p><a href="programs/index.html">Learn more</a></div>
</div>

## Explore the docs

<div class="cards">
<a href="understanding/index.html"><strong>Core concepts</strong><span>The local server, data model, sync and privacy boundaries.</span></a>
<a href="quickstart/index.html"><strong>Quickstart</strong><span>Install Any and build a working client with curl, JavaScript or Python.</span></a>
<a href="tutorial/index.html"><strong>Tutorial</strong><span>Build up from objects to typed properties, datasets and apps.</span></a>
<a href="database/index.html"><strong>Database</strong><span>Spaces, objects, types, collections, queries, writes and history.</span></a>
<a href="realtime/index.html"><strong>Realtime</strong><span>Live query windows, sync status, space lists and events.</span></a>
<a href="auth/index.html"><strong>Identity &amp; accounts</strong><span>Account setup, recovery, device identities and profiles.</span></a>
<a href="collaboration/index.html"><strong>Collaboration</strong><span>Share spaces, manage members and install shared bundles.</span></a>
<a href="types/index.html"><strong>Chat &amp; documents</strong><span>Messages, document blocks, pages, links and backlinks.</span></a>
<a href="files/index.html"><strong>Files</strong><span>Attach and fetch files; understand caching, P2P availability and backup.</span></a>
<a href="search/index.html"><strong>Search</strong><span>Full-text, semantic and hybrid search, including local embeddings.</span></a>
<a href="notifications/index.html"><strong>Notifications</strong><span>Encrypted push notifications and progress for running work.</span></a>
<a href="programs/index.html"><strong>Programs</strong><span>Write and run programs, control effects, and inspect traces.</span></a>
<a href="scheduling/index.html"><strong>Scheduling</strong><span>Recurring, one-time and event-triggered work on your devices.</span></a>
<a href="agents/index.html"><strong>Agents</strong><span>Conversations, reusable tools, persistent memory and connectors.</span></a>
<a href="operations/index.html"><strong>Operations</strong><span>Configure, run and troubleshoot the server and runtime.</span></a>
<a href="testing/index.html"><strong>Testing</strong><span>Check API behavior, replica convergence and runtime execution.</span></a>
<a href="reference/index.html"><strong>Reference</strong><span>Exact endpoints, commands, stream frames, errors and configuration.</span></a>
</div>

## What’s included

> **Local-first.** Each device works with its local data and exchanges changes when peers are reachable. Sync nodes distribute and back up encrypted space content without being able to read it. Local database files, embedding configuration and external model calls have separate [privacy boundaries](understanding/encryption.html).

- **Local document database** — documents and structured records, with queries and offline reads and writes.
- **Live queries** — subscriptions that keep your app’s views up to date.
- **Encrypted P2P sync** — changes sync between devices and merge automatically using CRDTs.
- **Search** — full-text, vector and hybrid search, with support for local embeddings.
- **Identity and access control** — accounts, shared spaces, invitations and member permissions.
- **Files and collaboration** — attachments, chat and collaborative document editing.
- **Programs and agents** — the companion `anyrt` runtime provides long-lived sessions, persistent memory, reusable tools and scheduling.

Build knowledge bases, agent memory systems, custom harnesses, research tools, business apps or just personal tools for fun.

Use [the data model](database/data-model.html) to plan your records, [JavaScript](quickstart/javascript.html) or [Python](quickstart/python.html) to connect a client, and [the HTTP reference](reference/http-api.html) for exact request and response shapes.

Also available as [`llms.txt`](llms.txt) for language models.
