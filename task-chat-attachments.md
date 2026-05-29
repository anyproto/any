# Task: Render chat-message attachments in any-ui

## Background

Chat messages on the `any` server (`/v1/spaces/:spaceId/objects/:objectId/chat/messages`) now carry one new optional field:

- **`attachments`** — a map keyed by short opaque ids. Each value has:
  - `type`: `"link"` or `"image"` (open enum; future values likely)
  - `link`: a URL-shaped string. For `any://spaceId/objectId` links the UI should open the object; for `image` types the link may be any URL the UI can render.

Wire shape on `ChatMessage`:

```json
{
  "id": "...",
  "creator": "...",
  "createdAt": 1717000000,
  "text": "✅ here are the docs you asked for",
  "attachments": {
    "a1": { "type": "link",  "link": "any://spaceid/objectid" },
    "a2": { "type": "image", "link": "https://example.com/x.png" }
  }
}
```

`ChatSendRequest` accepts the same `attachments` field on POST. Server-side validation:

- Attachment id: 1–64 chars, `[A-Za-z0-9_-]+`
- `type`: required, non-empty string, ≤ 64 bytes
- `link`: required, non-empty string, ≤ 2 KiB
- Up to 32 attachments per message

Attachments are stored on the message record and are immutable post-create (no edit/patch endpoint).

## Files of interest

- `src/lib/api/chat.ts` — extend `ChatMessage` and `ChatSendRequest` types
- `src/components/chats/ChatView.tsx` — message rendering; add an attachments block beneath the text
- `src/lib/sync/stores/chat.test.tsx` — extend fixtures if needed

## UI requirements

1. **Type extension** — add `attachments?: Record<string, ChatAttachment>` to `ChatMessage` and `ChatSendRequest`, where:
   ```ts
   interface ChatAttachment {
     type: string;           // open enum; switch on "link" / "image", fallback for unknown
     link: string;
   }
   ```

2. **Rendering** — beneath the message text, render each attachment sorted by attachment id (stable):
   - `type === "image"` → `<img src={link}>` with sensible max-width / lazy load.
   - `type === "link"` → clickable link. If `link` starts with `any://`, route internally to the object detail view; otherwise open in a new tab with `rel="noopener noreferrer"`.
   - Unknown type → render the raw `link` as a plain anchor so users still see something.

3. **Compose** — no UI for adding attachments in v1; only agent-sent messages will populate them. Compose path can keep the existing text-only form.

## Out of scope

- Editing attachments (server doesn't allow it).
- Uploading files (no file API yet; see `docs/07-roadmap.md`).
- Inline `any://` link extraction from message text — that legacy heuristic is gone; attachments are the structured replacement.

## Related changes shipping with this

- `any` server: validates + stores attachments in `internal/chat/handler.go`, surfaces them via `internal/api/chat.go`.
- `bobrik-watch`: `chatReply({ text, attachments })` is the new agent-side surface; the JS runtime forwards the shape verbatim to the server.
- `any` web UI (`internal/server/web/index.html`): dumps the attachments JSON as a `<pre>` block beneath the message — intentionally dumb. The "proper" rendering lives in any-ui.
