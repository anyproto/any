# Scalable tool injection — tiered module docs in the system prompt

## Problem

The agent's system prompt (observed: ~28k chars, 21,930 cached tokens per
conversation) is dominated by the "Module reference" section: the FULL
`## Tool Description` body of every `any_tool` program in the space, injected
verbatim by `_buildToolsPromptSection`. Measured across the current asset tree:
**50,133 chars (~12.5k tokens) of description text**, average ~2,500 chars per
tool. Every new connector grows every future conversation's prompt by its full
description — linear growth with the tool set, paid in cache writes per
conversation, attention dilution, and a higher floor for the smallest turn.

Method BODIES were already tier-2 (fetched via `describeMethod` at first use);
descriptions were the remaining always-inline bulk.

## What shipped (tier-1/tier-2 split)

- **Tier 1 — always in the prompt:** for every module, the heading + the
  method-signature list, plus:
  - **pinned core** (`anyHelper`, `convmemory` — the modules nearly every
    conversation touches): the full description, unchanged;
  - **everything else:** `_toolSummary(description)` — the first paragraph,
    hard-capped at 400 chars — with a
    `_(summary — call `NAME.describe()` for the full doc)_` pointer whenever
    anything was elided.
- **Tier 2 — on demand, zero HTTP:** `facade.describe()` on every kernel
  global returns the full description from `__toolDocs` (already shipped into
  the kernel at boot; the summary trim happens only at prompt-render time).
  The prompt's "Discovering module and method docs" section instructs the
  model to `describe()` a module on first use when the summary leaves doubt,
  mirroring the existing `describeMethod` discipline.

Measured effect (current 24 tools): description text drops
**50,133 → 15,090 chars (~12.5k → ~3.8k tokens)**; the marginal prompt cost of
a NEW tool drops **~2,500 → ~300 chars**. The tool set can now grow ~8× before
the description section returns to its old size.

## Authoring contract

Tool-description md is summary-first: the OPENING paragraph of
`## Tool Description` is what the model sees in every prompt — write it as a
self-contained "what is this / when to reach for it" statement (every current
tool already reads this way). Detail — auth flows, limits, error tables,
recipes — goes in later paragraphs, which surface via `describe()`.

## Considered and deferred

- **Per-message relevance selection** (embed descriptions, semsearch the user
  message, inject top-k full docs): better attention targeting, but varying
  the system prompt per message breaks the prompt-cache prefix — the moving
  message-tail breakpoint (see `_stampCacheTail`) makes the static prompt
  cheap, and a per-message tool section would forfeit that. Viable later as
  an injection into the USER message ("possibly relevant tools: …"), which
  leaves the cached prefix intact.
- **Explicit `## Summary` section in the md format**: gives authors direct
  control instead of the first-paragraph convention, but requires touching
  both splitters (`toolmd.go::splitToolMarkdown` + anyPrograms
  `_splitToolMarkdown`) and re-syncing every space. The first-paragraph
  convention gets the same result with zero format churn; revisit if
  summaries start needing to diverge from openings.
- **Anthropic tool-search / `defer_loading`**: doesn't apply — the agent
  exposes ONE Anthropic tool (`run_cell`); modules are kernel globals, not
  API tools.

## Tests

`tests/js/toolsummary_test.js` — first-paragraph extraction + cap, pinned
full-injection, non-pinned summarization with `describe()` pointer,
no pointer when nothing was elided, signatures always listed.
