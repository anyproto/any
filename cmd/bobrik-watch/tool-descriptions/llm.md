## Tool Description

Minimal text→text LLM interface for tools that need a one-shot generation step (summarize, classify, rewrite, extract a value). `llm.ask` runs a single completion; `llm.askBatch` runs many in parallel via `fetchBatch`. Both route through the `summarize` tier from `config@v1`, so the default model is one config knob — change `config.TIERS.summarize` to retune every caller at once.

Never throws. Errors surface as `{ ok: false, text: null, error: "..." }` so callers can branch without try/catch wrapping every LLM call.

For multi-turn / native-tool-use calls, the underlying `chat` interface is available via `createLLM()` from `import { createLLM } from "llm@v1"` — not exposed at the kernel level.

## Tool Schema

### ask(prompt) [getter]

Run a single completion via the default tier.

**Input:** `prompt` (string) — the full prompt text. The helper does no templating; pass whatever you need verbatim.

**Output:** `{ ok, text, error }`
- `ok` (boolean) — `true` only if a non-empty response came back.
- `text` (string | null) — the model's reply, or `null` on failure.
- `error` (string | null) — error message when `ok === false` (e.g. `"empty response"`, provider HTTP status, missing API key).

**Examples:**
```js
var r = llm.ask("Summarize in one sentence: " + body);
if (!r.ok) return { ok: false, error: r.error };
return { ok: true, summary: r.text };
```

### askBatch(prompts) [getter]

Run many completions in parallel via a single `fetchBatch`.

**Input:** `prompts` (string[]) — list of prompts. Empty array returns `[]`.

**Output:** `Array<{ ok, text, error }>` — one entry per prompt, in input order. Per-item shape matches `ask()`. Partial failure is normal: some items can be `{ ok: true, ... }` while others are `{ ok: false, ... }`.

**Examples:**
```js
var rs = llm.askBatch([
  "Classify as POSITIVE or NEGATIVE: " + a,
  "Classify as POSITIVE or NEGATIVE: " + b
]);
var classes = rs.map(function(r) { return r.ok ? r.text.trim() : "UNKNOWN"; });
```
