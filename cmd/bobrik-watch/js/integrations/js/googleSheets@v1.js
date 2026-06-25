// __main_source
// __tags: integration
// googleSheets@v1 — read-only connector for Google Sheets (REST v4). Reuses the
// shared googleAuth@v1 OAuth layer — ONE Google client / consent / refresh token
// covers gmail / googleSheets / googleCalendar / googleDrive, so there is NO
// per-connector token in config@v1. Every authed GET goes through
// googleAuth.getJSON(url), which injects a Bearer access token and transparently
// refreshes on a 401. Surfaces the user's spreadsheets (discovered via Drive
// files.list), each spreadsheet's tabs/grid metadata, and cell ranges as values
// (raw 2-D arrays or header-mapped records). All methods return a consistent
// {ok, ...} / {ok:false, error} shape; not-connected surfaces as an actionable
// "run googleAuth.connect()" error. Scopes used (already in googleAuth's union):
// spreadsheets.readonly + drive.metadata.readonly (both SENSITIVE).
// See dev space → Integration Docs → 03 (google-sheets).
import { getJSON } from "googleAuth@v1";

var SHEETS_BASE = "https://sheets.googleapis.com/v4/spreadsheets";
var DRIVE_FILES = "https://www.googleapis.com/drive/v3/files";

var DEFAULT_PAGE_SIZE = 100;
var MAX_PAGE_SIZE = 1000;
var MAX_RANGES = 25; // batchGetValues range cap — stay well under the read quota

// --- helpers --------------------------------------------------------------

// _clampPageSize(n) — Drive files.list pageSize is 1..1000 (default 100).
function _clampPageSize(n) {
  if (typeof n !== "number" || !(n > 0)) return DEFAULT_PAGE_SIZE;
  if (n > MAX_PAGE_SIZE) return MAX_PAGE_SIZE;
  return Math.floor(n);
}

// _qs(params) — build a query string from a {k:v} map, URL-encoding both sides.
// Skips null/undefined/"" values. Arrays expand to repeated key=val pairs
// (Drive/Sheets use ?ranges=a&ranges=b style).
function _qs(params) {
  var parts = [];
  for (var k in params) {
    if (!Object.prototype.hasOwnProperty.call(params, k)) continue;
    var v = params[k];
    if (v === null || v === undefined || v === "") continue;
    if (Array.isArray(v)) {
      for (var i = 0; i < v.length; i++) {
        parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v[i]));
      }
    } else {
      parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v));
    }
  }
  return parts.length ? "?" + parts.join("&") : "";
}

// _err(label, r) — normalize a failed getJSON() result into {ok:false, error}.
// A 401/403 from Google almost always means not-connected or a missing scope —
// point the agent at googleAuth.connect().
function _err(label, r) {
  var status = r && r.status;
  if (status === 401 || status === 403) {
    return {
      ok: false,
      status: status,
      error: label + " failed (HTTP " + status + ") — Google not connected or "
        + "missing scope. Run googleAuth.connect() once (and save the refresh "
        + "token to config@v1 as GOOGLE_OAUTH_REFRESH_TOKEN), then retry. "
        + "Original: " + ((r && r.error) || "auth error")
    };
  }
  return { ok: false, status: status, error: label + ": " + ((r && r.error) || "request failed") };
}

// --- tool methods ---------------------------------------------------------

// listSpreadsheets(opts) — DISCOVERY via Drive files.list. Lists the user's
// Google Sheets files (id + name + modifiedTime), newest-modified first.
// opts: { pageSize?, pageToken?, query? } — query is an optional name substring
// ANDed into the Drive q filter.
export function listSpreadsheets(opts) {
  opts = opts || {};
  var q = "mimeType='application/vnd.google-apps.spreadsheet' and trashed=false";
  if (opts.query && typeof opts.query === "string") {
    // Escape single quotes in the name fragment per Drive query grammar.
    var name = opts.query.split("'").join("\\'");
    q += " and name contains '" + name + "'";
  }
  var url = DRIVE_FILES + _qs({
    q: q,
    fields: "nextPageToken,files(id,name,modifiedTime)",
    pageSize: _clampPageSize(opts.pageSize),
    pageToken: opts.pageToken || "",
    orderBy: "modifiedTime desc"
  });
  var r = getJSON(url);
  if (!r.ok) return _err("listSpreadsheets", r);
  var body = r.body || {};
  return { ok: true, files: body.files || [], nextPageToken: body.nextPageToken || null };
}

// getSpreadsheet(spreadsheetId) — metadata only (fields-masked): the title and
// the list of tabs with their grid dimensions. No cell values — cheap, safe to
// call before reading ranges.
export function getSpreadsheet(spreadsheetId) {
  if (!spreadsheetId || typeof spreadsheetId !== "string") {
    return { ok: false, error: "spreadsheetId is required" };
  }
  var url = SHEETS_BASE + "/" + encodeURIComponent(spreadsheetId) + _qs({
    fields: "spreadsheetId,properties.title,sheets.properties(title,sheetId,gridProperties)"
  });
  var r = getJSON(url);
  if (!r.ok) return _err("getSpreadsheet", r);
  var body = r.body || {};
  var sheets = [];
  var raw = body.sheets || [];
  for (var i = 0; i < raw.length; i++) {
    var p = (raw[i] && raw[i].properties) || {};
    var g = p.gridProperties || {};
    sheets.push({
      title: p.title || "",
      sheetId: p.sheetId,
      rows: g.rowCount || 0,
      cols: g.columnCount || 0
    });
  }
  return {
    ok: true,
    spreadsheetId: body.spreadsheetId || spreadsheetId,
    title: (body.properties && body.properties.title) || "",
    sheets: sheets
  };
}

