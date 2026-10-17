---
title: Full-text search
description: The BM25 lexical leg — always on, no external dependency, titles first, with phrase, prefix, anchor, require and exclude operators.
order: 10
---
# Full-text search

The lexical leg of the index is a BM25 full-text index over each document's text — an object's name in its own `name` field, everything else in `data`. It needs no embedder, no model download and no network, and it is the leg that answers first: a write is searchable within the indexer's 250 ms debounce.

## Pinning the mode

`hybrid` is the default; pass `"mode": "fts"` when you want lexical ranking only — exact terms, identifiers, error strings, names.

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "index.embedder_unavailable", "mode": "fts"}'
```

```bash
any search $SPACE "index.embedder_unavailable" --mode fts
```

The reply's `vectorStatus` is `skipped` — the echo of your own choice (`disabled` on a server with no embedder) — and `score` is the BM25 score.

## Query operators

The `query` string is parsed by the full-text engine, so four operators work inline; two more arrive as separate arrays.

| Operator | Where | Meaning |
|---|---|---|
| `"quoted phrase"` | in `query` | terms must appear adjacent, in order |
| `prefix*` | in `query` | trailing `*` matches any term starting with `prefix` — scored as one term over every completion, so a rare completion never outranks a common one |
| `"quoted pre"*` | in `query` | the phrase followed by a completion of its last word |
| `^anchored`, `^"anchored phrase"*` | in `query` | the clause at the document's first token |
| `require` | body array | every listed term (any of the forms above) must be present |
| `exclude` | body array | no listed term may be present |

```bash
curl -s http://127.0.0.1:2046/v1/spaces/$SPACE/search \
  -H 'content-type: application/json' \
  -d '{"query": "\"zeppelin disaster\" hind*",
       "require": ["1937"],
       "exclude": ["fiction"]}'
```

```bash
any search $SPACE '"zeppelin disaster" hind*' --require 1937 --exclude fiction
```

Bare terms combine with **OR** by default: a hit needs any of them, and more matching terms rank higher. A bare term next to a `require` is an optional boost, not a filter. Phrases and prefixes in `query` shape the lexical leg only; `require` and `exclude` bind every hit — vector hits (hybrid and pure `vector` mode) are post-filtered against the FTS index before fusion, so no mode returns a hit that violates them.

> **Note.** `index.search.defaultOperator: and` makes every bare term required. It is precise for short keyword input a client controls, but on natural-language queries it collapses recall (few documents contain *every* word — measured on BEIR, nDCG@10 fell from 0.66 to 0.02). Prefer phrases, `require` and `exclude`, which add precision without the cliff.

## Titles first

Every `hybrid` and `fts` reply opens with the objects whose **name** matches the typed words, before any content hit. The server splits the query with the index's own analyzer, treats the last word as unfinished unless the query ends in a space, and runs the tiers as separate queries scoped to the `name` field, most specific first, until `limit` objects are found:

| `match` | The name… | Query the server runs for `mario ka` |
|---|---|---|
| `exact` | is the typed words | `^"mario ka"`, kept when the name's terms equal the typed ones |
| `prefix` | starts with them | `^"mario ka"*` |
| `phrase` | contains them in order | `"mario ka"*` |
| `words` | holds every word, any order | `"mario" "ka"*` |

```bash
any search $SPACE "mario ka" --mode fts --limit 5
```

```json
{
  "hits": [
    { "scope": "basic", "objectId": "…", "dataset": "prop", "recordId": "name",
      "data": "Mario Kart", "dataTotal": 10, "score": 5.12, "match": "prefix" },
    { "scope": "basic", "objectId": "…", "dataset": "prop", "recordId": "name",
      "data": "Mario Kart 64", "dataTotal": 13, "score": 4.70, "match": "prefix" },
    { "scope": "basic", "objectId": "…", "dataset": "prop", "recordId": "name",
      "data": "Super Mario Kart", "dataTotal": 16, "score": 4.31, "match": "phrase" },
    { "scope": "basic", "objectId": "…", "dataset": "editor_blocks",
      "recordId": "win_…", "data": "…Mario Kart 8 Deluxe sold…", "dataTotal": 912, "score": 3.07 }
  ],
  "mode": "fts",
  "vectorStatus": "skipped"
}
```

A title hit is the object's name doc — `dataset: "prop"`, `recordId: "name"`, `data` the name — and carries `match`; a content hit never does. Within a tier the shortest names come first and ties list alphabetically. Title hits count toward `limit`, and a page they fill runs no content leg and embeds no query (`vectorStatus` reads `skipped`, or `disabled` on a server without an embedder), so the default mode answers a search box without the embedding wait while the typed words still name objects. The object's other records — the pages that mention the words — still follow as content hits. Typed quotes, stars and carets are text to the title block; no stop word is removed from it, so `the leg` finds *The Legend of Zelda*.

`"mode": "title"` runs the title block alone, one hit per object, and is the call a search box makes on every keystroke; it takes no `scopes` and no `passages` (`400 request.invalid_field`). `require` and `exclude` bind a title hit against the name alone — a term that occurs only in bodies empties the title block while the bodies still answer as content — and `filter` binds it like any hit. The block is not capped: a one-word query that names hundreds of objects answers titles only, and another typed word narrows it. The costly keystroke is a one-letter last word: the engine walks every posting of every term starting with that letter, body text included, and the name scope only decides which of them score; a client may wait for the second letter.

```bash
any search $SPACE "mario ka" --mode title
```

> **Why it matters.** A title is two to six words; BM25 over body text buries it under every page that repeats the words. Scoping the tiers to the name field and running them first gives a search box exact titles on top and every completion of an unfinished word, without a second index — the tiers are the any-store engine's own search-as-you-type recipe.

## Stop words

A small English stop-word list is stripped from the FTS-leg query before it runs (`index.search.stopWords`, default on). On a bag-of-words OR engine every common word matches a large share of the corpus and drags BM25 toward length and frequency noise; dropping them is a precision win for conversational queries and neutral on terse ones. Two safeguards: an all-stop-words query is left unchanged rather than emptied, and stripping is skipped whenever `query` contains a `"`, so phrases survive intact. The vector leg always sees the full query.

