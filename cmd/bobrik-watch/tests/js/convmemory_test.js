// convmemory storage round-trip over the server's agent data layer
// (docs/11-agent-memory.md): brain resolution, typed memory items
// (create / category filter / evolve), and the layered history contract —
// append-only turns, chunk with explicit fromSeq..toSeq pointers, and the
// chunk → raw-turns drill-down. Run via jsrunner_test.go against a live
// server (ANY_ADDR).

import { createClient } from "anyHelper@v1";
import { createConvMemory, detectTemporalReference } from "convmemory@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}

export function main(args) {
  var h = mkHarness();
  var c = createClient({ apiBaseUrl: args.apiBaseUrl, spaceId: args.spaceId, noTrace: true });
  var conv = createConvMemory(c);
  var uniq = "" + new Date().getTime();

  // --- brain: deterministic, cached -----------------------------------------
  var brain1 = conv.brainId();
  var brain2 = conv.brainId();
  h.check("brainId non-empty", typeof brain1 === "string" && brain1.length > 10, JSON.stringify(brain1));
  h.check("brainId stable", brain1 === brain2);

  // --- memory items -----------------------------------------------------------
  var ctx = "test pref " + uniq;
  var add = conv.addMemory("body for " + uniq, {
    category: "preference", context: ctx, tags: ["t_" + uniq], entities: ["thing"]
  });
  h.check("addMemory ok", add && add.ok === true, JSON.stringify(add));
  h.check("addMemory returns id", add && typeof add.id === "string" && add.id.length > 0);

  var noCat = conv.addMemory("x", { context: "no category" });
  h.check("addMemory requires category", noCat && noCat.ok === false);

  var items = conv.byCategory(["preference"], 100);
  var found = null;
  for (var i = 0; i < items.length; i++) {
    if (items[i].id === add.id) { found = items[i]; break; }
  }
  h.check("byCategory finds item", !!found, "id " + add.id + " not in " + items.length + " rows");
  if (found) {
    h.check("server defaults applied", found.confidence === 5 && found.importance === 5 &&
      found.salience === 10 && found.accessCount === 0, JSON.stringify(found));
    h.check("creator stamped", typeof found.creator === "string" && found.creator.length > 0);
    h.check("tags round-trip as array", Array.isArray(found.tags) && found.tags[0] === "t_" + uniq);
  }

  var ev = conv.evolveMemory(add.id, { salience: 3 });
  h.check("evolveMemory ok", ev && ev.ok === true, JSON.stringify(ev));
  var after = conv.memoryQuery({ id: add.id }, null, 1);
  h.check("evolved salience persisted", after.length === 1 && after[0].salience === 3,
    JSON.stringify(after[0] && after[0].salience));

  var cats = conv.listCategories();
  h.check("listCategories shape", cats && Array.isArray(cats.builtin) && Array.isArray(cats.observed));
  var sawPref = false;
  for (var oi = 0; oi < cats.observed.length; oi++) {
    if (cats.observed[oi].name === "preference") sawPref = true;
  }
  h.check("observed includes preference", sawPref, JSON.stringify(cats.observed));

  // --- history: turns + chunk + drill-down -----------------------------------
  // Host object: any object can carry the agent_turns/agent_chunks datasets
  // (ensureType attaches agent_log on first write) — mint a fresh one so seq
  // starts at -1.
  var hostRes = c.api("POST", "/v1/spaces/" + c.config.spaceId + "/objects", {});
  h.check("host object created", hostRes.ok && hostRes.data && hostRes.data.objectId, JSON.stringify(hostRes.error));
  var host = hostRes.data.objectId;

  h.check("lastSeq empty = -1", conv.lastSeq(host) === -1);

  for (var seq = 0; seq < 3; seq++) {
    var ap = conv.appendTurn(host, {
      seq: seq, userName: "tester", userText: "msg " + seq,
      replies: ["reply " + seq], effects: ["created Thing [x](any://s/o)"],
      llm: { stopReason: "end_turn", inTokens: 10, outTokens: 5 }
    });
    h.check("appendTurn " + seq + " ok", ap && ap.ok === true, JSON.stringify(ap));
  }
  h.check("lastSeq after appends", conv.lastSeq(host) === 2);

  var recent = conv.recentTurns(host, 2);
  h.check("recentTurns window", recent.length === 2 && recent[0].seq === 1 && recent[1].seq === 2,
    JSON.stringify(recent.map(function (t) { return t.seq; })));
  h.check("turn fields round-trip", recent[1].userText === "msg 2" &&
    Array.isArray(recent[1].replies) && recent[1].replies[0] === "reply 2" &&
    recent[1].llm && recent[1].llm.stopReason === "end_turn", JSON.stringify(recent[1]));
  h.check("turn creator stamped", typeof recent[1].creator === "string" && recent[1].creator.length > 0);

  // Append-only: same seq again must be rejected, record unchanged.
  var dup = conv.appendTurn(host, { seq: 2, userText: "overwrite attempt" });
  h.check("duplicate seq rejected", dup && dup.ok === false, JSON.stringify(dup));
  var still = conv.turnRange(host, 2, 2);
  h.check("turn immutable after dup", still.length === 1 && still[0].userText === "msg 2");

  // Chunk with explicit pointers, then drill down.
  var t01 = conv.turnRange(host, 0, 1);
  var ck = conv.createChunk(host, {
    seq: 0, summary: "tester sent msg 0 and msg 1; agent replied to both",
    periodStart: t01[0].createdAt, periodEnd: t01[1].createdAt,
    fromSeq: 0, toSeq: 1, turnsCovered: 2
  });
  h.check("createChunk ok", ck && ck.ok === true, JSON.stringify(ck));

  var chunks = conv.recentChunks(host, 5);
  h.check("recentChunks returns chunk", chunks.length === 1 && chunks[0].fromSeq === 0 && chunks[0].toSeq === 1,
    JSON.stringify(chunks));

  var ex = conv.expandChunk(host, 0);
  h.check("expandChunk ok", ex && ex.ok === true, JSON.stringify(ex && ex.error));
  h.check("expandChunk returns raw range", ex.turns && ex.turns.length === 2 &&
    ex.turns[0].userText === "msg 0" && ex.turns[1].userText === "msg 1",
    JSON.stringify(ex.turns && ex.turns.map(function (t) { return t.userText; })));

  // Inverted chunk pointers rejected server-side.
  var badCk = conv.createChunk(host, {
    seq: 1, summary: "bad", periodStart: 1, periodEnd: 2, fromSeq: 5, toSeq: 2
  });
  h.check("inverted chunk range rejected", badCk && badCk.ok === false);

  // --- temporal detection (pure date math, ported from amemory) ---------------
  var det = detectTemporalReference("what did we do yesterday?");
  h.check("detectTemporalReference yesterday", det && det.from && det.until, JSON.stringify(det));
  h.check("detectTemporalReference none", detectTemporalReference("no time words here") === null);

  return h.done();
}
