// __main_source
// __tags: integration
// googleDrive@v1 — Google Meet transcripts (primary) + Google Drive Doc-export
// (fallback) connector. Read-only. Builds on the shared googleAuth@v1 OAuth
// layer — ONE Google client/consent/refresh token covers all the Google
// connectors. Imports getJSON(url) for authed GET reads; never touches OAuth
// directly. If Google isn't connected the auth layer surfaces a clear error
// (run googleAuth.connect() once and save the refresh token).
//
// PRIMARY path — Google Meet REST API v2 (meetings.space.readonly, in
// googleAuth's default scope union): list conference records → list their
// transcripts → page transcript ENTRIES into readable "Speaker: text" lines.
// WORKSPACE-ONLY: transcripts need a paid Workspace plan, admin-enabled
// transcription, and the caller to have been the meeting ORGANIZER; on personal
// Gmail the scope authorizes but returns nothing. API artifacts are deleted
// ~30 days after the meeting.
//
// FALLBACK path — Drive API v3 export (drive.readonly, RESTRICTED, NOT in the
// default googleAuth scope union): the transcript Google Doc persists in Drive
// indefinitely, so older-than-30-day meetings (and generic Docs) go through
// findTranscriptDocs() + exportDocText(). The user must re-consent with the
// restricted scope: googleAuth.connect({scopes:[...,"https://www.googleapis.com/auth/drive.readonly"]}).
// Contract: dev space → Integration Docs → 05 (google-drive).
import { getJSON } from "googleAuth@v1";

var MEET_BASE = "https://meet.googleapis.com/v2";
var DRIVE_BASE = "https://www.googleapis.com/drive/v3";

var DEFAULT_PAGE_SIZE = 25;
var MAX_PAGE_SIZE = 100;          // Meet conferenceRecords/transcripts ceiling
var ENTRIES_PAGE_SIZE = 100;      // Meet entries: default 10, max 100
var MAX_ENTRIES = 2000;           // hard cap on entries assembled per transcript
var MAX_ENTRY_PAGES = 50;         // page-loop safety valve
var DRIVE_PAGE_SIZE = 100;        // Drive files.list (max 1000)

// --- helpers ------------------------------------------------------------

// _clamp(n, def, max) — keep page sizes sane (default small, hard-capped).
function _clamp(n, def, max) {
  if (typeof n !== "number" || !(n > 0)) return def;
  if (n > max) return max;
  return Math.floor(n);
}

// _qs(params) — build a query string from a flat object, skipping undefined /
// null / empty values. Encodes keys + values.
function _qs(params) {
  var parts = [];
  for (var k in params) {
    if (!Object.prototype.hasOwnProperty.call(params, k)) continue;
    var v = params[k];
    if (v === undefined || v === null || v === "") continue;
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v));
  }
  return parts.length ? "?" + parts.join("&") : "";
}

// _get(url) — authed GET via googleAuth.getJSON, normalizing the Meet/Drive
// error envelope and flagging the workspace-only / not-connected cases. Returns
// { ok:true, body } or { ok:false, error, status }.
function _get(url) {
  var r = getJSON(url);
  if (r && r.ok) return { ok: true, body: r.body, headers: r.headers };
  var status = r && r.status;
  var detail = (r && r.error) || ("HTTP " + (status || "?"));
  if (status === 401) {
    return { ok: false, status: status,
      error: "Google not connected / token rejected (HTTP 401). Run googleAuth.connect() once and save the refresh token to config@v1." };
  }
  if (status === 403) {
    return { ok: false, status: status,
      error: "Google denied access (HTTP 403): " + detail + ". The Meet API is WORKSPACE-ONLY — it needs a paid Google Workspace plan with admin-enabled transcription, and you must have been the meeting organizer; it returns nothing on a personal Gmail account. The Drive fallback also needs the restricted drive.readonly scope (re-consent via googleAuth.connect({scopes:[...]}))." };
  }
  if (status === 429) {
    return { ok: false, status: status,
      error: "Google rate limit (HTTP 429): " + detail + ". The Meet API allows ~60 requests/min per project — retry after a short backoff." };
  }
  return { ok: false, status: status, error: detail };
}

// _entrySpeaker(e) — best-effort display name for a TranscriptEntry. Meet keys
// the participant under signedinUser.user (display name) or
// anonymousUser.displayName for guests; field drift across docs is handled by
// probing the common shapes.
function _entrySpeaker(e) {
  if (!e) return "Unknown";
  if (typeof e.speaker === "string" && e.speaker) return e.speaker;
  var p = e.participant;
  if (p) {
    if (p.signedinUser && p.signedinUser.user) return p.signedinUser.user;
    if (p.anonymousUser && p.anonymousUser.displayName) return p.anonymousUser.displayName;
    if (p.phoneUser && p.phoneUser.displayName) return p.phoneUser.displayName;
    if (typeof p === "string") return p;
  }
  return "Unknown";
}

