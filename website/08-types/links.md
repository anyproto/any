---
title: Links
description: The canonical any:// link grammar — one self-describing URI for objects, dataset records, mentions, spaces, property values and files.
order: 40
---
# Links

Every reference from inside any to something else inside any is an `any://` URI: an object, a chat message, a member, a space, a property value, a file. One grammar, self-describing (a reader can tell *what* a URI points at without a lookup), extensible without a version bump, and small enough to sit unescaped inside a markdown link destination.

## The grammar

```
any://<kind>/<spaceId>[/<rest…>][?<params>][#<fragment>]
```

The **kind** is the first path segment. Prefix-match `any://o/`, `any://m/`, `any://f/` and you know what you are pointing at before resolving anything. `spaceId` is present in every kind — links are global by design.

| Kind | Shape | Points at |
|------|-------|-----------|
| `o` | `any://o/<spaceId>/<objectId>` | an object |
| `o` | `any://o/<spaceId>/<objectId>/<dataset>/<recordId>` | a record inside an object's dataset (a block, a message) |
| `o` | `any://o/<spaceId>/<objectId>/<dataset>/<recordId>/<propId>` | *(reserved)* a field on a record |
| `m` | `any://m/<spaceId>/<identity>` | an identity — a **mention** |
| `s` | `any://s/<spaceId>` | a space |
| `p` | `any://p/<spaceId>/<objectId>/<propId>` | a **property value** on an object |
| `f` | `any://f/<spaceId>/<fileId>` | a file |
| `i` | *reserved, not implemented* | an invite deep link |

Kind slugs occupy a reserved lexical namespace: a first segment of 1–4 lowercase alphanumeric bytes is always a kind; anything longer is a legacy bare id (below). Real ids are 40+ character base58 strings, so the two never collide.

## Objects and their records

The object is the addressing root. Appending `<dataset>/<recordId>` walks into one of its datasets — one composition rule for every built-in and runtime dataset alike:

```
any://o/<spaceId>/<objectId>/editor_blocks/<blockId>    a block in a document
any://o/<spaceId>/<objectId>/chat_messages/<msgId>      a message in a chat
```

`<dataset>` is the name exactly as it appears in `/query`'s `dataset` field; `<recordId>` is the record's derived id. A multi-record citation is a comma-joined list of such URIs.

## Mentions

A mention in message or block text is a markdown link whose destination is an `m` URI:

```
Hey [Zarko](any://m/<spaceId>/<identity>), take a look
```

The link **text** is the display name at time of writing — a snapshot, and what a non-aware renderer (raw markdown, export, grep) shows. A mention-aware client detects the `m/` prefix, resolves the current display name through [identities](../auth/identities.html), and renders the "magic" chip. Profiles are encrypted, so tolerate an unresolved identity and fall back to the snapshot text until its key arrives. In chat, the server derives each message's `mentions` array from exactly these links — see [chat](chat.html).

## Files

```
any://f/<spaceId>/<fileId>                 a file
any://f/<spaceId>/<fileId>?variant=thumb   a variant by tag
```

Files get their own kind because a file's payload row lives on a hidden derived child object that the generic query cannot reach. Two rules every consumer follows:

- **`fileId`, not `rootCid`.** `fileId` is the per-attach handle every files endpoint takes; `rootCid` is the encrypted-content address, shared across deduplicated attaches, and never appears in a URI. A re-upload is a new `fileId` — a link means "*this* attachment, as shared".
- A client detects the `f/` prefix and fetches bytes from `GET /v1/spaces/:spaceId/files/:fileId/content` (see [downloading](../files/downloading.html)). Chat `attachments` entries and editor image destinations (`![alt](any://f/<sp>/<fileId>)`) both use this form. Name and mime are member-only; a keyless renderer falls back to the link text.

## Property values

`any://p/<spaceId>/<objectId>/<propId>` is "this property's value on this object" — the citation target for "I took this figure from here". It is a *value* reference, keyed by the content-addressed `propId`, never a client-side `xKey`. How a client renders it (open the object, highlight the field) is a UI question.

## Params and fragments

Query params carry kind-specific modifiers that are not part of the target's identity (`?variant=thumb`). An unknown param must be ignored, never a parse failure.

`#fragment` is for **view-level anchors only** — "scroll here" — and never identifies a data record:

```
any://o/<spaceId>/<debugObjectId>#turn_3      scroll to turn 3 on a rendered page
```

Anything that identifies data goes in the path (`…/<dataset>/<recordId>`). A block is a record, not an anchor: blocks re-render and restructure.

## Extension policy

The kind registry is open, and adding a kind must not break an existing parser:

- **Unknown kind → degrade to a plain link.** Render the markdown link text; do not error, do not guess.
- **Unknown param → ignore it.**
- Prefix-match on the kind; never sniff id shapes.

> **Why it matters.** Links live inside encrypted, user-owned data that outlives any particular client build. A grammar that degrades gracefully means a document written by tomorrow's client still renders — with its links readable — in today's.

## Legacy bare forms

The pre-typed bare forms stay valid and mean **object**:

```
any://<objectId>              in-space object reference (the relation property-value form)
any://<spaceId>/<objectId>    global object reference
```

Relation property values use the one-segment form, and agent `debugLink` fields on chat messages use `any://<spaceId>/<objectId>#turn_<n>`. Parsers accept both as kind `o`; the link index canonicalises every written form to `any://o/<spaceId>/<objectId>` before keying backlinks on it; new typed references (mentions, files, citations, records) always use the explicit-kind form. No stored value is rewritten.

## Use the package, not a regex

The grammar ships as the public Go package `github.com/anyproto/any/anyuri` — builders (`BuildObject`, `BuildMention`, …), `Parse`, `IsValid`, `IsPropertyValueRef`, `ExtractLinks` (the sanctioned scanner the server's link index uses, with `Canonical` for the index-key form) and `ExtractMentions`, its filter the server uses to derive chat mentions. `Parse` distinguishes an unknown kind (`ErrKindUnknown`, degrade) from a malformed URI (`ErrInvalid`, reject); classify with `errors.Is`. Clients and agents import the rule rather than reimplementing it.

> **Note.** Invite deep links (`i`) are reserved but not implemented — invites are shared as raw text tokens today (see [invites](../collaboration/invites.html)). Inline transclusion of a live value is not a link either; a future embed block will *contain* an `any://` target rather than be one.
