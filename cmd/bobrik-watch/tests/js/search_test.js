// search@v1 RLM loop mechanics with a scripted mock LLM (no real provider
// calls): final-sentinel termination, cell containment (new Function scope),
// error recovery, nudge, wrap-up on caps, fallback-recency, and the stats
// accounting (turns / toolcalls / subCalls / tokens / ms). Candidate reads run
// against the REAL server (memory items created below). Run via
// jsrunner_test.go against a live server (ANY_ADDR).

import { createClient } from "anyHelper@v1";
import { createConvMemory } from "convmemory@v1";
import { createSearch } from "search@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}

// Scripted mock LLM: each call to chat() shifts the next handler; a handler
// gets (messages, opts) and returns the Anthropic-shaped response.
function mkMockLLM(handlers) {
  var calls = [];
  return {
    calls: calls,
    chat: function (messages, opts) {
      // snapshot — the loop keeps appending to the same messages array after
      // this call, so storing the reference would show later state
      calls.push({ messages: JSON.parse(JSON.stringify(messages)), opts: opts });
      if (handlers.length === 0) throw new Error("mock llm.chat exhausted after " + calls.length + " calls");
      var h = handlers.shift();
      return h(messages, opts);
    },
    classify: function () { throw new Error("mock llm.classify not scripted"); }
  };
}

function cellTurn(code) {
  return function () {
    return {
      content: [{ type: "tool_use", id: "tu_" + Math.random().toString(36).slice(2, 8), name: "run_cell", input: { code: code } }],
      stop_reason: "tool_use",
      usage: { input_tokens: 100, output_tokens: 10 }
    };
  };
}

function textTurn(text) {
  return function () {
    return {
      content: [{ type: "text", text: text }],
      stop_reason: "end_turn",
      usage: { input_tokens: 100, output_tokens: 10 }
    };
  };
}

// Mock batched classifier: scores every snippet in every prompt with a fixed
// score, returns usage per prompt so stats.tokensIn/Out accounting is testable.
function mkMockBatch(score) {
  return function (prompts) {
    var out = [];
    for (var i = 0; i < prompts.length; i++) {
      var section = prompts[i].split("\nSnippets:\n")[1] || "";
      var lines = section.split("\n");
      var entries = [];
      for (var li = 0; li < lines.length; li++) {
        var m = lines[li].match(/^(\d+)\. /);
        if (m) entries.push({ n: parseInt(m[1], 10), score: score, why: "mock match" });
      }
      out.push({ text: JSON.stringify(entries), inTokens: 50, outTokens: 5 });
    }
    return out;
  };
}

