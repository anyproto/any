# Meeting-transcript enrichment — plan

> Source note: `any://bafyreighxgqlpqfq2sutnmnxyu7kknr6mdb4iqp3vkvm3gzjjb6ms3yxje.3nv5f5wmb6wkc/bafyreifg6easxhnvrturjdsdouu3qlzsiazydibkuujnar4y6fl7w2yrma`

A program that reads a meeting transcript, derives knowledge from it, and
reconciles that knowledge against the space — **creating** new objects and
**enriching** existing ones, with a provenance trail back to the transcript.

The original note framed the output as **actions as data**: a map
`object -> [update]` where each update carries a `sourceBlockLink`. That framing
is the key — because actions are data, we can run the engine in **dry-run** mode
and inspect the proposed map without applying anything.

---

## 1. The real shape: reconciling two corpora, not mapping blocks

The transcript is one body of knowledge; the space is another. The task is **not**
"for each block, find a matching object." Information is rarely block-aligned:

- a single fact can be argued across many scattered blocks,
- a fact can be *derived* from blocks that never state it outright (someone
  proposes X, someone agrees 20 turns later → "we decided X"),
- a fact can be revised within the transcript itself (proposed Q2, later
  corrected to Q3 — only the final state matters).

So the unit of work is a **knowledge unit** (a decision, a fact about an entity,
a task, a relation) — derived by *synthesis*, not extraction. Provenance is
therefore a **set** of supporting blocks per claim (`sourceObject#b1,b7,b12`),
not a single link.

## 2. The key inversion: grounding drives extraction

The iterative process falls out of one move: **don't extract-then-search,
ground-then-extract.**

1. **Skim** the transcript cheaply to discover what it's *about* — the
   entities / threads / topics (people, projects, docs, decisions). Metadata
   only; stays in the orchestrator's context.
