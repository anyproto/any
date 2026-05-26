// __main_source
// subagent — delegate a task to a fresh toolcall_core instance.
//
// This module is the *tool-facing* surface. The actual reasoning loop lives
// in `toolcall_core@v1` (kept as a system program). Subagent just wraps a
// runProgram call with `__quiet: true`, so:
//   - chatReply inside the child is captured into the return value instead of
//     posting to the user's chat,
//   - chat history is not loaded or persisted (the sub-call is isolated),
//   - the parent's trace bag stays small (helper.runProgram already drops the
//     child's traces; __prepareTraces below also drops the runtime's own
//     `js.eval` auto-trace entries, which would otherwise leak the entire
//     toolcall_core source + inner trace tree into the parent kernel).
//
// Not safe to call from inside the same toolcall_core instance you're
// currently running — that would recurse the LLM loop. Spawn a sub-agent
// only when the parent agent has identified a self-contained subtask.

import { createClient } from "anyHelper@v1";

function _delegateImpl(text, opts) {
  var t = (text == null) ? "" : String(text);
  if (!t.trim()) {
    return "subagent.delegate: empty text — nothing to do";
  }

  // noTrace keeps the helper's wrapped runProgram call OUT of the parent's
  // callTrace — we already surface a single `subagent.delegate` entry and
  // don't want a duplicate `anyHelper.runProgram` line.
  var client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID,
    systemSpaceId: env.ANYTYPE_PRIVATE_SPACE_ID,
    noTrace: true
  });

  var runArgs = { text: t, __quiet: true };
  // Forward any caller-provided extras (e.g. botIdentity) without overriding
  // our quiet flag or the text.
  if (opts && typeof opts === "object") {
    for (var k in opts) {
      if (!opts.hasOwnProperty(k)) continue;
      if (k === "text" || k === "__quiet" || k === "version") continue;
      runArgs[k] = opts[k];
    }
  }

  var version = (opts && opts.version) || "v1";
  var res = client.runProgram("toolcall_core", runArgs, { version: version });
  if (!res || !res.ok) {
    return "subagent error: " + ((res && res.error) || "unknown");
  }
  return typeof res.result === "string" ? res.result : JSON.stringify(res.result);
}

// Surface delegate as a wrapped trace so the calling kernel sees a single
// `subagent.delegate(text) → reply` line in Effects, not the inner storm.
export var delegate = (typeof __wrapTrace === "function")
  ? __wrapTrace("subagent.delegate", _delegateImpl)
  : _delegateImpl;

// Drop the runtime's auto-recorded `js.eval` entries from the parent trace.
// runProgram internally calls js.eval with the toolcall_core source, and the
// runtime records that at the parent kernel's level — payload includes the
// full inner trace tree (tens to hundreds of KB). The semantic signal already
// lives in the `subagent.delegate` entry, so the duplicate is pure noise.
export function __prepareTraces(traces) {
  if (!traces || !traces["js.eval"]) return traces;
  var out = {};
  for (var k in traces) {
    if (k === "js.eval") continue;
    out[k] = traces[k];
  }
  return out;
}

export function main(args) {
  if (args && args.text) return delegate(args.text, args);
  return "subagent — call delegate(text, opts?) to delegate a task to a sub-agent. See Tool Description.";
}
