// createType is idempotent AND additive, and supports the `objects` (array)
// property format used by ref-list fields like chat_history. Run via
// jsrunner_test.go (TestJSAnyHelper).

import { createClient, getProp } from "anyHelper@v1";

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
  var uniq = "" + new Date().getTime();
  var typeName = "Anchor_" + uniq;

  // First declaration: one text prop.
  var t1 = c.createType({ name: typeName, properties: [{ key: "role", name: "Role", format: "text" }] });
  h.check("createType first ok + created", t1 && t1.ok && t1.created === true, JSON.stringify(t1));
  var tx = t1.type && t1.type.xKey; // stable handle for dotted paths
  h.check("createType derived xKey", tx === "anchor_" + uniq, "" + tx);

  // Second declaration of the SAME type adds a DIFFERENT prop — must be
  // additive, not an early-return that drops it (the init_agent/amemory bug).
  var t2 = c.createType({
    name: typeName,
    properties: [
      { key: "role", name: "Role", format: "text" },        // already there
      { key: "history", name: "History", format: "objects" }, // new, array kind
      { key: "count", name: "Count", format: "number" }       // new
    ]
  });
  h.check("createType second ok + not created", t2 && t2.ok && t2.created === false, JSON.stringify(t2));
  h.check("xKey stable across re-declare", t2.type && t2.type.xKey === tx, "" + (t2.type && t2.type.xKey));

  // Both the original and the newly-added props must now be writable.
  var co = c.createObject(tx, {
    name: "anchor",
    properties: { "role": "_main", "count": 3, "history": ["bafyA", "bafyB"] }
  });
  h.check("createObject with additive props ok", co && co.ok, JSON.stringify(co));

  var obj = c.getObject(co.id);
  h.check("read role", getProp(obj, tx + ".role") === "_main", getProp(obj, tx + ".role"));
  h.check("read count", getProp(obj, tx + ".count") === 3, "" + getProp(obj, tx + ".count"));
  var hist = getProp(obj, tx + ".history");
  h.check("read history is array", Array.isArray(hist) && hist.length === 2, JSON.stringify(hist));
  h.check("history element preserved", hist && hist[0] === "bafyA", JSON.stringify(hist));

  // Append to the objects field via updateObject (dotted).
  var up = c.updateObject(co.id, { properties: { } });
  up = c.updateObject(co.id, { properties: dottedOne(tx + ".history", ["bafyA", "bafyB", "bafyC"]) });
  h.check("update history ok", up && up.ok, JSON.stringify(up));
  var hist2 = getProp(c.getObject(co.id), tx + ".history");
  h.check("history grew to 3", Array.isArray(hist2) && hist2.length === 3, JSON.stringify(hist2));

  return h.done();
}

function dottedOne(k, v) { var o = {}; o[k] = v; return o; }
