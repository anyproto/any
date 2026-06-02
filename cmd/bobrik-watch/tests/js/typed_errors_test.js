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
  function one(k, v) { var o = {}; o[k] = v; return o; }

  c.createType({ name: T, properties: [{ key: "count", name: "Count", format: "number" }] });
  var obj = c.createObject(T, { name: "x_" + uniq, properties: { count: 1 } });
  h.check("setup createObject ok", obj && obj.ok, JSON.stringify(obj));

  // wrong kind: string into a number prop → server property.kind_mismatch,
  // threaded into updateObject's result.
  var km = c.updateObject(obj.id, { properties: one(T + ".count", "not a number") });
  h.check("kind mismatch: ok false", km && km.ok === false, JSON.stringify(km));
  h.check("kind mismatch: code surfaced", km && km.code === "property.kind_mismatch", JSON.stringify(km));

  // unknown property is caught CLIENT-SIDE by the resolver (fail fast, never
  // hits the server) — ok:false with a clear message, no server code.
  var unk = c.updateObject(obj.id, { properties: one(T + ".nope", "x") });
  h.check("unknown prop: ok false", unk && unk.ok === false, JSON.stringify(unk));
  h.check("unknown prop: clear message", unk && /unknown property/.test(unk.error || ""), JSON.stringify(unk));

  // 501 route: account-scope property write isn't implemented → api() maps it
  // to sdk.not_implemented.
  var r501 = c.api("POST", c.config.spacePath + "/properties/" + obj.id + "/account/" + T, { patch: {} });
  h.check("501 status", r501 && r501.status === 501, "" + (r501 && r501.status));
  h.check("501 code = sdk.not_implemented", r501 && r501.code === "sdk.not_implemented", JSON.stringify(r501 && r501.code));

  // success carries no error/code
  var ok = c.updateObject(obj.id, { properties: one(T + ".count", 42) });
  h.check("success ok, no code", ok && ok.ok === true, JSON.stringify(ok));

  return h.done();
}
