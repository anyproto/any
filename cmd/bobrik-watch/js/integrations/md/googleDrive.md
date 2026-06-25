# googleDrive

## Tool Description

Pulls **Google Meet transcripts** (primary) and Google Drive document text (fallback) into your second brain. Tagged `integration`. A Meet transcript is a dense, timestamped, speaker-attributed record of a meeting — the highest-value feed for recall ("what did we decide about pricing last Tuesday?"). This connector lists your recent meetings, finds their transcripts, and assembles the speaker/text lines into readable "Speaker: text" transcripts you can save as notes.

**Depends on `googleAuth` — connect first.** This tool does no OAuth of its own; it reads through the shared `googleAuth@v1` layer (`getJSON`). One Google client / one consent / one refresh token covers all the Google connectors. If you aren't connected, calls return an actionable error: run `googleAuth.connect()` once, then save the returned refresh token to `config@v1` as `GOOGLE_OAUTH_REFRESH_TOKEN`. Access tokens refresh automatically inside `googleAuth`.

**Two paths — Meet API primary, Drive export fallback.**

- **PRIMARY — Google Meet REST API v2** (`https://meet.googleapis.com/v2`, scope `meetings.space.readonly`, already in `googleAuth`'s default scope union). `listConferences` → `listTranscripts` → `getTranscriptText` is a clean "list my meetings, list their transcripts, read one" tree, returning already-speaker-attributed entries — no Doc parsing. Prefer this path.
- **FALLBACK — Drive API v3 export** (`https://www.googleapis.com/drive/v3`, scope `drive.readonly`). The transcript Google **Doc persists in Drive indefinitely**, so this covers meetings older than the Meet API's ~30-day retention (and generic Docs). `drive.readonly` is a **RESTRICTED** scope and is **NOT** in `googleAuth`'s default union — the user must explicitly re-consent: `googleAuth.connect({scopes:[...,"https://www.googleapis.com/auth/drive.readonly"]})` (Google CASA verification applies beyond test users).

**Hard caveats to relay to the user when results are empty or denied:**

- **Workspace-only.** The Meet API needs a paid Google **Workspace** plan (Business/Enterprise Standard or Plus), the admin must have **transcription enabled**, and **you must have been the meeting organizer**. On personal Gmail the scope authorizes but returns nothing useful (an HTTP 403 / empty list).
- **~30-day retention.** Meet API transcript artifacts are **deleted ~30 days after the meeting**. Older meetings only reachable via the Drive fallback (the saved Doc).
- **Post-meeting only.** Transcripts appear after Google finishes processing the recording; there is no live feed.
- **Rate limit ~60 req/min/project** — `getTranscriptText` pages entries (≤100/page) and honors `pageToken`; on HTTP 429 back off and retry. The Drive export cap is **10 MB** (larger Docs fail export).

Methods are read-only and return a consistent `{ok, ...}` / `{ok:false, error, status}` shape. Result sizes are capped (page sizes default small; `getTranscriptText` caps at 2000 entries). Prefer **returning** transcript text to the agent over auto-persisting — let the user/agent decide what to save as a note.

## Tool Schema

### listConferences(opts) [getter]

List recent Meet conference records (`GET /v2/conferenceRecords`). Each record has a `name` like `conferenceRecords/{id}`.

**Input:** `opts.pageSize` (number, default 25, max 100), `opts.pageToken` (string), `opts.filter` (string — Meet list filter, e.g. by `space.name` or time).

**Output:** `{ok, conferences:[{name, startTime, endTime, space, ...}], nextPageToken}`.

```js
var r = googleDrive.listConferences({ pageSize: 10 });
// → {ok:true, conferences:[{name:"conferenceRecords/abc", ...}], nextPageToken:"..."}
```

### listTranscripts(conferenceRecordName) [getter]

List the transcript(s) for one conference record (`GET /v2/conferenceRecords/{record}/transcripts`). Accepts the full `conferenceRecords/{id}` resource name or a bare id.

**Input:** `conferenceRecordName` (string, required).

**Output:** `{ok, transcripts:[{name, state, startTime, endTime, docsDestination:{document, exportUri}}], nextPageToken}` — `docsDestination.exportUri` is the browser link, usable as `externalUrl`.

```js
var r = googleDrive.listTranscripts("conferenceRecords/abc123");
```

### getTranscriptText(transcriptName) [getter]

**Headline method.** Page through a transcript's entries (`GET /v2/conferenceRecords/{r}/transcripts/{t}/entries`) and concatenate them into one readable "Speaker: text" transcript. Follows `pageToken` (entries page at ≤100), caps at 2000 entries, orders by page order (timing fields are best-effort).

**Input:** `transcriptName` (string, required — full resource name `conferenceRecords/{r}/transcripts/{t}`).

**Output:** `{ok, transcriptName, text, lines:[string], entryCount, truncated}`.

```js
var r = googleDrive.getTranscriptText("conferenceRecords/abc/transcripts/xyz");
// → {ok:true, text:"Alice: let's ship Friday\nBob: agreed", entryCount:2, truncated:false}
```

### recentMeetingTranscripts(opts) [getter]

Convenience: list recent conferences → for each, list its transcripts → return lightweight **summaries**. Does NOT auto-fetch the full text of every transcript — call `getTranscriptText` on the ones you want.

**Input:** `opts.pageSize` / `opts.pageToken` / `opts.filter` (same as `listConferences`).

**Output:** `{ok, meetings:[{conferenceRecord, startTime, endTime, space, transcripts:[{name, state, startTime, endTime, docId, exportUri}], transcriptsError}], nextPageToken}`.

```js
var r = googleDrive.recentMeetingTranscripts({ pageSize: 5 });
// pick a meeting, then: googleDrive.getTranscriptText(r.meetings[0].transcripts[0].name)
```

### findTranscriptDocs(opts) [getter]

**FALLBACK** (needs the restricted `drive.readonly` scope — re-consent via `googleAuth.connect({scopes:[...]})`). Find Meet transcript Google Docs in Drive (`GET /drive/v3/files?q=...`) for meetings older than the Meet API's ~30-day window, or generic Docs. By default matches Docs whose name contains "Transcript"; scope to the "Meet Recordings" folder with `folderId`, or pass a raw `q`.

**Input:** `opts.q` (string — raw Drive query, overrides the rest), `opts.folderId` (string — e.g. the "Meet Recordings" folder id), `opts.nameContains` (string, default "Transcript"), `opts.pageSize` (number, default 100, max 1000), `opts.pageToken` (string).

**Output:** `{ok, files:[{id, name, createdTime, webViewLink}], nextPageToken}`.

```js
var r = googleDrive.findTranscriptDocs({ nameContains: "Transcript" });
```

### exportDocText(fileId) [getter]

**FALLBACK** (needs the restricted `drive.readonly` scope). Export a Google Doc to plain text (`GET /drive/v3/files/{fileId}/export?mimeType=text/plain`). Used for transcript Docs of meetings too old for the Meet API and for generic Docs. **Export cap is 10 MB** — larger Docs fail (the error is surfaced).

**Input:** `fileId` (string, required — e.g. from `findTranscriptDocs` or a transcript's `docsDestination.document`).

**Output:** `{ok, fileId, text}`.

```js
var r = googleDrive.exportDocText("1AbC...docId");
// → {ok:true, fileId:"1AbC...", text:"..."}
```
