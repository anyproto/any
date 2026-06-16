// Typed errors propagate: anyHelper.api() surfaces the server's error.code
// (docs/06-errors.md), and the write helpers thread it into their {ok:false}
// returns so callers can switch on the code, not regex the message. Run via
// jsrunner_test.go.

import { createClient } from "anyHelper@v1";

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
  var T = "Typed_" + uniq;
  // one(typeKey, group, extra?) — build a { [typeKey]: group, ...extra } data
  // object (type-group keys are dynamic in tests).
  function one(k, v, extra) {
    var o = {};
    o[k] = v;
    if (extra) { for (var ek in extra) { if (Object.prototype.hasOwnProperty.call(extra, ek)) o[ek] = extra[ek]; } }
    return o;
  }

  var ct = c.createType({ name: T, properties: [{ key: "count", name: "Count", format: "number" }] });
  var tx = ct.type.xKey;  // type handle (xKey)
  var obj = c.createObject(tx, one(tx, { count: 1 }, { name: "x_" + uniq }));
  h.check("setup createObject ok", obj && obj.ok, JSON.stringify(obj));

  // wrong kind: string into a number prop → server property.kind_mismatch,
  // threaded into updateObject's result.
  var km = c.updateObject(obj.id, one(tx, { count: "not a number" }));
  h.check("kind mismatch: ok false", km && km.ok === false, JSON.stringify(km));
  h.check("kind mismatch: code surfaced", km && km.code === "property.kind_mismatch", JSON.stringify(km));

  // unknown property is caught CLIENT-SIDE by the resolver (fail fast, never
  // hits the server) — ok:false with a clear message, no server code.
  var unk = c.updateObject(obj.id, one(tx, { nope: "x" }));
  h.check("unknown prop: ok false", unk && unk.ok === false, JSON.stringify(unk));
  h.check("unknown prop: clear message", unk && /unknown property/.test(unk.error || ""), JSON.stringify(unk));

  // success carries no error/code
  var ok = c.updateObject(obj.id, one(tx, { count: 42 }));
  h.check("success ok, no code", ok && ok.ok === true, JSON.stringify(ok));

  return h.done();
}
