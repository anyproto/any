# Anytype 2.0 — MVP v0.1 (proposal)

> Status: **proposal / ideation output**, not yet committed scope. This
> doc captures the product framing for Anytype 2.0 sitting on top of the
> `any` binary. Same rule as other `docs/NN-*.md`: update in the same
> change as anything that contradicts it.

## One-line vision

> **A local-first context engine that turns your meetings into a second brain
> — and quietly builds the tools you wish existed on top of your data.**
> Software that grows with you.

## The problem we're solving

The user (a founder-CEO) is the human bottleneck for their company's
context, commitments, and tools — and their brain doesn't scale. Every
symptom on the wishlist is a leak from that single bottleneck:

- repeating the same answers to different people,
- forgetting commitments and unanswered emails,
- walking into meetings under-prepared,
- no time to reflect on whether meetings or comms are actually working,
- a "wiki" that's stale the day it's written,
- recurring manual work that should be a small tool but never gets built.

Existing tools (Notion, Granola, Mem, Glean, Hebbia, etc.) attack one
slice each and all assume cloud. None of them combine *unified context*,
*local-first ownership*, and *agents that build on top of your data*.

## Thesis

Three capabilities, applied to one domain (meetings + their downstream
artifacts), generate every use case on the wishlist:

1. **Memory.** The agent remembers everything you've ever said, decided,
   or committed to — across meetings, calendar, email — and can answer
   *as you*, with sources.
2. **Surfacing.** It brings the right thing to your attention at the
   right moment: pre-meeting prep, dropped commitments, unanswered
   email, "you're about to repeat yourself."
3. **Building.** It constructs new artifacts on top of that context —
   trackers, dashboards, mini-apps, draft docs — on demand.

Everything else (auto-wiki, communication coaching, productivity insights,
automations) is downstream of these three. We do not ship them as
features in v0.1 — we let them emerge in v0.2+.

## ICP

**Founder-CEO of a 30–200 person company.** Specifically, the kind that:

- runs 15–30 meetings/week,
- lives in calendar + email + Slack + a notes tool,
- has at least one investor relationship and a board,
- has the technical taste to appreciate "local-first."

VCs are the **second wave**, not the wedge — they share ~70% of the
workflow shape and become the warm distribution amplifier once the
founder ICP is locked.

The existing **100k Anytype MAU is a beta pool and feedback asset**, not
an audience to design for. Many already use Anytype for meeting notes;
the upgrade path is natural, but we do not let "what PKM users want"
steer the v0.1 roadmap.

## The three demo moments

These are how the three capabilities surface in product:

| # | Capability | Demo moment | Role in funnel |
|---|------------|-------------|----------------|
| 1 | Memory | Hotkey in Slack/email → agent answers a question with your own past words, with sources. *"You answered this in 3 places already — want to send the synthesis?"* | **Acquisition hook.** High-frequency, instantly demoable. |
| 2 | Surfacing | Monday morning brief is waiting — every meeting prepped, every dropped commitment surfaced, every overdue email flagged with a draft. | **Habit lock.** Daily ritual that drives retention. |
| 3 | Building | "I wish I could see who I haven't 1:1'd in 4 weeks." Agent ships a live tracker in 60 seconds. | **Differentiator / virality.** No competitor can credibly do this. The moment they tell another founder. |

All three ship in v0.1. Onboarding leads with #1. #2 forms the daily
loop. #3 is the moment that earns word-of-mouth.

## The 30-day user arc

- **Day 1 (install hook):** Memory. They connect calendar + drop in
  recent transcripts + connect Gmail. First "wow" queries hit. One
  mini-app shipped before they close the laptop.
- **Day 3–7 (habit forms):** Surfacing. Morning briefs become routine.
  Pre-meeting prep auto-appears.
- **Day 10–20 ("holy shit" moment):** Building. They request their first
  custom tracker; it works.
- **Day 30+ (lock-in):** Corpus is rich. Coaching, insights, auto-wiki,
  automations start emerging from the same engine — without new product
  surface area.

## Day 1 — the magical onboarding (10 minutes, 6 beats)

This is the v0.1 onboarding flow. Each beat produces a visible artifact.

1. **The interview (90 sec).** Conversational chat. Five questions, no
   forms. Output: a profile of the user the agent now holds in context.
2. **The pre-built workspace (instant).** Based on answers, the agent
   generates an initial space with typed objects: People, Meetings,
   Projects, Companies, Decisions, Commitments. Living schema, sensible
   properties, ready to be populated. **No empty state.**
3. **The import wizard (60 sec).** Three suggested imports, two
   one-click: Google Calendar OAuth, Gmail OAuth, manual transcript
   drop-zone. No 17-integration matrix.
4. **The build (visible, ~30 sec).** Live ingestion with a progress
   strip: *"Found 47 people across 134 meetings. 12 recurring. 8
   projects. 23 commitments. 6 dominant topics."* Watching the system
   populate is itself the magic.