// _entryText(e) — the spoken text. Docs show both `text` and `content`.
function _entryText(e) {
  if (!e) return "";
  if (typeof e.text === "string") return e.text;
  if (typeof e.content === "string") return e.content;
  return "";
}

// --- Meet REST v2 (PRIMARY) ---------------------------------------------

// listConferences(opts) — GET /v2/conferenceRecords. Enumerate recent
// conference records. opts: { pageSize?, pageToken?, filter? } — `filter` is the
// Meet list filter (e.g. by space.name or time, see the Meet API docs). Returns
// { ok, conferences, nextPageToken }.
export function listConferences(opts) {
  opts = opts || {};
  var pageSize = _clamp(opts.pageSize, DEFAULT_PAGE_SIZE, MAX_PAGE_SIZE);
  var url = MEET_BASE + "/conferenceRecords"
    + _qs({ pageSize: pageSize, pageToken: opts.pageToken, filter: opts.filter });
  var r = _get(url);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var body = r.body || {};
  return {
    ok: true,
    conferences: body.conferenceRecords || [],
    nextPageToken: body.nextPageToken || null
  };
}

// listTranscripts(conferenceRecordName) — GET
// /v2/conferenceRecords/{record}/transcripts. List the transcript(s) for one
// conference record. Accepts the full "conferenceRecords/{id}" resource name or
// a bare id. Returns { ok, transcripts, nextPageToken } — each transcript has
// `name`, `state`, `startTime`/`endTime`, and `docsDestination`
// { document, exportUri } (the underlying Google Doc id + browser link).
export function listTranscripts(conferenceRecordName) {
  if (!conferenceRecordName || typeof conferenceRecordName !== "string") {
    return { ok: false, error: "conferenceRecordName is required (e.g. \"conferenceRecords/abc123\")" };
  }
  var rec = conferenceRecordName.indexOf("conferenceRecords/") === 0
    ? conferenceRecordName
    : "conferenceRecords/" + conferenceRecordName;
  var url = MEET_BASE + "/" + rec + "/transcripts" + _qs({ pageSize: MAX_PAGE_SIZE });
  var r = _get(url);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var body = r.body || {};
  return {
    ok: true,
    transcripts: body.transcripts || [],
    nextPageToken: body.nextPageToken || null
  };
}

// getTranscriptText(transcriptName) — THE HEADLINE METHOD. Page through
// .../transcripts/{transcript}/entries and concatenate the speaker/text lines
// into one readable "Speaker: text" transcript. `transcriptName` is the full
// resource name "conferenceRecords/{r}/transcripts/{t}". Follows nextPageToken
// (entries page at ≤100), caps at MAX_ENTRIES, and orders by page order
// (timing fields are best-effort). Returns
// { ok, transcriptName, text, lines, entryCount, truncated }.
export function getTranscriptText(transcriptName) {
  if (!transcriptName || typeof transcriptName !== "string") {
    return { ok: false, error: "transcriptName is required (e.g. \"conferenceRecords/abc/transcripts/xyz\")" };
  }
  var lines = [];
  var entryCount = 0;
  var truncated = false;
  var pageToken = null;
  var pages = 0;

  while (true) {
    var url = MEET_BASE + "/" + transcriptName + "/entries"
      + _qs({ pageSize: ENTRIES_PAGE_SIZE, pageToken: pageToken });
    var r = _get(url);
    if (!r.ok) return { ok: false, error: r.error, status: r.status };
    var body = r.body || {};
    var entries = body.transcriptEntries || body.entries || [];
    for (var i = 0; i < entries.length; i++) {
      if (entryCount >= MAX_ENTRIES) { truncated = true; break; }
      var e = entries[i];
      lines.push(_entrySpeaker(e) + ": " + _entryText(e));
      entryCount++;
    }
    pages++;
    pageToken = body.nextPageToken || null;
    if (truncated || !pageToken || pages >= MAX_ENTRY_PAGES) {
      if (pageToken && !truncated) truncated = true; // hit the page-loop cap
      break;
    }
  }

  return {
    ok: true,
    transcriptName: transcriptName,
    text: lines.join("\n"),
    lines: lines,
    entryCount: entryCount,
    truncated: truncated
  };
}

