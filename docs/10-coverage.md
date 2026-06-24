# 10 — anyHelper ↔ server endpoint coverage

Maps the server's 74 HTTP endpoints (`internal/server/docs/swagger.json`) to the
JS client surface (`cmd/bobrik-watch/anyHelper.js`). anyHelper is the agent's
window onto the server, scoped to **one space, content operations** — so
space/account lifecycle, sharing (ACL/invites), and diagnostics are
deliberately out of scope (they're admin/host concerns, reachable via the raw
`client.api(method, path, body)` escape hatch if ever needed).

## Covered

| Area | Endpoints | anyHelper |
|------|-----------|-----------|
| Objects | `POST /objects`, `DELETE /objects/:id` | `createObject` / `createCollection`, `deleteObject` |
| Properties | `GET /properties/:o`, `POST /properties/:o/set/:typeId` | `getObject`, `createObject`/`updateObject` (nested type groups: `{ book: { author } }`) |
| Query | `POST /objects/query`, `POST /query` | `getObjects` (cross-object AND per-object dataset modes) |
| Datasets | `POST /modify`, `POST /delete-records` | `setRecord`, `deleteRecord` |
| Editor (md) | `GET`/`PUT`/`POST …/editor/markdown[/append]` | `getObject`, `updateObject`, `appendToObject`, `editObject` |
| Types | `GET`/`POST /types`, `GET /types/:id/properties`, `POST …/properties` | `getTypes`/`createType`, `getProperties`/`getProperty`/`describeType` (catalog-backed) |
| Members (read) | `GET /members`, `GET /members/:identity` | `listSpaceMembers`, `getSpaceMember` |
| Chat (write) | `POST …/objects/:o/chat/messages` | `sendChatMessage` (auto-stamps the `agent` group; reads/list via `getObjects`) |
| Spaces (list) | `GET /spaces` | `listSpaces` |
| Programs | (via `/modify` + `/query` on `program_*` datasets) | `listPrograms`/`getProgram`/`saveProgram`/`saveTool`/`runProgram` |
| Agent data layer | `POST …/agent/turns`, `POST …/agent/chunks`, `GET /agent/brain`, `POST`/`PATCH`/`DELETE /agent/memory[/:itemId]` | via `client.api(...)` from JS (convmemory module); reads via `getObjects({objectId, dataset})` with `agent_turns` / `agent_chunks` / `agent_memory_items` — see `docs/11-agent-memory.md` |

## Added in this pass

- `deleteRecord(objId, dataset, ids)` → `POST /delete-records` — completes the
  dataset CRUD (setRecord / deleteRecord; reads via getObjects dataset mode).
- `getObjects` gained `filter`/`sort`/`limit`/`offset` → the full
  `POST /objects/query` surface (see `docs/09-query.md`).
- `sendChatMessage(chatObjectId, text, opts)` → `POST …/chat/messages` — lets a
  JS program post to any chat. Auto-stamps the `agent` group `{ name, done:true }`
  (name defaults to `"bao (<your display name>)"`, resolved once from
  `GET …/members/me`; `opts.agent = null` opts out). Listing chats is
  `getObjects("chat")`; reading history is `getObjects({objectId, dataset:
  "chat_messages"})` — no dedicated wrappers, the query primitive already covers
  both.

## Deliberately not wrapped (out of agent scope)

Reachable via `client.api(...)` if a concrete need appears; no speculative
surface added.

| Endpoints | Why deferred |
|-----------|--------------|
| `GET /account`, `PUT /account/metadata` | host/account admin, not per-space content |
| `POST /spaces`, `GET`/`DELETE`/`PATCH /spaces/:id`, `POST /spaces/join` | space lifecycle — the client binds to an existing space at `createClient` |
| `POST /spaces/query`, `/spaces/query/subscribe` | account-wide space-list query/subscribe — host/UI concern; the JS agent operates within one bound space |
| `GET /datasets`, `GET /spaces/:id/datasets` | dataset-schema discovery — host/tooling concern; the agent already knows the datasets it writes |
| `POST /acl/*` (9), `…/invites` (5) | sharing/membership admin — host concern |
| `GET /members/me`, `/members/requests`, `/members/subscribe` | membership admin/streaming |
| `GET /sync-status*` (4), `GET /debug*` (2) | diagnostics — host/ops, not agent logic |
| `POST /objects/derive` | deterministic-id creation — niche; no caller |
| `POST /objects/aggregate`, `POST /aggregate` | aggregation pipelines (`docs/14-aggregation.md`) — reachable via `client.api(...)`; add a `getAggregate` wrapper when an agent program actually needs server-side rollups |
| `POST`/`PATCH`/`DELETE …/editor/blocks` | atomic block writes — the markdown bridge (`append`/`edit`/`PUT`) covers agent needs; blocks reachable via `api()` and read via `getObjects(null,{objectId:o,dataset:"editor_blocks"})` |
| chat `PATCH/DELETE …/chat/messages[...]`, reactions | edit/delete/react have no in-JS caller yet (the reply send is wrapped by `sendChatMessage`; the host-side bobrik-watch path still posts its own replies directly) |
| `*/subscribe` (SSE) | live streams are a host/Go concern; the JS client is request/response |
| `GET /health`, `POST /shutdown` | lifecycle, host-side |

## Notes

- The `account`/`device` property scopes and `attach`/`detach`/type-delete/
  type-prop-delete endpoints are server-side `501 sdk.not_implemented`
  (`docs/07-roadmap.md`) — nothing to wrap.
- Chat-from-JS is wrapped (`sendChatMessage`). If atomic block editing or chat
  edit/delete/react becomes a real agent need, add thin wrappers (`blockCreate`
  etc.) following the dataset-helper pattern; they're a few lines each over
  `api()`.
