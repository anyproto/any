## Tool Description

Long-term memory over the agent's persistent store. **Memory content is NOT in your prompt by default** — `amemory.search(...)` is how you read it. The "Earlier context, compressed" prepend you see at turn start is chat-history summarisation, not the full store; preferences, decisions, and lessons from past sessions never enter your prompt until you search.

Treat `amemory.search` as a reflex before acting: before non-trivial answers, before create/edit/write moves, before committing to a design choice. One search costs ~1s; skipping it risks contradicting a stated preference or repeating a known mistake.

**All read methods return compact records** — `{id, name, kind, period?, context, keywords, tags, score?}`. No full markdown body. If you need a body, call `anyHelper.getObject(id)` on the hit you care about.

### Shape cheat sheet (commonly confused)

- **Write**: `amemory.addMemory(text, {category: "preference", ...})` — **singular** `category`, REQUIRED.
- **Search filter**: `amemory.search(query, {categories: ["preference"]})` — **plural** `categories`, optional whitelist.

### When to use which method

| Situation | Verb |
|---|---|
| Look up a past topic / name / ID, or check preferences before acting | `amemory.search(query, {categories: [...]?})` |
| User gives a date range | `amemory.search(query, {periodFrom, periodUntil})` |
| Walk compressed history chronologically (no specific query) | `amemory.listChatChunks(opts)` |
| Expand a chat_chunk hit with ±N neighbours | `amemory.getNeighbours(chunkId, opts)` |
| Save something durable this turn produced | `amemory.addMemory(text, opts)` |
| Check what categories exist (only when needed) | `amemory.listCategories()` |

## Tool Schema

### createAMemory(client, opts)
Initialize the memory system. Returns a memory manager object with search/store methods.
- client: anyHelper client instance
- opts: configuration options
