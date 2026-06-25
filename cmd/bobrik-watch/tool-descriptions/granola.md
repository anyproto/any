# granola

## Tool Description

Connects to **Granola** (AI meeting notes) over its official read-only public REST API (`https://public-api.granola.ai/v1`). Surfaces your meeting notes, AI-generated summaries, attendees, and full call transcripts so the agent can answer questions grounded in real meeting content — "what did we decide with Acme last week?", "summarise my 1:1s with Sarah this quarter", "who owns the action items from Tuesday's planning call?". Read-only: the Granola public API has no write endpoints, so nothing here mutates your Granola workspace. Tagged `integration`.

**Plan gate — read this first.** Granola API keys can only be minted on a **Business or Enterprise** plan. Free and Basic users have no "API keys" panel and cannot get a `grn_` key, so this connector cannot reach them via the official path (a local-cache fallback exists in the community but needs a filesystem host function this runtime doesn't have yet — not supported here). Confirm the user is on Business/Enterprise before sending them to set up a key.

**Credential setup.** The user creates a key in the **Granola desktop app → Settings → Connectors → API keys** (it starts with `grn_`; for a second-brain ingest they want the *Personal notes* scope). Save it to `config@v1` under `GRANOLA_API_KEY` — the user can paste it in chat and the agent stores it via the anyPrograms tool. Every request sends `Authorization: Bearer grn_…`. If the key is missing, methods return a clear "not connected" error naming the Settings path and the plan gate; on a bad key the API returns 401/403, which surfaces the same guidance.

**Usage guidance.** Ingest is poll-based — Granola has no webhooks — so pull deltas by passing `createdAfter` (an ISO-8601 watermark) to `listNotes` and keeping the newest `createdAt` you've seen. A note only appears once its AI summary + transcript have finished generating, so a freshly-ended meeting can briefly 404 on `getNote` — retry shortly. Rate limits are ~5 req/s (25 burst); the connector backs off on 429 automatically. Page sizes default small and hard-cap at 100; `listNotes`/`listFolders` accept `maxItems` to auto-follow the cursor (bounded), or return one page plus `nextCursor` for manual paging. Fetch transcripts lazily (`getNote` with `includeTranscript: true`) — they're large. Privacy note: transcripts contain other attendees' speech; ingesting them pulls full meeting content into local-first storage.

**Endpoint caveat.** The Granola public API is young; the exact paths, the pagination cursor field, and response field names in this connector are from a 2026-06 verification. Confirm them against Granola's live API docs on first use and adjust if they've drifted.

## Tool Schema

### verify() [getter]
Connectivity check + key validator. Pulls a 1-item note page and reports whether the configured key works. Run this right after the user saves a key.
- Returns: `{ ok: true, connected: true }` on success, or `{ ok: false, error, status }` (401/403 ⇒ bad key / plan gate; no key ⇒ "not connected" with setup instructions).

### listNotes(opts) [getter]
List meeting notes (newest-first per the API), cursor-paginated. A note only surfaces once its AI summary + transcript exist.
- opts: `{ createdAfter?: string /*ISO-8601 watermark for incremental pulls*/, folderId?: string, cursor?: string, limit?: number /*≤100, default 25*/, maxItems?: number }`
- When `maxItems` is set, follows the cursor across pages (bounded) and returns up to that many rows; otherwise returns one page plus `nextCursor`.
- Returns: `{ ok: true, notes: [...], nextCursor }`.
- Example: `granola.listNotes({ createdAfter: "2026-06-01T00:00:00Z", maxItems: 50 })`

### getNote(opts) [getter]
Fetch one note with its summary and (optionally) the raw transcript. Accepts a bare id string too.
- opts: `{ id: string, includeTranscript?: boolean }` (or just the id string).
- Transcript is an array of `{ speaker, text }` segments; flatten to `speaker: text` lines before storing.
- A freshly-ended meeting may 404 until its summary/transcript generates — retry shortly.
- Returns: `{ ok: true, note: {...} }`.
- Example: `granola.getNote({ id: "note_123", includeTranscript: true })`

### listFolders(opts) [getter]
List accessible folders (hierarchy via `parent_folder_id`), cursor-paginated. Useful for filtering `listNotes` by `folderId` or mapping Granola structure into the space.
- opts: `{ cursor?: string, limit?: number /*≤100, default 25*/, maxItems?: number }` (same paging contract as `listNotes`).
- Returns: `{ ok: true, folders: [...], nextCursor }`.
- Example: `granola.listFolders({ maxItems: 100 })`