2. For each entity, **load its current state from the space** (resolve via
   cheap search → the existing object's props + body, or "none").
3. Do a **focused re-read** of the transcript *with that state in hand*: "given
   Project X currently has these properties and this body, what does the whole
   transcript say that's **new, changed, or contradictory** about Project X?"

Step 3 naturally (a) gathers all the scattered blocks about X, (b) synthesizes
rather than copies, and (c) extracts **deltas** — only what the space doesn't
already know, which solves dedup for free.

## 3. The iteration is a worklist, not a single pass

```
frontier = entities found in the cheap skim
known, model = {}, {}
while frontier not empty and budget remains:
    e        = frontier.pop()
    target   = resolve(e)              # cheap search first; deep search on ambiguity/miss
    state    = load(target)            # current object, or empty -> "new"
    delta    = extract_delta(transcript, e, state)   # focused synthesis re-read
    frontier += entities newly referenced by delta   # <- the iteration
    model[e] = {target, delta, supportingBlocks, outcome}
reconcile(model) -> updates + new objects + conflicts
```

It iterates because grounding reveals more: extracting Project X's delta surfaces
a person or a related doc you hadn't loaded, which goes back on the frontier. A
detected conflict can trigger a re-read. It converges when no new entities appear
and no conflicts remain (or budget runs out → best-so-far, RLM-style honesty).

## 4. Make both sides queryable environments

The elegant version treats the **transcript itself as a corpus you `ask`**, not a
string you read linearly. The loop becomes a dialogue between two queryable
environments:

- ask the **space**: "do we have an object about X? what do we know?"
- ask the **transcript**: "what does this meeting say about X that's new?"

The orchestrator never holds the transcript or the objects in context — only the
questions and the distilled answers with citations. This is the RLM offload
principle applied to *both* corpora.

## 5. Outcomes are richer than create/enrich

Each knowledge unit resolves to one of:

- **new** — mint an object (no match),
- **enrich** — add prop/body to an existing object,
- **conflict** — transcript contradicts current state (Q2→Q3) — its own outcome,
  needs flagging/approval, not silent overwrite,
- **redundant** — already known, drop.

The original note only had new/enrich; conflict and redundant are where the
provenance trail earns its keep ("this property changed because of *this*
meeting").

## 6. Tooling we already have (bobrik)

| Capability | Tool | Role in this engine |
|---|---|---|
| Cheap hybrid recall (FTS+vector), zero tokens | `semsearch` / `POST /search` | bulk entity resolution |
| Deep recursive recall + grounded synthesis | RLM `search` / `ask` | escalate on ambiguity / coverage / synthesis |
| Isolated sub-loop, same tools, clean context | `subagent.delegate` | shape one target without flooding root context |
| Paged reads, type catalog | `anyHelper.getObjects` / `describeType` | load object state, pick types for creates |

Resolution default: **semsearch-first, RLM-escalate** on low confidence — cheap
by default, expensive precisely where matching is hard.

## 7. Data model (foundational, gates the apply path)

Two new primitives, needed only once we move from dry-run to apply:

1. **Block-addressable `any://` links** — `any://<space>/<obj>#<blockId>` where
   `blockId` is the stable derived `editor_blocks` record id (survives
   reordering). Provenance links a *set* of blocks.
2. **`SourceLinks` collection** — provenance map on the target object. Leaning
   toward a new built-in dataset `source_links` (chat/editor pattern), one
   record per provenance entry: `{ kind, summary?, targetPath?, value?,
   sourceLink[], createdBy }`. Idempotent on `(sourceBlockLink, target,
   targetPath)`.

## 8. Phasing

- **Phase 0 — primitives**: block links + `source_links` data model. *(later)*
- **Phase 1 — transcript as object**: markdown blocks, immutable (UI-only for
  now). Create-from-text + Participant type + calendar sync **deferred**.
- **Phase 2 — the engine** (this task): the entity-anchored worklist, dry-run
  first (output the action map as data), apply + provenance later.
- **Phase 3 — UI**: render `source_links`, source-on-hover for properties.

## 9. First step (now): dry-run engine — a deployable `any` program

Built as a real bobrik program that runs in bobrik's own kernel (no second
environment, no module loader, reuses `llm`/`semsearch`/`search`/`anyHelper`):

- **Program:** `cmd/bobrik-watch/js/system/js/meetingEnrich@v1.js` — deployed
  into the bao space by the normal bootstrap (object id
  `bafyreigodw7vw363fugsvpklzwg6ac5jjtmfkulbpjcm5me6a7py4iutl4`).
  `main({spaceId, transcript|transcriptId, apiBaseUrl})`:
  1. extract knowledge units (synthesis, multi-block) via `llm.chatUntraced`,
  2. ground each entity against the target space via `semsearch`,
  3. reconcile units + candidates → the action map (`new|enrich|conflict|
     redundant`). **No writes** (dry run). Returns `{ok, tally, units_detail,
     grounded, actions}`.
  v0 shortcut: single grounding pass + one batched reconcile call (not yet the
  per-entity worklist loop of §3; RLM `search`/`ask` escalation is a TODO).

- **Run surface:** `POST /run` on the bobrik control API (`:7010`, added in
  `cmd/bobrik-watch/run_server.go`). Body:
  `{ "program": "meetingEnrich@v1" | "programId": "<id>", "spaceId": "<target>",
  "args": { "transcriptId": "<obj>" | "transcript": "<md>" } }`. It spins the
  same runtime path a chat message uses (`NewSobekRuntime` →
  `SetupAnySDKDirtyRuntime` → `EvalToString`), passes `spaceId`/`apiBaseUrl`
  into the program, and returns its `main()` result as JSON. Generic — runs any
  deployed program by name or object id against any space.

Run it (once a real transcript exists in the dev space):

```bash
curl -s -X POST http://127.0.0.1:7010/run -H 'Content-Type: application/json' \
  -d '{"program":"meetingEnrich@v1","spaceId":"<dev space id>","args":{"transcriptId":"<transcript obj id>"}}'
```

Because the dev space currently holds our dev tasks/discussions (not meeting
content), the expectation is: **enrich ~nothing, propose many creates** — which
is exactly the signal we want to read off the dry-run.

## 10. Open forks

1. **Convergence control** — frontier-until-stable vs fixed entity/turn budget
   with best-so-far. Leaning budgeted + honest "wrapped up".
2. **Conflict handling** — always surface for approval vs auto-supersede w/
   provenance for low-stakes fields.
3. **How agentic** — scripted worklist (deterministic, debuggable) first; free
   RLM loop as an escalation mode.
4. **`SourceLinks` placement** — built-in dataset (recommended) vs plain props.
5. **Create autonomy** — auto-apply-then-show enrichments; propose-only for new
   objects/types.
