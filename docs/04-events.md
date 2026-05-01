# Events / Subscriptions (deferred)

**Not in v1.** There is no `/subscribe` endpoint. Callers that need to
observe state poll with `GET /v1/spaces/:spaceId` (for metadata) or
`POST /v1/spaces/:spaceId/query` (for data).

## Why defer

The v1 goal is to surface the API shape quickly and let us see where the
SDK surface is painful. Subscriptions are the largest single piece of
machinery (long-lived connection, reconnection, overflow handling,
session-based filtering) and they don't need to be right-first-time.
Better to ship the rest, then decide — once we have real usage — whether
the transport should be WebSocket, SSE, HTTP long-poll, or something
else. That's this file's one job until we're ready: record the options
and the tradeoffs so the decision is quick when we get to it.

## Likely direction

**WebSocket**, bound to the same server on `127.0.0.1:7001`, upgraded
via an `/v2/subscribe` endpoint (note: bumps the version). Reasons:

- Language-binding support is uniformly good.
- The same connection can multiplex multiple subscriptions (subscribe
  to space A, then later subscribe to space B) without opening new
  HTTP connections.
- Future writes from the client (e.g. ack/flow-control frames) are
  trivial to add.

## Alternatives

- **NDJSON over a streaming HTTP response**. Simpler server; no
  upgrade; any HTTP client can consume it with a line reader. Downside:
  no standard for multiplexing, no flow control, reconnection is
  ad-hoc.
- **SSE**. Cleaner than NDJSON (has `Last-Event-ID`, event types,
  standard reconnect), but still unidirectional. If we never need
  client-originated frames, SSE is a fine pick; if we do, WebSocket
  is simpler overall.

## When we implement

Pin:

1. Transport (WebSocket most likely).
2. Multiplexing scheme — one connection per subscription, or multiplex
   via frame envelopes?
3. Event shape — mirror `space.Event` from the SDK, plus a framing
   envelope for errors, heartbeats, and future event kinds.
4. Reconnection — event id cursor? Best-effort re-query? Both?
5. Overflow — what the server does when the client is slow (drop
   oldest vs close the stream).
6. Versioning — bump to `/v2/` or add subscriptions as an additive
   `/v1/` feature? Leaning additive if we're careful.
