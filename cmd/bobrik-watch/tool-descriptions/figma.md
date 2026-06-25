# figma

## Tool Description

Read-only **Figma** connector (tagged `integration`). Pulls the second-brain-useful slices of a Figma design file — account identity, lightweight file metadata, threaded **comments**, and the extracted **TEXT-layer content** of a file — into reach of the agent. It talks to the Figma REST API (`https://api.figma.com/v1`) with a Personal Access Token.

**Credential setup.** Auth is a Figma **Personal Access Token** sent in the Figma-specific `X-Figma-Token` header (NOT `Authorization: Bearer` — that header is OAuth-only). On first use, if no token is stored, ask the user to create one at **Figma → account menu → Settings → Security → Personal access tokens → Generate new token**, ticking these read scopes: **`current_user:read`** (account identity), **`file_metadata:read`** (the lightweight `/meta` endpoint), **`file_content:read`** (file tree → text extraction), **`file_comments:read`** (comments). The token is shown exactly once — copy it immediately. Store the pasted value in `config@v1` as **`FIGMA_TOKEN`**. A token created without a given scope will `403` on just that endpoint (e.g. comments) while others still work — on a 403, re-prompt the user to regenerate with the missing checkbox.

**What to ingest, and what NOT to.** Prioritize, in order: (1) **comments** — the human discussion threaded onto designs, the richest and cheapest second-brain signal; (2) **file metadata** — name / last-modified / editor type, for "what files exist and when did they change"; (3) **text layers** — the readable copy (`characters`) of every `TEXT` node, extracted out of the structural tree. **Never ingest the raw node tree**: a full `GET /files/:key` on a design-system file is multi-megabyte and deeply nested — it blows context and burns the rate budget. `getFileText` always passes a `depth` cap and returns ONLY the extracted text nodes, never the tree. Prefer `getFileMeta` for "has it changed?" change-detection and re-extract text only when `lastModified` advanced.

**Rate limits + usage.** Figma's per-minute rate limits are tight (heavy file reads can be ~15 req/min on full seats, single-digit on lighter seats/plans, since the Nov-2025 change). The connector **serializes** calls and, on `429`, honors the `Retry-After` header and backs off (bounded retries) — but a naive "ingest everything" loop will still 429. Work one file at a time. **Figma has no "list all my files" API**, so the user supplies a file URL or key; `getFileMeta` / `listComments` / `getFileText` all accept either a raw file key or a pasted `figma.com/(file|design|board)/:key/...` URL (auto-parsed). The `listProjectFiles` / `listTeamProjects` methods need the `projects:read` scope, which is **private-OAuth-app-only** — a plain PAT generally 403s there; they're included for completeness but expect them to fail on a PAT. Every method returns a consistent `{ok, ...}` / `{ok:false, error, status}` shape.

## Tool Schema

### me() [getter]

Return the current Figma account identity; doubles as the token validator. Requires `current_user:read`.
- params: `{}`
- returns: `{ok, id, handle, email?, imgUrl}` — or `{ok:false, error, status}`.

```js
figma.me(); // {ok:true, id:"123", handle:"Ada", email:"ada@x.com", imgUrl:"..."}
```

### getFileMeta(fileKey) [getter]

Lightweight file metadata via the `/meta` endpoint — cheap; use for "exists / has it changed?" checks before pulling text. Requires `file_metadata:read`.
- params: `fileKey` (string, required) — a raw file key OR a full `figma.com` file URL (auto-parsed).
- returns: `{ok, fileKey, name, lastModified, editorType, thumbnailUrl}` — or `{ok:false, error, status}`.

```js
figma.getFileMeta("https://www.figma.com/design/abc123/My-Spec");
```

### listComments(fileKey) [getter]

All comments on a file, markdown-rendered (`as_md=true`) and threaded via `parentId`. The highest-value ingest target. Requires `file_comments:read`.
- params: `fileKey` (string, required) — file key or `figma.com` URL.
- returns: `{ok, fileKey, count, comments: [{id, author, message, createdAt, resolvedAt, parentId}]}` — or `{ok:false, error, status}`. Consider dropping `resolvedAt != null` comments before ingesting — they're closed discussion.

```js
figma.listComments("abc123").comments[0]; // {id, author, message, createdAt, resolvedAt, parentId}
```

### getFileText(fileKey, opts?) [getter]

Walk the **depth-limited** node tree and return every `TEXT` layer's content. Never returns the raw tree. Requires `file_content:read`.
- params: `fileKey` (string, required) — file key or `figma.com` URL; `opts` (object, optional): `depth` (number, default 8) — tree-traversal cap passed straight to the API to bound payload size.
- returns: `{ok, fileKey, name, lastModified, depth, textNodes: [{nodeId, page, name, text}]}` — or `{ok:false, error, status}`. `page` is the enclosing canvas/page name; `nodeId` lets you deep-link back (`figma.com/file/:key?node-id=<id>`).

```js
var t = figma.getFileText("abc123", {depth: 6});
t.textNodes.map(function (n) { return n.page + ": " + n.text; });
```

### listProjectFiles(projectId) [getter]

Files in a Figma project. **Needs `projects:read` (private-OAuth-app-only)** — a plain PAT usually 403s; included for completeness.
- params: `projectId` (string, required).
- returns: `{ok, projectId, name, count, files}` — or `{ok:false, error, status}`.

```js
figma.listProjectFiles("55391681");
```

### listTeamProjects(teamId) [getter]

Projects in a team. `teamId` comes from a team URL (`figma.com/team/:team_id/...`). **Needs `projects:read` (private-OAuth-app-only)** — a plain PAT usually 403s; included for completeness.
- params: `teamId` (string, required).
- returns: `{ok, teamId, name, count, projects}` — or `{ok:false, error, status}`.

```js
figma.listTeamProjects("1101853299");
```
