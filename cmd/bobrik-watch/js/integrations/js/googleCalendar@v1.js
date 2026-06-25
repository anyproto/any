// __main_source
// __tags: integration
// googleCalendar@v1 — read-only Google Calendar connector (Calendar REST v3).
// OAuth is NOT a config token: it rides the shared googleAuth@v1 layer — one
// Google client / one consent / one refresh token covers gmail / sheets /
// calendar / drive. We import getJSON() for every authed GET; if Google isn't
// connected the imported helper throws a clear "run googleAuth.connect()"
// error, which each method surfaces verbatim.
//
// Surfaces the user's calendars, events in a time window (recurrences expanded),
// a single event in full, and a convenience "upcoming" view. Events are TRIMMED
// to the useful fields (summary, start/end, attendees, location, conferencing,
// description) rather than the raw Google object. Incremental sync is supported
// via syncToken (pass opts.syncToken in, read nextSyncToken back out).
// Scope: calendar.readonly (SENSITIVE — part of googleAuth's union).
// Contract: dev space → Integration Docs → 04 (Google Calendar).
import { getJSON } from "googleAuth@v1";

var BASE = "https://www.googleapis.com/calendar/v3";
var DEFAULT_MAX = 50;
var MAX_RESULTS = 250;

// --- helpers ------------------------------------------------------------

// _clampMax(n) — keep page sizes sane: default small, hard-cap at MAX_RESULTS
// (an agent context is finite; recurrence expansion can fan out fast).
function _clampMax(n) {
  if (typeof n !== "number" || !(n > 0)) return DEFAULT_MAX;
  if (n > MAX_RESULTS) return MAX_RESULTS;
  return Math.floor(n);
}

// _qs(params) — build a URL query string, URL-encoding keys and values and
// skipping null/undefined/"" entries.
function _qs(params) {
  var parts = [];
  for (var k in params) {
    if (!Object.prototype.hasOwnProperty.call(params, k)) continue;
    var v = params[k];
    if (v === null || v === undefined || v === "") continue;
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v));
  }
  return parts.length ? ("?" + parts.join("&")) : "";
}

// _get(path) — authed GET via googleAuth.getJSON. Returns {ok, body} or
// {ok:false, error, status}. getJSON throws when Google isn't connected; we
// translate that into the actionable not-connected error.
function _get(path) {
  var r;
  try {
    r = getJSON(BASE + path);
  } catch (e) {
    return { ok: false, error: (e && e.message ? e.message : String(e))
      + " (run googleAuth.connect() once to authorize Google)." };
  }
  if (!r || !r.ok) {
    return { ok: false, error: (r && r.error) || "request failed", status: r && r.status };
  }
  return { ok: true, body: r.body, headers: r.headers };
}

// _trimAttendee(a) — keep only email + responseStatus (+ organizer/self flags
// when present); drop the rest of Google's attendee object.
function _trimAttendee(a) {
  if (!a) return null;
  var out = { email: a.email || "", responseStatus: a.responseStatus || "" };
  if (a.displayName) out.displayName = a.displayName;
  if (a.organizer) out.organizer = true;
  if (a.self) out.self = true;
  if (a.optional) out.optional = true;
  return out;
}

// _conferenceLink(ev) — best conferencing link: hangoutLink, else the first
// videoUri entry in conferenceData.
function _conferenceLink(ev) {
  if (ev.hangoutLink) return ev.hangoutLink;
  var cd = ev.conferenceData;
  if (cd && cd.entryPoints) {
    for (var i = 0; i < cd.entryPoints.length; i++) {
      var ep = cd.entryPoints[i];
      if (ep && ep.entryPointType === "video" && ep.uri) return ep.uri;
    }
  }
  return "";
}

