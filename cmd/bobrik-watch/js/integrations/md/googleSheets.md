# googleSheets

## Tool Description

Read the user's **Google Sheets** as a second-brain data source. Spreadsheets are where structured, frequently-updated truth lives — pipelines, OKR trackers, budgets, hiring funnels, inventory, experiment logs, contact lists — so this is high-signal tabular data the agent can reason over directly (sum a column, find a row, cross-reference a name). Read-only: it discovers the user's spreadsheets, lists their tabs, and pulls cell ranges as values (raw 2-D arrays or header-mapped records). Tagged `integration`.

**Auth — shares one Google connection** with the `gmail` / `googleCalendar` / `googleDrive` connectors via `googleAuth@v1`. There is **no per-connector token**: every read goes through `googleAuth.getJSON(url)`, which injects a Bearer access token and transparently refreshes on a 401. If a call returns an auth error (HTTP 401/403), Google is **not connected** — tell the user to run **`googleAuth.connect()`** once and save the returned refresh token to `config@v1` as `GOOGLE_OAUTH_REFRESH_TOKEN`. The scopes this connector uses (`spreadsheets.readonly` + `drive.metadata.readonly`) are already in `googleAuth`'s default scope union; both are **sensitive** scopes, so until the OAuth app is submitted for Google verification it runs in "Testing" mode (≤100 test users, refresh tokens expire after 7 days) — fine for a local single-user prototype, real work to ship publicly.

**Discovery is via Drive, not Sheets.** `listSpreadsheets` runs Google Drive's `files.list` (filtered to the spreadsheet mime type) to enumerate the user's sheets and get their ids — the actual cell data then comes from the Sheets API using that same id (a Drive file id and a spreadsheet id are the same string). Use `getSpreadsheet` first to learn the tab names and grid sizes, then read with A1 notation.

**Ranges use A1 notation** (`"Sheet1!A1:F100"`, `"A:A"` for a whole column, `"Sheet1"` for a whole tab); tab names with spaces must be quoted (`"'My Tab'!A1:C9"`). Trailing empty cells and rows are dropped by the API, so returned rows are **ragged** — `readSheetAsObjects` pads them against the header length. Default values are **formatted** (locale display strings); pass `unformatted:true` when the agent needs to compute on numbers/dates. Mind the per-user read quota (~60 read req/min on Sheets) — prefer `batchGetValues` over many `getValues` calls, and read narrow ranges.

## Tool Schema

### listSpreadsheets(opts) [getter]

Discover the user's spreadsheets via Google Drive (`files.list`), newest-modified first.

**Input:**
- `opts.pageSize` (number, optional) — 1–1000, default 100.
- `opts.pageToken` (string, optional) — next-page cursor from a prior call.
- `opts.query` (string, optional) — name substring; ANDed into the Drive filter.

**Output:** `{ ok, files: [{ id, name, modifiedTime }], nextPageToken }` — or `{ ok:false, error }`. `nextPageToken` is `null` when there are no more pages.

```js
var r = googleSheets.listSpreadsheets({ query: "budget", pageSize: 20 });
// → { ok:true, files:[{id:"1AbC...", name:"2026 Budget", modifiedTime:"..."}], nextPageToken:null }
```

### getSpreadsheet(spreadsheetId) [getter]

Get a spreadsheet's title and the list of its tabs (no cell data — fields-masked, cheap).

**Input:**
- `spreadsheetId` (string, required) — the id from `listSpreadsheets`.

**Output:** `{ ok, spreadsheetId, title, sheets: [{ title, sheetId, rows, cols }] }` — or `{ ok:false, error }`.

```js
var r = googleSheets.getSpreadsheet("1AbC...");
// → { ok:true, title:"2026 Budget", sheets:[{title:"Q1", sheetId:0, rows:200, cols:12}] }
```

### getValues(spreadsheetId, range, opts) [getter]

Read one A1 range as a 2-D array of values. Rows are ragged (trailing empties dropped).

**Input:**
- `spreadsheetId` (string, required).
- `range` (string, required) — A1 notation, e.g. `"Sheet1!A1:D50"` or `"Sheet1"`.
- `opts.unformatted` (boolean, optional) — `true` ⇒ `UNFORMATTED_VALUE` (raw numbers/dates); default formatted display strings.

**Output:** `{ ok, range, values: any[][] }` — or `{ ok:false, error }`.

```js
var r = googleSheets.getValues("1AbC...", "Q1!A1:D50", { unformatted: true });
// → { ok:true, range:"Q1!A1:D50", values:[["Item","Cost"],["Servers",1200]] }
```

### batchGetValues(spreadsheetId, ranges, opts) [getter]

Read several A1 ranges in one call (one request against the per-user read quota — prefer over many `getValues`). Capped at 25 ranges.

**Input:**
- `spreadsheetId` (string, required).
- `ranges` (string[], required) — non-empty array of A1 ranges.
- `opts.unformatted` (boolean, optional) — as in `getValues`.

**Output:** `{ ok, valueRanges: [{ range, values }] }` — or `{ ok:false, error }`.

```js
var r = googleSheets.batchGetValues("1AbC...", ["Q1!A1:D50", "Q2!A1:D50"]);
// → { ok:true, valueRanges:[{range:"Q1!A1:D50", values:[...]}, {range:"Q2!A1:D50", values:[...]}] }
```

### readSheetAsObjects(spreadsheetId, range, opts) [getter]

Convenience: read a range and map the header row to keys, yielding records (table → records). Short rows are padded against the header length so keys never misalign.

**Input:**
- `spreadsheetId` (string, required).
- `range` (string, required) — A1 notation.
- `opts.headerRow` (number, optional) — 1-based row within the range to use as headers (default 1).
- `opts.unformatted` (boolean, optional) — as in `getValues`.

**Output:** `{ ok, range, headers: string[], records: object[] }` — or `{ ok:false, error }`. Empty/missing header cells become `col1`, `col2`, ….

```js
var r = googleSheets.readSheetAsObjects("1AbC...", "Contacts!A1:C100");
// → { ok:true, headers:["Name","Email","Team"], records:[{Name:"Ada", Email:"ada@x.io", Team:"Eng"}] }
```