## Scopes

`scopes` narrows the search to a set of slugs; empty means all. Scopes are an open set (`[a-z0-9_-]`, up to 64 characters) — an unknown but well-formed scope returns no hits, a malformed one is `400 search.bad_scope`.

| Scope | What lands there |
|---|---|
| `basic` | editor windows, object names and descriptions, runtime-dataset records |
| `chat` | chat messages |
| `props` | user property values — **full-text only**, never embedded |
| any other slug | whatever a property's `meta.index` or a dataset's `x-search.scope` minted |

Property values are self-describing entries — `"Publisher: Gollancz"`, `"Score: 9"` — so searching by property *name* works too, and bare numbers carry context. A content-only search passes `scopes` without `props`.

```bash
any search $SPACE "gollancz" --scopes props --mode fts
```

## BM25 tuning

Three engine dials are exposed under `index.search.*`; all default to 0, meaning "engine default", so an absent block changes nothing.

| Key | Effect | Engine default |
|---|---|---|
| `bm25B` | length normalization | 0.75 |
| `bm25K1` | term-frequency saturation | 1.2 |
| `titleWeight` | BM25F boost for the title field (runtime datasets with an `x-search.title`) | 1.0 |

All three are part of the index definition fixed when a space's index is created — `bm25B`, `bm25K1`, and `titleWeight` both as the title field's presence and as its value — so any change takes a rebuild (remove the index directory); the engine refuses to open an index under a different definition. The measured sweep found no non-zero value that helped on the reference corpus, so the knobs stay neutral; see [Evaluation](evaluation.html).

## Reading the scores

BM25 scores are comparable only within one block of one response — the title block and the content block score on different footings — and never with the cosine or RRF scores of the other modes. Rank on them, don't threshold.

## Errors

| Status | Code | When |
|---|---|---|
| 409 | `index.disabled` | the server runs with `index.enabled: false` |
| 400 | `search.bad_mode` | `mode` is not `hybrid`, `fts`, `vector` or `title` |
| 400 | `search.bad_scope` | a scope slug is malformed |
| 400 | `request.invalid_field` | `scopes` or `passages` with `mode: "title"` |
| 503 | `index.not_ready` | the space's full-text index is being rewritten in place after an update, or not created yet — retry; `index.fts_upgrade.<spaceId>` on the [process view](../notifications/processes.html) shows the rewrite |

See [Hybrid ranking](hybrid.html) for how this leg is fused with the vector leg, and [How indexing works](indexing.html) for what makes it into the index in the first place.
