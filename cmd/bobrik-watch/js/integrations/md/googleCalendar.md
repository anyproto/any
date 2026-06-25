# googleCalendar

## Tool Description

Read-only Google Calendar connector (Calendar REST v3). It surfaces the user's calendars and events — meeting titles, times, locations, conferencing links, descriptions/agendas, and attendees (email + responseStatus) — so the agent can answer scheduling and relationship questions ("what's on tomorrow", "who am I meeting with Acme", "what was the last sync about"), build a meeting timeline, and correlate events with other connectors. It never creates or edits events. Tagged `integration`.

**Depends on `googleAuth` — connect first.** This tool has no token of its own: it imports `getJSON` from the shared `googleAuth@v1` layer (one Google OAuth client / one consent / one refresh token across gmail / sheets / calendar / drive). If Google isn't connected, every method returns an actionable error telling the user to run `googleAuth.connect()` once (after setting `GOOGLE_OAUTH_CLIENT_ID` / `GOOGLE_OAUTH_CLIENT_SECRET` in `config@v1`). Access tokens refresh automatically inside `googleAuth`.

**Usage guidance.** Start with `upcoming()` for "what's next", or `listCalendars()` to discover which calendars exist, then `listEvents(calendarId, …)` to pull a time window (RFC3339 `timeMin`/`timeMax`; recurrences are expanded and ordered by start time). Use `getEvent` for full detail on one event. Results are TRIMMED to the useful fields, not the raw Google object, and capped (`maxResults` default 50, max 250). Paginate by passing the returned `nextPageToken` back as `pageToken`.

**Incremental sync.** `listEvents` accepts `opts.syncToken` and returns `nextSyncToken` (present only on the final page of a run). Do a full windowed pull first, store the final `nextSyncToken`, then on later calls pass it back to get only changed events (incl. `status:"cancelled"` tombstones) — note an incremental call must NOT also pass `timeMin`/`timeMax`/`q`, so omit them when using a syncToken; if Google rejects an expired token (HTTP 410), drop it and redo a full pull.

**Scope note.** Uses the SENSITIVE `https://www.googleapis.com/auth/calendar.readonly` scope (part of `googleAuth`'s default union). It reads calendar metadata and event contents — including attendee emails and meeting descriptions — for every calendar the user can access. While the shared Google consent screen is in "Testing" mode the refresh token expires after 7 days.

## Tool Schema

### listCalendars(opts?) [getter]

List the user's calendars (`calendarList.list`). `opts`: `maxResults?` (default 50, max 250), `minAccessRole?` (`owner`/`writer`/`reader`/`freeBusyReader`), `pageToken?`. Returns `{ok, calendars:[{id, summary, primary, accessRole, timeZone}], nextPageToken}`.

```js
var r = googleCalendar.listCalendars();
// → {ok:true, calendars:[{id:"primary", summary:"me@x.com", primary:true, accessRole:"owner"}], ...}
```

### listEvents(calendarId, opts?) [getter]

List events in a time window (`events.list`). `calendarId` defaults to `"primary"`. `opts`: `timeMin?`/`timeMax?` (RFC3339), `q?` (free-text), `maxResults?`, `pageToken?`, `syncToken?`. Without a syncToken it expands recurring events (`singleEvents=true`, `orderBy=startTime`); with a syncToken it does an incremental pull (timeMin/timeMax/q are dropped). Returns `{ok, calendarId, events:[…trimmed], nextPageToken, nextSyncToken, timeZone}`.

```js
var r = googleCalendar.listEvents("primary", { timeMin: "2026-06-24T00:00:00Z", maxResults: 20 });
// incremental: googleCalendar.listEvents("primary", { syncToken: stored })
```

### getEvent(calendarId, eventId) [getter]

Fetch one event in full (`events.get`). `calendarId` defaults to `"primary"`; `eventId` required. Returns `{ok, event}` — the trimmed event (summary, start/end, attendees, location, description, conferenceLink, organizer).

```js
var r = googleCalendar.getEvent("primary", "abc123eventid");
```

### upcoming(opts?) [getter]

Convenience view of upcoming events on the primary calendar — `listEvents("primary", {timeMin: now})`. `opts`: `maxResults?`, `calendarId?` (override primary), `pageToken?`. Returns the same shape as `listEvents`.

```js
var r = googleCalendar.upcoming({ maxResults: 10 });
```
