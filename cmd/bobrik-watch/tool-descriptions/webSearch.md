## Tool Description

Grounded web search via Gemini API + Google Search. Each query returns a short synthesized answer (4–8 sentences) with source URLs. Use for quick factual lookups, grounding claims with sources, sanity-checking recent events, pulling product/API docs, comparing a few options. **Not** for multi-step research that needs sub-topic decomposition or structured synthesis — for that, run multiple targeted queries and aggregate yourself.

Returns an array of strings, one entry per query (max 5). Each entry is `[N] query\nprimary-url\n\nanswer\n\nSources: ...`. Cite specific `[N]` entries when relaying to the user.

### How to craft good queries

- **Phrase queries as questions or specific lookups** — Gemini synthesizes an answer per query. `"What's the current any-sync CRDT model and which library does it use?"` beats `"any-sync crdt"`.
- **Run multiple queries for different angles.** Each query becomes one independently grounded answer. 2–3 angled queries give a richer picture than one broad question. Example angles: architecture, comparisons, recent changes.
- **Add year or version terms** when recency matters (`"glm-4.6 benchmarks 2025"`, `"react server components stable release"`).
- **Quote exact phrases** you want grounded literally (`'"z-ai/glm-5.1" context length'`).
- **Prefer site-specific queries** when you already know where the answer lives (`"openrouter.ai max_completion_tokens parameter glm-5.1"`).

## Tool Schema

### search(query, opts?) [getter]
Run one or more grounded web queries. Returns an array of stringified answers (max 5 queries per call).
- query: string or array of strings — each becomes one independently synthesized answer
- opts: search options (reserved)