5. **The three "wow" queries (the moment).** Without being asked, the
   agent runs three pre-tuned queries on the fresh corpus and shows
   real, surprising answers. Examples:
   - *"You usually meet weekly with Sarah, but haven't in 5 weeks. Last topic: pricing."*
   - *"You committed to 7 things in the last 10 meetings. 3 moved, 4 didn't."*
   - *"Most-discussed unresolved topic across your last 20 meetings: 'enterprise tier' (14 references)."*
6. **The build-a-tool offer.** *"Want me to build a live tracker for
   your open commitments? Or a 1:1 prep page for your direct reports?"*
   Pick one, agent ships it in 60 seconds.

End state of Day 1: populated workspace + 3 "wow" answers + 1 personal
mini-app + a hook for tomorrow morning.

## V0.1 scope

### IN

- **Conversational onboarding agent** (cloud LLM, BYO API key acceptable
  for v0.1).
- **One starter blueprint: Founder/CEO.** (Founder + VC is a stretch
  goal; default to Founder only to ship faster.)
- **Auto-generated typed schema** with properties per object type
  (People, Meetings, Projects, Companies, Decisions, Commitments).
- **Google Calendar OAuth import.**
- **Gmail OAuth import** (read-only, last 90 days).
- **Manual transcript drop-zone** — `.txt` / `.md` from Granola, Otter,
  Fathom, ChatGPT export, etc.
- **Auto-extraction pipeline:** People, Companies, Topics, Commitments,
  Decisions extracted from transcripts + emails.
- **Auto-built collections** — 3–5 generated query views per blueprint.
- **The three "wow" queries** at end of onboarding (hand-tuned per
  blueprint).
- **Mini-app generator, single fixed shape:** "live filtered collection
  with a couple of computed fields." Open-ended generation deferred.
- **Slack-style hotkey for the Memory demo moment** — invocable from a
  desktop overlay, surfaces the answer + sources from the local corpus.
- **Local-first storage** on top of `any`'s existing data dir
  (see `docs/02-server.md`). Outbound network calls only for the LLM.

### OUT (deferred to v0.2+)

- Slack ingest (high value, big eng lift).
- Live transcript capture (use imports — solves 80% of value for now).
- Multi-shape mini-app generator / app marketplace.
- Scheduled jobs / Monday morning brief automation
  (manual trigger only in v0.1).
- Mobile / web (desktop-only v0.1).
- Team features, sharing, permissions.
- Coaching layer / communication insights.
- Auto-wiki as a first-class feature (emerges from Memory later).
- Local LLM option (cloud only in v0.1).
- Custom blueprints beyond the starter(s).
- VC blueprint (defer unless we explicitly choose to ship two).

## The three hard technical bets

V0.1 lives or dies on these:

1. **Auto-extraction quality.** People + Companies + Commitments +
   Topics out of free-text transcripts is the entire foundation. If
   extraction is sloppy, the wow queries fail. Worth heavy investment
   and per-blueprint prompt tuning.
2. **"Wow query" calibration.** Three pre-set queries per blueprint must
   feel uncannily true on real data. Hand-tuned for v0.1; learn the
   patterns before generalizing.
3. **Mini-app generator reliability.** One fixed shape, but it must work
   *every time*, on every reasonable user request. A flaky generator
   kills the differentiator demo.

## Open decisions (to lock before code starts)

1. **Form factor.** Is this the existing Anytype app evolving into 2.0,
   a new desktop shell on the same data, or a desktop overlay
   (Spotlight-style) that pairs with the existing app? Cascades into
   everything.
2. **One blueprint or two for v0.1?** Founder only ships faster.
   Founder + VC doubles the beachhead but doubles the tuning surface.
3. **Gmail in v0.1 or hold for v0.2?** Second-highest-value integration
   after calendar but doubles the auth/parsing surface.
4. **Mini-app generator: fixed shape vs open-ended?** Recommendation
   above is *fixed*, but worth an explicit call.
5. **Launch shape.** Private beta to founder/VC network + slice of
   existing 100k MAU? Or a public 2.0 announcement?

## What this is built on

The `any` binary (this repo) provides the local-first foundation: HTTP
server, wallet, spaces, objects, types, properties, the `nav` virtual
built-in. See `docs/00-overview.md` and `docs/02-server.md`.

Anytype 2.0 sits on top:

- Onboarding agent + extraction pipeline + mini-app generator are new
  product surface (in `internal/server/agent/` — to be created — and a
  new desktop shell, depending on form-factor decision).
- Calendar / Gmail / transcript ingest become new server-side import
  endpoints under `/v1/imports/...` (also new).
- Wow queries and auto-collections are stored as ordinary
  Anytype objects via the existing object/query endpoints.
- The mini-app generator emits views that live as objects in a space —
  no new persistence layer.

Architectural invariants from `CLAUDE.md` still hold: Echo + `/v1/`
prefix, localhost-only, uniform error shape, CLI maps 1:1 onto
endpoints.

## What this doc explicitly is *not*

- Not a v2.0 roadmap. v0.1 is the wedge; v0.2+ is intentionally out of
  scope here.
- Not a pricing or GTM doc. Both deferred.
- Not a design spec. UI surface decisions wait on the form-factor call.
- Not a commitment to build all of this. This is the proposal we're
  ideating against.
