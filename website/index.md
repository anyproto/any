---
title: any docs
description: Documentation for any — the open source, reactive, local-first, end-to-end encrypted database with built-in chat and editor CRDTs, and a sandboxed runtime for jobs and agents that live in your data.
---
<div class="hero">

# Any

The open source, reactive, **local-first** database. Documents live on your devices, merge as CRDTs, sync end-to-end encrypted, and answer Mongo-style queries with live subscriptions — online or not. Chat and a block editor are built in as first-class CRDT types, and **anyrt** runs sandboxed Python programs, scheduled jobs and agents next to your data instead of on someone else's server.

<div class="pills"><span class="pill">local-first</span><span class="pill cyan">e2e encrypted</span><span class="pill amber">crdt</span><span class="pill magenta">mongo queries</span><span class="pill">sse live queries</span><span class="pill cyan">wasm-sandboxed programs</span><span class="pill amber">single go binary</span></div>

</div>

## Get started

<pre class="term"><code class="language-sh">$ any init
<span class="out">account  A9f3…c21e   (mnemonic printed once — write it down)</span>
$ any run &
<span class="out">listening 127.0.0.1:7001   network prod   bootstrapping…done</span>
$ curl -s :7001/v1/spaces -d '{"name":"notes"}' | jq -r .id
<span class="out">bafyrei…7q.1a2b3c</span>
$ curl -s :7001/v1/spaces/$S/objects -d '{"initialProperties":{"any":{"name":"hello"}}}'
$ curl -sN :7001/v1/spaces/$S/objects/query/subscribe -d '{"limit":20}'
<span class="out">event: ready
event: snapshot   {"records":[{"id":"…","any.name":"hello"}],"total":1}
event: changes    …live from here, from every device you own</span></code></pre>

<div class="tiers">
<div class="tier"><h3>Database</h3><p>Spaces of objects and datasets, typed properties, Mongo-style filters, aggregation pipelines, version history — every byte CRDT-merged and encrypted.</p><a href="database/index.html">Learn more</a></div>
<div class="tier"><h3>Sync &amp; realtime</h3><p>Windowed live queries over SSE, head-sync with peers, sync status, an ephemeral event bus, end-to-end encrypted push.</p><a href="realtime/index.html">Learn more</a></div>
<div class="tier"><h3>Runtime</h3><p>Python programs in a wasm cage with a recorded effect boundary; cron / once / event triggers pinned to a device; the bao agent.</p><a href="programs/index.html">Learn more</a></div>
</div>

## Everything

<div class="cards">
<a href="understanding/index.html"><strong>Understanding any</strong><span>Local-first, encryption, CRDT consistency, the invariants.</span></a>
<a href="quickstart/index.html"><strong>Quickstart</strong><span>Install, first space in curl, CLI, JS, Python, mobile, anyrt.</span></a>
<a href="database/index.html"><strong>Database</strong><span>Spaces, objects, types, reading & writing, aggregation, history.</span></a>
<a href="realtime/index.html"><strong>Realtime</strong><span>Subscribe, sync status, space list, event bus.</span></a>
<a href="auth/index.html"><strong>Auth & identity</strong><span>Mnemonic accounts, devices, identities directory.</span></a>
<a href="collaboration/index.html"><strong>Collaboration</strong><span>Members, invites, ACL, one-to-one spaces, bundles.</span></a>
<a href="types/index.html"><strong>Modules</strong><span>Chat, block editor, page types, any:// links.</span></a>
<a href="files/index.html"><strong>Files</strong><span>Encrypted attachments, durability, cache.</span></a>
<a href="search/index.html"><strong>Search</strong><span>Full-text, vector, hybrid — with a local embedder.</span></a>
<a href="notifications/index.html"><strong>Notifications</strong><span>E2E push and process progress.</span></a>
<a href="programs/index.html"><strong>Programs</strong><span>Sandboxed Python, effects, traces that replay bit-exact.</span></a>
<a href="scheduling/index.html"><strong>Scheduling</strong><span>Cron, once and event triggers as synced records.</span></a>
<a href="agents/index.html"><strong>Agents</strong><span>bao: conversations, tools, memory, connectors.</span></a>
<a href="operations/index.html"><strong>Operations</strong><span>Server lifecycle, config, networks, builds.</span></a>
<a href="testing/index.html"><strong>Testing</strong><span>e2e patterns and the kernel-fidelity harness.</span></a>
<a href="reference/index.html"><strong>Reference</strong><span>HTTP API, CLI, events, errors, config, glossary.</span></a>
</div>

## Why local-first

> **Why it matters.** A hosted backend is available exactly when its vendor is. In any every device holds the full state of every space it belongs to, every write lands locally first, and peers converge later through encrypted sync — the network is an optimisation, not a dependency. Relay nodes store ciphertext and can't read a single document.

- **Reactive** — every read has a live form: `/query` gives a snapshot, `/query/subscribe` keeps a window of results current.
- **Mongo-style queries** — `$eq`, `$in`, `$regex`, `$text`, nested paths, array matching, `$group`/`$unwind` pipelines.
- **CRDT** — concurrent edits from any number of devices merge deterministically; versions are content-addressed changes you can diff and view.
- **Built-in messenger and editor** — chat messages with reactions, mentions and read tracking; block documents with a markdown bridge.
- **Runtime in your data** — programs, triggers and agent memory are records in your encrypted space, not rows in a vendor's database.

Also available as [`llms.txt`](llms.txt) for language models.