// recentMeetingTranscripts(opts) — convenience: list recent conferences →
// for each, list its transcripts → return lightweight SUMMARIES. Does NOT
// auto-fetch every full transcript (that's getTranscriptText's job — let the
// agent pick which to pull). opts: { pageSize?, pageToken?, filter? }. Returns
// { ok, meetings:[{ conferenceRecord, startTime, endTime, space, transcripts:
// [{ name, state, startTime, endTime, docId, exportUri }] }], nextPageToken }.
export function recentMeetingTranscripts(opts) {
  opts = opts || {};
  var listed = listConferences(opts);
  if (!listed.ok) return listed;
  var meetings = [];
  for (var i = 0; i < listed.conferences.length; i++) {
    var c = listed.conferences[i] || {};
    var tr = listTranscripts(c.name);
    var transcripts = [];
    if (tr.ok) {
      for (var j = 0; j < tr.transcripts.length; j++) {
        var t = tr.transcripts[j] || {};
        var dd = t.docsDestination || {};
        transcripts.push({
          name: t.name || null,
          state: t.state || null,
          startTime: t.startTime || null,
          endTime: t.endTime || null,
          docId: dd.document || null,
          exportUri: dd.exportUri || null
        });
      }
    }
    meetings.push({
      conferenceRecord: c.name || null,
      startTime: c.startTime || null,
      endTime: c.endTime || null,
      space: c.space || null,
      transcripts: transcripts,
      transcriptsError: tr.ok ? null : tr.error
    });
  }
  return { ok: true, meetings: meetings, nextPageToken: listed.nextPageToken };
}

// --- Drive v3 export (FALLBACK — needs restricted drive.readonly) -------

// findTranscriptDocs(opts) — FALLBACK for >30-day-old meetings / generic Docs.
// Drive v3 files.list to find Meet transcript Google Docs. By default matches
// Docs whose name contains "Transcript"; pass opts.folderId to scope to the
// "Meet Recordings" folder, or opts.q to supply a raw Drive query. Requires the
// RESTRICTED drive.readonly scope (re-consent via googleAuth.connect({scopes:
// [...,"https://www.googleapis.com/auth/drive.readonly"]})). opts:
// { q?, folderId?, nameContains?, pageSize?, pageToken? }. Returns
// { ok, files:[{ id, name, createdTime, webViewLink }], nextPageToken }.
export function findTranscriptDocs(opts) {
  opts = opts || {};
  var q = opts.q;
  if (!q) {
    var clauses = ["mimeType='application/vnd.google-apps.document'"];
    var nameContains = opts.nameContains || "Transcript";
    clauses.push("name contains '" + String(nameContains).replace(/'/g, "\\'") + "'");
    if (opts.folderId) clauses.push("'" + opts.folderId + "' in parents");
    q = clauses.join(" and ");
  }
  var pageSize = _clamp(opts.pageSize, DRIVE_PAGE_SIZE, 1000);
  var url = DRIVE_BASE + "/files" + _qs({
    q: q,
    pageSize: pageSize,
    pageToken: opts.pageToken,
    fields: "nextPageToken,files(id,name,createdTime,webViewLink)",
    orderBy: "createdTime desc"
  });
  var r = _get(url);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var body = r.body || {};
  return {
    ok: true,
    files: body.files || [],
    nextPageToken: body.nextPageToken || null
  };
}

// exportDocText(fileId) — FALLBACK. Export a Google Doc to plain text via Drive
// v3 files.export?mimeType=text/plain. Used for transcript Docs of meetings too
// old for the Meet API (~30 days) and generic Docs. Requires the RESTRICTED
// drive.readonly scope. NOTE: Drive's export cap is 10 MB — larger Docs fail
// export (Google returns an error; this surfaces it). Returns
// { ok, fileId, text }. The exported body is plain text, not JSON, so it comes
// back as a string in resp.body.
export function exportDocText(fileId) {
  if (!fileId || typeof fileId !== "string") {
    return { ok: false, error: "fileId is required" };
  }
  var url = DRIVE_BASE + "/files/" + encodeURIComponent(fileId)
    + "/export" + _qs({ mimeType: "text/plain" });
  var r = _get(url);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var text = r.body;
  if (text && typeof text === "object") text = JSON.stringify(text);
  return { ok: true, fileId: fileId, text: typeof text === "string" ? text : String(text || "") };
}

// main(args) — default entry point: list recent conferences (a quick
// connectivity + workspace check).
export function main(args) {
  return listConferences(args || {});
}