export function main(args) {
  var h = mkHarness();
  var client = createClient({ apiBaseUrl: args.apiBaseUrl, spaceId: args.spaceId, noTrace: true });
  var conv = createConvMemory(client);
  var uniq = "" + new Date().getTime();

  // Seed real memory items so candidates('memory') has rows to return.
  for (var i = 0; i < 3; i++) {
    var add = conv.addMemory("search test body " + uniq + " #" + i, {
      category: "claim", context: "search test item " + uniq + " #" + i
    });
    h.check("seed memory item " + i, add && add.ok === true, JSON.stringify(add));
  }

  function mkSearch(handlers, deps) {
    var llm = mkMockLLM(handlers);
    var base = {
      client: client, conv: conv, llm: llm,
      batch: mkMockBatch(8),
      spaceId: args.spaceId
    };
    for (var k in (deps || {})) base[k] = deps[k];
    return { inst: createSearch(base), llm: llm };
  }

  // --- 1. happy path: candidates → mapClassify → final ----------------------
  var happyCell =
    "var cands = rlm.candidates('memory', {limit: 20});\n" +
    "var scored = rlm.mapClassify(cands, query);\n" +
    "var byId = {}; cands.forEach(function(c){ byId[c.id] = c; });\n" +
    "scored.sort(function(a,b){ return (b.score||0)-(a.score||0); });\n" +
    "var top = scored.slice(0, 3).map(function(r){ var c = byId[r.id];\n" +
    "  return {id: r.id, kind: 'item', score: r.score, title: c.title, snippet: c.text, why: r.why}; });\n" +
    "return rlm.final({results: top, note: 'scanned ' + cands.length + ' items'});";

  var s1 = mkSearch([cellTurn(happyCell)]);
  var r1 = s1.inst.search("search test item " + uniq, { scope: "memory", k: 3 });
  h.check("happy: ok", r1 && r1.ok === true, JSON.stringify(r1 && { ok: r1.ok, mode: r1.mode, error: r1.error }));
  h.check("happy: mode rlm", r1.mode === "rlm", r1.mode);
  h.check("happy: results returned", Array.isArray(r1.results) && r1.results.length > 0, JSON.stringify(r1.results && r1.results.length));
  if (r1.results && r1.results.length > 0) {
    var rec = r1.results[0];
    h.check("happy: record shape", rec.id && rec.kind === "item" && typeof rec.score === "number" &&
      typeof rec.snippet === "string" && typeof rec.why === "string", JSON.stringify(rec));
    h.check("happy: source link", rec.source && rec.source.objectId && rec.source.link &&
      rec.source.link.indexOf("any://" + args.spaceId + "/") === 0, JSON.stringify(rec.source));
  }
  h.check("happy: note passthrough", typeof r1.note === "string" && r1.note.indexOf("scanned") === 0, r1.note);
  h.check("happy: stats rootTurns=1", r1.stats.rootTurns === 1, JSON.stringify(r1.stats));
  h.check("happy: stats toolcalls=1", r1.stats.toolcalls === 1);
  h.check("happy: stats batchedSubCalls=1", r1.stats.batchedSubCalls === 1);
  h.check("happy: stats subCalls>=1", r1.stats.subCalls >= 1);
  h.check("happy: stats objectsScanned>=3", r1.stats.objectsScanned >= 3, "" + r1.stats.objectsScanned);
  // tokens: 1 root turn (100/10) + subCalls batched prompts (50/5 each)
  h.check("happy: tokensIn accounted", r1.stats.tokensIn === 100 + r1.stats.subCalls * 50, JSON.stringify(r1.stats));
  h.check("happy: tokensOut accounted", r1.stats.tokensOut === 10 + r1.stats.subCalls * 5, JSON.stringify(r1.stats));
  h.check("happy: ms measured", typeof r1.stats.ms === "number" && r1.stats.ms >= 0);
  h.check("happy: not wrappedUp", r1.stats.wrappedUp === false);

  // --- 2. containment: cell vars don't leak to globalThis -------------------
  var s2 = mkSearch([
    cellTurn("var leakProbeSearchTest = 42; return leakProbeSearchTest;"),
    cellTurn("return rlm.final({results: []});")
  ]);
  var r2 = s2.inst.search("containment", { scope: "memory" });
  h.check("containment: search ok", r2.ok === true, r2.mode + " " + (r2.error || ""));
  h.check("containment: no var leak", typeof globalThis.leakProbeSearchTest === "undefined");
  h.check("containment: 2 toolcalls", r2.stats.toolcalls === 2, "" + r2.stats.toolcalls);

  // --- 3. scratch persists across cells; large value preview ----------------
  var s3 = mkSearch([
    cellTurn("s.acc = []; for (var i = 0; i < 500; i++) s.acc.push({i: i, pad: 'xxxxxxxxxxxxxxxx'}); return s.acc;"),
    cellTurn("return rlm.final({results: [], note: 'acc=' + s.acc.length + ' stash=' + (s._1 ? s._1.length : 0)});")
  ]);
  var r3 = s3.inst.search("scratch", { scope: "memory" });
  h.check("scratch: persisted across cells + lossless stash", r3.note === "acc=500 stash=500", r3.note);
  // the big return must have produced a preview tool_result, not the full dump
  var s3msgs = s3.llm.calls[1].messages;
  var s3lastResult = s3msgs[s3msgs.length - 1].content[0].content;
  h.check("scratch: large value previewed", s3lastResult.indexOf("kept LOSSLESS in s._1") !== -1 &&
    s3lastResult.length < 3000, s3lastResult.substring(0, 120));

  // --- 4. cell error surfaces as is_error; loop recovers ---------------------
  var s4 = mkSearch([
    cellTurn("this is not javascript"),
    function (messages) {
      var last = messages[messages.length - 1].content[0];
      if (!(last.is_error === true && last.content.indexOf("Syntax error") !== -1)) {
        throw new Error("expected syntax-error tool_result, got: " + JSON.stringify(last));
      }
      return cellTurn("return rlm.final({results: []});")();
    }
  ]);
  var r4 = s4.inst.search("error recovery", { scope: "memory" });
  h.check("error: recovered to final", r4.ok === true && r4.mode === "rlm", r4.mode + " " + (r4.error || ""));
  h.check("error: 2 turns", r4.stats.rootTurns === 2, "" + r4.stats.rootTurns);

  // --- 5. nudge: text-only end_turn without final → one nudge ---------------
  var s5 = mkSearch([
    textTurn("I think I'm done."),
    function (messages) {
      var last = messages[messages.length - 1].content;
      if (String(last).indexOf("rlm.final") === -1) throw new Error("expected nudge, got: " + last);
      return cellTurn("return rlm.final({results: []});")();
    }
  ]);
  var r5 = s5.inst.search("nudge", { scope: "memory" });
  h.check("nudge: recovered to final", r5.ok === true && r5.mode === "rlm", r5.mode);
  h.check("nudge: 2 turns", r5.stats.rootTurns === 2, "" + r5.stats.rootTurns);

  // --- 6. wrap-up honored: caps hit → WRAP-UP → final ------------------------
  var s6 = mkSearch([
    cellTurn("return 1;"),
    cellTurn("return 2;"),
    function (messages) {
      var last = String(messages[messages.length - 1].content);
      if (last.indexOf("WRAP-UP") === -1) throw new Error("expected WRAP-UP after cap, got: " + last);
      return cellTurn("return rlm.final({results: [], note: 'wrapped'});")();
    }
  ], { maxToolcalls: 2 });
  var r6 = s6.inst.search("wrapup honored", { scope: "memory" });
  h.check("wrapup: ok with final", r6.ok === true && r6.mode === "rlm", r6.mode + " " + (r6.error || ""));
  h.check("wrapup: wrappedUp flagged", r6.stats.wrappedUp === true);
  h.check("wrapup: note passthrough", r6.note === "wrapped", r6.note);

  // --- 7. wrap-up ignored → fallback-recency with seeded items --------------
  var s7 = mkSearch([
    cellTurn("return 1;"),
    cellTurn("return 2;"),
    cellTurn("return 3;") // wrap-up turn still no final
  ], { maxToolcalls: 2 });
  var r7 = s7.inst.search("wrapup ignored", { scope: "memory", k: 5 });
  h.check("fallback: mode", r7.mode === "fallback-recency", r7.mode);
  h.check("fallback: still ok", r7.ok === true);
  h.check("fallback: wrappedUp", r7.stats.wrappedUp === true);
  h.check("fallback: recency results present", Array.isArray(r7.results) && r7.results.length > 0,
    "" + (r7.results && r7.results.length));
  if (r7.results && r7.results.length > 0) {
    h.check("fallback: labeled", r7.results[0].why === "recency fallback", r7.results[0].why);
  }

  // --- 8. ask(): synthesize plumbs answer + citations ------------------------
  var s8 = mkSearch([
    cellTurn("return rlm.final({results: [{id: 'x1', kind: 'item', score: 9, title: 't', snippet: 'sn', why: 'w'}], " +
      "answer: 'the answer', citations: [{id: 'x1'}]});")
  ]);
  var r8 = s8.inst.ask("question?");
  h.check("ask: answer present", r8.answer === "the answer", JSON.stringify(r8.answer));
  h.check("ask: citations present", Array.isArray(r8.citations) && r8.citations[0].id === "x1");

  // --- 9. system prompt carries corpus metadata, not corpus ------------------
  var sys1 = s1.llm.calls[0].opts.system;
  h.check("system: memory metadata present", typeof sys1 === "string" && sys1.indexOf("memory:") !== -1 &&
    sys1.indexOf("Categories observed") !== -1, (sys1 || "").substring(0, 80));
  h.check("system: cell contract present", sys1.indexOf("FUNCTION BODY") !== -1 && sys1.indexOf("rlm.final") !== -1);
  h.check("system: decomposition examples present", sys1.indexOf("Decomposition patterns") !== -1);
  h.check("system: no seeded item bodies leaked", sys1.indexOf("search test body " + uniq) === -1);

  // --- 10. query validation ---------------------------------------------------
  var r10 = s1.inst.search("");
  h.check("validation: empty query rejected", r10.ok === false && r10.mode === "error", JSON.stringify(r10));

  return h.done();
}
