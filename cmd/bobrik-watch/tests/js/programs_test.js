// anyHelper program surface: saveProgram/getProgram/listPrograms round-trip
// over the program_source / program_description datasets (the foundation
// anyPrograms builds on). Run via jsrunner_test.go.

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
  // valid JS identifier required; unique-ish per run
  var name = "probeProg" + ("" + new Date().getTime()).slice(-7);
  var src = "// __main_source\nexport function main(args){ return 1+1; }\n// regex stays intact: /[\\s\\S]*?/";
  var md = "## Tool Description\nA probe program.\n\n## Tool Schema\n### main()\nreturns 2.";

  var saved = c.saveProgram({ name: name, source: src, markdown: md });
  h.check("saveProgram ok", saved && saved.ok, JSON.stringify(saved));

  // markdown without the required heading must be rejected (loud, not silent).
  var bad = c.saveProgram({ name: name + "x", source: "x", markdown: "no heading here" });
  h.check("saveProgram rejects md without Tool Description", bad && bad.ok === false, JSON.stringify(bad));

  // invalid identifier rejected
  var badName = c.saveProgram({ name: "has-dash", source: "x" });
  h.check("saveProgram rejects bad identifier", badName && badName.ok === false, JSON.stringify(badName));

  // getProgram round-trips source + markdown byte-for-byte (incl. the regex).
  var got = c.getProgram(name, "v1");
  h.check("getProgram found", !!got, JSON.stringify(got));
  h.check("getProgram source exact", got && got.source === src, JSON.stringify(got && got.source));
  h.check("getProgram markdown exact", got && got.markdown === md, JSON.stringify(got && got.markdown));

  // update path: re-save changes source, same id.
  var src2 = src + "\n// v2";
  var saved2 = c.saveProgram({ name: name, source: src2, markdown: md });
  h.check("saveProgram update ok + same id", saved2 && saved2.ok && saved2.object.id === saved.object.id, JSON.stringify(saved2));
  h.check("getProgram reflects update", c.getProgram(name, "v1").source === src2);

  // listPrograms includes it.
  var progs = c.listPrograms();
  h.check("listPrograms includes our program", progs.some(function (p) { return p.name === name; }), JSON.stringify(progs.map(function (p) { return p.name; })));

  return h.done();
}