// _trimEvent(ev) — reduce a raw Google event to the useful fields.
function _trimEvent(ev) {
  if (!ev) return null;
  var attendees = [];
  if (ev.attendees) {
    for (var i = 0; i < ev.attendees.length; i++) {
      var a = _trimAttendee(ev.attendees[i]);
      if (a) attendees.push(a);
    }
  }
  var out = {
    id: ev.id,
    summary: ev.summary || "",
    status: ev.status || "",
    start: ev.start || null,
    end: ev.end || null,
    attendees: attendees,
    location: ev.location || "",
    description: ev.description || "",
    organizer: ev.organizer || null,
    conferenceLink: _conferenceLink(ev),
    htmlLink: ev.htmlLink || "",
    updated: ev.updated || ""
  };
  if (ev.recurringEventId) out.recurringEventId = ev.recurringEventId;
  return out;
}

function _trimEvents(items) {
  var out = [];
  if (items) {
    for (var i = 0; i < items.length; i++) {
      var t = _trimEvent(items[i]);
      if (t) out.push(t);
    }
  }
  return out;
}

// --- tool methods -------------------------------------------------------

// listCalendars(opts) — the user's calendar list (calendarList.list).
// opts: { maxResults?, minAccessRole?, pageToken? }.
export function listCalendars(opts) {
  opts = opts || {};
  var qs = _qs({
    maxResults: _clampMax(opts.maxResults),
    minAccessRole: opts.minAccessRole,
    pageToken: opts.pageToken
  });
  var r = _get("/users/me/calendarList" + qs);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var body = r.body || {};
  var items = body.items || [];
  var calendars = [];
  for (var i = 0; i < items.length; i++) {
    var c = items[i];
    calendars.push({
      id: c.id,
      summary: c.summary || "",
      primary: !!c.primary,
      accessRole: c.accessRole || "",
      timeZone: c.timeZone || ""
    });
  }
  return { ok: true, calendars: calendars, nextPageToken: body.nextPageToken || null };
}

// listEvents(calendarId, opts) — events in a time window (events.list).
// calendarId defaults to "primary". opts: { timeMin?, timeMax? (RFC3339),
//   q?, maxResults?, pageToken?, syncToken? }. Expands recurrences
// (singleEvents=true, orderBy=startTime) unless a syncToken is given — an
// incremental call must drop timeMin/timeMax/orderBy/q (Google rejects the
// mix), so with syncToken we send only syncToken + paging.
export function listEvents(calendarId, opts) {
  calendarId = calendarId || "primary";
  opts = opts || {};
  var params;
  if (opts.syncToken) {
    params = { syncToken: opts.syncToken, pageToken: opts.pageToken,
      maxResults: _clampMax(opts.maxResults) };
  } else {
    params = {
      singleEvents: "true",
      orderBy: "startTime",
      timeMin: opts.timeMin,
      timeMax: opts.timeMax,
      q: opts.q,
      maxResults: _clampMax(opts.maxResults),
      pageToken: opts.pageToken
    };
  }
  var path = "/calendars/" + encodeURIComponent(calendarId) + "/events" + _qs(params);
  var r = _get(path);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var body = r.body || {};
  return {
    ok: true,
    calendarId: calendarId,
    events: _trimEvents(body.items),
    nextPageToken: body.nextPageToken || null,
    nextSyncToken: body.nextSyncToken || null,
    timeZone: body.timeZone || ""
  };
}

// getEvent(calendarId, eventId) — one event in full (events.get).
export function getEvent(calendarId, eventId) {
  calendarId = calendarId || "primary";
  if (!eventId || typeof eventId !== "string") {
    return { ok: false, error: "eventId is required" };
  }
  var path = "/calendars/" + encodeURIComponent(calendarId)
    + "/events/" + encodeURIComponent(eventId);
  var r = _get(path);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  return { ok: true, event: _trimEvent(r.body) };
}

// upcoming(opts) — convenience: events on the primary calendar from now
// forward (listEvents('primary', {timeMin: now, maxResults})).
// opts: { maxResults?, calendarId?, pageToken? }.
export function upcoming(opts) {
  opts = opts || {};
  return listEvents(opts.calendarId || "primary", {
    timeMin: new Date().toISOString(),
    maxResults: opts.maxResults,
    pageToken: opts.pageToken
  });
}

// main(args) — default entry point: the upcoming view on the primary calendar.
export function main(args) {
  return upcoming(args || {});
}
