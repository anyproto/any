# googleAuth

## Tool Description

Shared Google OAuth for all the Google connectors (`gmail`, `googleSheets`, `googleCalendar`, `googleDrive`). One OAuth client, one consent, one refresh token covers all four via incremental authorization over the union of read-only scopes. Tagged `integration`.

**One-time setup.** First put the OAuth app credentials in `config@v1`: `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET`, created in Google Cloud Console → APIs & Services → Credentials → **OAuth client ID**, application type **Desktop app** (enable the Gmail, Sheets, Calendar, Drive, and Meet APIs in the same project). Then call `connect()` — it opens the system browser to Google's consent screen and blocks until you approve, using the `oauthFlow` host function (auth-code + PKCE over a `http://127.0.0.1:<port>/` loopback redirect). On success it returns a **refresh token**; save it to `config@v1` as `GOOGLE_OAUTH_REFRESH_TOKEN` (ask the `anyPrograms` tool to edit `config@v1`) so it survives restarts.

**After setup**, the connectors call `googleAuth.authedFetch(url, opts)` / `googleAuth.token()` internally — access tokens are refreshed automatically (cached for the session; a 401 triggers one transparent refresh + retry). You normally only call `connect()` (once) and `status()` (to check) directly.

Default scopes are all **sensitive** (not restricted): `gmail.readonly`, `spreadsheets.readonly`, `drive.metadata.readonly`, `calendar.readonly`, `meetings.space.readonly`, plus `openid email`. The Drive Doc-export fallback needs the **restricted** `drive.readonly` scope (Google CASA verification) — pass it explicitly via `connect({scopes:[...]})` only if you accept that. While the OAuth consent screen is in "Testing" mode, refresh tokens expire after 7 days and the app is capped at ≤100 test users; submit the app for Google verification to remove both limits.

## Tool Schema

### connect(opts?) [setup]

Run the one-time Google consent. Opens the browser, blocks until you approve, exchanges the code for tokens. Requires `GOOGLE_OAUTH_CLIENT_ID`/`GOOGLE_OAUTH_CLIENT_SECRET` in `config@v1` first.

**Input:**
- `opts.scopes` (string[], optional) — override the default scope union (e.g. to add the restricted `drive.readonly`).

**Output:** `{ok, connected, refreshToken, scope, note}` — or `{ok:false, error}`. SAVE `refreshToken` to `config@v1` as `GOOGLE_OAUTH_REFRESH_TOKEN`.

```js
var r = googleAuth.connect();
// → {ok:true, refreshToken:"1//0g...", note:"SAVE this refreshToken to config@v1 ..."}
```

### status() [getter]

Report whether Google is configured (client id/secret present) and connected (a refresh token is available).

**Output:** `{ok, configured, connected, hasSessionToken, scopes}`.

```js
googleAuth.status(); // → {ok:true, configured:true, connected:false, ...}
```
