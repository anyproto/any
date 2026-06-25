# github

## Tool Description

`github@v1` (tagged `integration`) reads the user's GitHub work context into
bobrik — issues, pull requests, notifications, and repository README/file
contents. It is **read-only**: writes (open issue, comment, merge) are
deliberately out of scope for this connector. The MVP is ingestion and recall,
not driving GitHub.

**Credential setup (Pattern 1 — token).** The connector authenticates with a
**fine-grained Personal Access Token** read from `config@v1` under the key
`GITHUB_TOKEN`. If no token is stored, every method returns a clear "not
connected" error. Ask the user to create one at
<https://github.com/settings/tokens?type=beta> with **Read-only** permissions,
then save it to `config@v1` as `GITHUB_TOKEN` (the agent can save a pasted key
via the `anyPrograms` tool). Permissions to enable, scoped to the repos they
want ingested (or "All repositories"):

- **Repository → Issues**: Read-only (list/read issues and comments).
- **Repository → Pull requests**: Read-only (PR detail; PRs also surface
  through the issues endpoints).
- **Repository → Contents**: Read-only (README + file contents).
- **Repository → Metadata**: Read-only (mandatory; auto-selected).
- **Account → Notifications**: Read-only — only needed for `listNotifications`.

A `401`-derived error means the token is bad or expired — prompt the user for a
fresh one. A `403` without a rate-limit signal usually means a missing PAT
permission (or org SSO approval) — surface GitHub's message so the user knows
which permission to grant.

**What it surfaces.** `whoami` verifies the token and returns the login.
`listMyIssues` / `listRepoIssues` pull issues + PRs (flagged by `isPR`).
`searchIssues` runs a targeted GitHub-search-syntax query. `getIssue` returns
one issue/PR with its comment thread. `listNotifications` returns the unread
inbox. `getReadme` / `getContents` decode repository files to text. Payloads are
trimmed aggressively (small `Issue` shape, body/text truncated) so the agent's
context isn't flooded; methods page via the `Link` header and accept a `perPage`
cap (default 30, max 100).

**Rate-limit caveat.** Authenticated reads share a **5000 requests/hour** bucket.
The **search API is a separate, much tighter 30 requests/minute** bucket — treat
`searchIssues` as expensive and prefer `listMyIssues` / `listRepoIssues` for
bulk pulls. The connector honors `Retry-After` and backs off on `403`/`429`, but
a tight agent loop can still trip the search limit.

## Tool Schema

### whoami() [getter]
Verify the stored token and return the authenticated user. No arguments.
returns: `{ ok, login, id, name, url }` (or `{ ok:false, error }` if not
connected / token invalid).
Example: `github.whoami()`

### listMyIssues(opts) [getter]
Issues + PRs assigned to / created by / mentioning the user across all visible
repos (`GET /issues`).
- `opts.filter`: "assigned"|"created"|"mentioned"|"subscribed"|"all" (default "assigned")
- `opts.state`: "open"|"closed"|"all" (default "open")
- `opts.since`: ISO-8601 string (optional) — only items updated after this
- `opts.perPage`: number (default 30, max 100)
returns: `{ ok, items: Issue[] }` where `Issue = {number,title,state,url,repo,author,labels,isPR,updatedAt,body}`.
Example: `github.listMyIssues({ filter: "assigned", state: "open", perPage: 20 })`

### listRepoIssues(owner, repo, opts) [getter]
Issues + PRs for one repository (`GET /repos/{owner}/{repo}/issues`). PRs are
included; tell them apart by `isPR`.
- `owner`: string (required)
- `repo`: string (required)
- `opts.state`: "open"|"closed"|"all" (default "open")
- `opts.since`: ISO-8601 string (optional)
- `opts.perPage`: number (default 30, max 100)
returns: `{ ok, items: Issue[] }`
Example: `github.listRepoIssues("anyproto", "any", { state: "open" })`

### searchIssues(q, opts) [getter]
Targeted search over issues/PRs using GitHub search syntax
(`GET /search/issues`). **Rate-limited to 30/min** — use sparingly; prefer the
list methods for bulk pulls.
- `q`: string (required) — e.g. `"repo:anyproto/any is:pr is:open review-requested:@me"`
- `opts.perPage`: number (default 30, max 100)
returns: `{ ok, totalCount, incompleteResults, items: Issue[] }`
Example: `github.searchIssues("repo:anyproto/any is:issue is:open assignee:@me")`

### getIssue(owner, repo, number) [getter]
One issue or PR with its comment thread
(`GET /repos/{owner}/{repo}/issues/{number}` + `…/comments`).
- `owner`: string (required)
- `repo`: string (required)
- `number`: number (required)
returns: `{ ok, issue: Issue, comments: Comment[] }` where
`Comment = {author, createdAt, body}`.
Example: `github.getIssue("anyproto", "any", 42)`

### listNotifications(opts) [getter]
The authenticated user's notification inbox (`GET /notifications`). Requires the
account-level **Notifications: Read** permission on the PAT.
- `opts.all`: boolean (default false) — include already-read notifications
- `opts.since`: ISO-8601 string (optional)
- `opts.perPage`: number (default 30, max 100)
returns: `{ ok, items: Notification[] }` where
`Notification = {id, reason, subjectTitle, type, url, repo, updatedAt, unread}`.
Example: `github.listNotifications({ all: false })`

### getReadme(owner, repo) [getter]
A repository's README, base64-decoded to text (`GET /repos/{owner}/{repo}/readme`).
- `owner`: string (required)
- `repo`: string (required)
returns: `{ ok, path, text }` (text truncated for context safety).
Example: `github.getReadme("anyproto", "any")`

### getContents(owner, repo, path) [getter]
A file (base64-decoded to text) or a directory listing
(`GET /repos/{owner}/{repo}/contents/{path}`).
- `owner`: string (required)
- `repo`: string (required)
- `path`: string (required) — file or directory path within the repo
returns: `{ ok, kind: "file"|"dir", text?, path?, entries?: {name,path,type}[] }`.
Example: `github.getContents("anyproto", "any", "README.md")`