// getValues(spreadsheetId, range, opts) — read one A1 range as a 2-D array.
// range is A1 notation, e.g. "Sheet1!A1:D50" or "Sheet1" (whole tab). Trailing
// empty cells/rows are dropped by the API, so rows are ragged. opts.unformatted
// => UNFORMATTED_VALUE (numbers/dates as raw values, for computing); default is
// FORMATTED_VALUE (locale display strings).
export function getValues(spreadsheetId, range, opts) {
  if (!spreadsheetId || typeof spreadsheetId !== "string") {
    return { ok: false, error: "spreadsheetId is required" };
  }
  if (!range || typeof range !== "string") {
    return { ok: false, error: "range is required (A1 notation, e.g. \"Sheet1!A1:D50\")" };
  }
  opts = opts || {};
  var url = SHEETS_BASE + "/" + encodeURIComponent(spreadsheetId)
    + "/values/" + encodeURIComponent(range)
    + _qs({
      valueRenderOption: opts.unformatted ? "UNFORMATTED_VALUE" : "FORMATTED_VALUE",
      dateTimeRenderOption: "FORMATTED_STRING"
    });
  var r = getJSON(url);
  if (!r.ok) return _err("getValues", r);
  var body = r.body || {};
  return { ok: true, range: body.range || range, values: body.values || [] };
}

// batchGetValues(spreadsheetId, ranges, opts) — read several A1 ranges in one
// call (cheaper than N getValues — one request against the per-user read quota).
// ranges is a string[] of A1 ranges. Capped at MAX_RANGES. opts.unformatted as
// in getValues.
export function batchGetValues(spreadsheetId, ranges, opts) {
  if (!spreadsheetId || typeof spreadsheetId !== "string") {
    return { ok: false, error: "spreadsheetId is required" };
  }
  if (!Array.isArray(ranges) || ranges.length === 0) {
    return { ok: false, error: "ranges is required (non-empty array of A1 ranges)" };
  }
  if (ranges.length > MAX_RANGES) {
    return { ok: false, error: "too many ranges (" + ranges.length + ") — cap is " + MAX_RANGES };
  }
  opts = opts || {};
  var url = SHEETS_BASE + "/" + encodeURIComponent(spreadsheetId) + "/values:batchGet"
    + _qs({
      ranges: ranges,
      valueRenderOption: opts.unformatted ? "UNFORMATTED_VALUE" : "FORMATTED_VALUE",
      dateTimeRenderOption: "FORMATTED_STRING"
    });
  var r = getJSON(url);
  if (!r.ok) return _err("batchGetValues", r);
  var body = r.body || {};
  var out = [];
  var vr = body.valueRanges || [];
  for (var i = 0; i < vr.length; i++) {
    out.push({ range: (vr[i] && vr[i].range) || "", values: (vr[i] && vr[i].values) || [] });
  }
  return { ok: true, valueRanges: out };
}

// readSheetAsObjects(spreadsheetId, range, opts) — convenience: read a range and
// map the header row to keys, yielding records. opts.headerRow (1-based, default
// 1) is the row WITHIN the returned range to use as headers; rows after it become
// records. Short rows are padded against the header length (the API drops
// trailing empties) so keys never misalign. opts.unformatted as in getValues.
export function readSheetAsObjects(spreadsheetId, range, opts) {
  opts = opts || {};
  var got = getValues(spreadsheetId, range, opts);
  if (!got.ok) return got;
  var values = got.values || [];
  var headerIdx = (typeof opts.headerRow === "number" && opts.headerRow > 0)
    ? Math.floor(opts.headerRow) - 1 : 0;
  if (values.length <= headerIdx) {
    return { ok: true, range: got.range, headers: [], records: [] };
  }
  var headerRow = values[headerIdx] || [];
  var headers = [];
  for (var h = 0; h < headerRow.length; h++) {
    var key = (headerRow[h] === null || headerRow[h] === undefined) ? "" : String(headerRow[h]);
    headers.push(key || ("col" + (h + 1)));
  }
  var records = [];
  for (var r = headerIdx + 1; r < values.length; r++) {
    var row = values[r] || [];
    var rec = {};
    for (var c = 0; c < headers.length; c++) {
      rec[headers[c]] = (c < row.length) ? row[c] : "";
    }
    records.push(rec);
  }
  return { ok: true, range: got.range, headers: headers, records: records };
}

// main(args) — default entry point: discovery (a quick connectivity check that
// also doubles as "show me my spreadsheets").
export function main(args) {
  args = args || {};
  return listSpreadsheets(args);
}
