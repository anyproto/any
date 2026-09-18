---
title: Tutorial
description: Four parts that build on each other — an object, then properties, then a dataset, then the parts, bundles and miniapps that turn it into an app — so you can stop at any level and use only what you have learned.
order: 0
---
# Tutorial

`any` looks like a lot of machinery from the outside — types, properties, parts, datasets, modules, bundles, a catalog. Almost all of it is optional. In the simplest case you create an object and you are done; every further concept is one more thing you can add when you need it. This tutorial adds them in order, one part per level, with a working example at each step.

**Before you start:** the [curl quickstart](../quickstart/curl.html) — a booted server, `curl`, `jq`, and one terminal that keeps the tutorial variables between parts.

## The four levels

```
 1. Objects        an object with a name and a description         a notebook
 2. Properties     a type: typed columns on objects of that type    a password manager
 3. Datasets       a table of records inside one object            10 000 emails
 4. Apps           parts, bundles, the sidebar, the catalog         the mailbox as an app
```

Each level is complete on its own. A space full of plain objects with names is a valid space. A type with three properties and no parts is a valid type. You never have to reach the last level to use the first. Parts 1, 2 and 3 each create their own example in the shared tutorial space; Part 4 extends the mailbox from Part 3 and needs its `MAILBOX` and `INBOX` variables.

| Part | You build | You learn |
|------|-----------|-----------|
| [1. Objects](objects.html) | a notebook of named objects | create, read back, subscribe, rename, delete; the universal `any` group |
| [2. Properties](properties.html) | a password manager | types, the three ids, kinds and descriptors, choice options, filters on values, collections stacking a second group of columns |
| [3. Datasets](datasets.html) | a mailbox holding 10 000 emails | a table inside one object: when to use it instead of many objects, an enforced schema, idempotent import, paging, search, aggregation |
| [4. Apps](apps.html) | the mailbox as a sidebar app | parts and modules, bundles that converge across devices, `miniapp`, the usecase catalog |

## One model, two directions

Seen as a database, the simple things are objects, types and properties, and an app is the complicated construction on top. Seen from the interface, it flips: the simple thing is the app in the sidebar — a wiki, a chat, a contacts directory — and the objects, types and properties inside it are the details. `any` has to serve both. The levels above are the database direction; Part 4 ends where the interface begins, at the [usecase catalog](../collaboration/bundles.html) that installs the well-known apps with one call.

## The picture to keep in mind

<svg class="figure" viewBox="0 0 760 210" role="img" aria-label="An object is one type; a type has parts; a part owns a dataset served by a module. Property values hang off the object, property definitions off the type.">
<style>
.fig1 .box{fill:var(--bg1);stroke:var(--line2);stroke-width:1}
.fig1 .sub{fill:none;stroke:var(--line2);stroke-width:1;stroke-dasharray:4 3}
.fig1 .mod{fill:var(--bg1);stroke:var(--accent);stroke-width:1}
.fig1 .t{font:600 14px var(--sans);fill:var(--fg);text-anchor:middle}
.fig1 .e{font:400 11px var(--mono);fill:var(--fg3);text-anchor:middle}
.fig1 .l{font:400 11px var(--mono);fill:var(--fg2);text-anchor:middle}
.fig1 .a{stroke:var(--fg3);stroke-width:1;fill:none}
.fig1 .h{fill:var(--fg3)}
</style>
<g class="fig1">
<defs><marker id="fig1-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="8" markerHeight="8" orient="auto"><path class="h" d="M0 0 L8 4 L0 8 z"/></marker></defs>
<rect class="box" x="5" y="36" width="150" height="52" rx="8"/>
<text class="t" x="80" y="58">object</text><text class="e" x="80" y="76">Inbox</text>
<rect class="box" x="205" y="36" width="150" height="52" rx="8"/>
<text class="t" x="280" y="58">type</text><text class="e" x="280" y="76">Mailbox</text>
<rect class="box" x="405" y="36" width="150" height="52" rx="8"/>
<text class="t" x="480" y="58">part</text><text class="e" x="480" y="76">messages</text>
<rect class="box" x="605" y="36" width="150" height="52" rx="8"/>
<text class="t" x="680" y="58">dataset</text><text class="e" x="680" y="76">&lt;typeId&gt;_messages</text>
<path class="a" d="M155 62 H203" marker-end="url(#fig1-arrow)"/><text class="l" x="180" y="54">is a</text>
<path class="a" d="M355 62 H403" marker-end="url(#fig1-arrow)"/><text class="l" x="380" y="54">has</text>
<path class="a" d="M555 62 H603" marker-end="url(#fig1-arrow)"/><text class="l" x="580" y="54">owns</text>
<path class="a" d="M80 88 V138" marker-end="url(#fig1-arrow)"/>
<path class="a" d="M280 88 V138" marker-end="url(#fig1-arrow)"/>
<path class="a" d="M680 88 V138" marker-end="url(#fig1-arrow)"/><text class="l" x="722" y="118">served by</text>
<rect class="sub" x="5" y="140" width="150" height="52" rx="8"/>
<text class="t" x="80" y="162">property values</text><text class="e" x="80" y="180">one group per owner</text>
<rect class="sub" x="205" y="140" width="150" height="52" rx="8"/>
<text class="t" x="280" y="162">property definitions</text><text class="e" x="280" y="180">the columns</text>
<rect class="mod" x="605" y="140" width="150" height="52" rx="8"/>
<text class="t" x="680" y="162">module</text><text class="e" x="680" y="180">records · editor · chat</text>
</g>
</svg>

An object has exactly one type. The type contributes its property definitions — the columns the object can hold values for — and its **parts**: display units a client renders, each owning a dataset that a **module** serves. The `records` module serves a dataset whose schema you declare; the `editor` module serves block documents; the `chat` module serves messages.

On top of that an object can be filed under any number of **collections**, each adding a second group of columns and nothing else — no parts, no layout. That is the closest thing any has to inheritance, and it is deliberately flat: behaviour comes from the one type, extra columns come from the collections, and neither inherits from the other. [Part 2](properties.html) files the first object under a collection and [Part 4](apps.html) spells out the rules.

> **Why it matters.** Every one of these levels is a CRDT record in an end-to-end encrypted space on your own device. A type definition, a choice option, a part declaration and the data they govern all sync the same way, so a schema change made offline on one device converges with data written on another without a migration step or a server that sees plaintext.

## Before you start

The tutorial assumes a running server and one space, as in the [Quickstart](../quickstart/index.html). Every example is a `curl` call against it:

```bash
API=http://127.0.0.1:7001/v1
curl -fsS "$API/auth" | jq -e '.authorized == true'
SPACE=$(curl -fsS "$API/spaces" -H 'content-type: application/json' \
  -d '{"name": "Tutorial"}' | jq -er .id)
printf 'Space: %s\n' "$SPACE"
```

Keep `$SPACE` — every part uses it. A CLI example is the alternative to the HTTP call above it, not a second step: running both mints a duplicate or hits `409 type.xkey_conflict`.

<div class="cards">
<a href="objects.html"><strong>1. Objects</strong><span>Create an object, read it back, watch it change live.</span></a>
<a href="properties.html"><strong>2. Properties</strong><span>A type with typed columns — enough for a password manager.</span></a>
<a href="datasets.html"><strong>3. Datasets</strong><span>Ten thousand emails as records on one object.</span></a>
<a href="apps.html"><strong>4. Apps</strong><span>Parts, bundles, the sidebar and the catalog.</span></a>
</div>
