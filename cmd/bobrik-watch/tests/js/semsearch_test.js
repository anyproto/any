// semsearch@v1 result trimming: the inline-render contract. A worst-case
// default-limit reply (every data at the cap, CID recordIds) must stay under
// the run_cell kernel's 4000-char value-store inline budget even when
// pretty-printed — the stub it would otherwise trip forces the logs.get
// round-trip that derailed a real run (3-turn recovery detour). No server
// needed: the client is injected.

import { createSemSearch } from "semsearch@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}

var h = mkHarness();

// --- fake client capturing opts, returning a worst-case shaped result ------
var CID = "bafyreighxgqlpqfq2sutnmnxyu7kknr6mdb4iqp3vkvm3gzjjb6ms3yxje";
function longData() {
  var s = "";
  while (s.length < 500) s += "проектная заметка о предложенных упрощениях архитектуры ";
  return s;
}
function mkClient(hitCount) {
  var captured = {};
  return {
    captured: captured,
    search: function (query, opts) {
      captured.query = query;
      captured.opts = opts;
      var hits = [];
      for (var i = 0; i < hitCount; i++) {
        hits.push({
          scope: "chat",
          objectId: CID,
          dataset: "chat_messages",
          recordId: CID, // chat record ids are full CIDs — the worst case
          data: longData(),
          score: 0.07894737666112998 - i * 0.001
        });
      }
      return { ok: true, hits: hits, mode: "hybrid", vectorStatus: "used" };
    }
  };
}

// --- default limit -----------------------------------------------------------
var c = mkClient(8);
var ss = createSemSearch({ client: c });
var r = ss.search("proposed simplifications");
h.check("default limit injected", c.captured.opts && c.captured.opts.limit === 8,
  "opts.limit = " + (c.captured.opts && c.captured.opts.limit));

var c2 = mkClient(3);
createSemSearch({ client: c2 }).search("q", { limit: 50 });
h.check("explicit limit passes through", c2.captured.opts.limit === 50);

var c3 = mkClient(1);
createSemSearch({ client: c3 }).search("q", { scopes: ["basic"] });
h.check("caller opts survive the limit default",
  c3.captured.opts.limit === 8 && c3.captured.opts.scopes && c3.captured.opts.scopes[0] === "basic");

// --- trimming ---------------------------------------------------------------
h.check("ok result", r.ok === true && r.hits.length === 8);
h.check("data capped with ellipsis",
  r.hits[0].data.length === 161 && r.hits[0].data.slice(-1) === "…",
  "len=" + r.hits[0].data.length);
h.check("score rounded to 4 decimals", r.hits[0].score === 0.0789, "score=" + r.hits[0].score);

// The contract itself: worst-case reply, pretty-printed, stays inline.
var rendered = JSON.stringify(r, null, 2);
h.check("worst-case pretty-printed reply renders inline (<4000 chars)",
  rendered.length < 4000, "rendered=" + rendered.length + " chars");

// --- passthrough of non-ok / malformed results --------------------------------
var errClient = { search: function () { return { ok: false, error: "boom", code: "index.disabled" }; } };
var er = createSemSearch({ client: errClient }).search("q");
h.check("error envelope untouched", er.ok === false && er.code === "index.disabled");

h.done();
