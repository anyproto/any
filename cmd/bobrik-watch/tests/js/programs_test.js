// Program write/read surface: anyPrograms.saveProgram (the one write path —
// anyHelper no longer has saveProgram/saveTool) against the SPLIT tool-doc
// storage: program_description = description body, program_methods = one
// record per method, program.any_tool gates getTools. Run via jsrunner_test.go.

import { createClient } from "anyHelper@v1";
import { saveProgram } from "anyPrograms@v1";

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
  var md = "## Tool Description\n\nA probe program.\n\n## Tool Schema\n\n### main() [getter]\n\nreturns 2.\n\n### probe(x) [mutator]\n\nwrites x.";

  var saved = saveProgram({ name: name, source: src, markdown: md });
  h.check("saveProgram ok", saved && saved.ok, JSON.stringify(saved));

  // markdown without the required heading must be rejected (loud, not silent).
  var bad = saveProgram({ name: name + "x", source: "x", markdown: "no heading here" });
  h.check("saveProgram rejects md without Tool Description", bad && bad.ok === false, JSON.stringify(bad));

  // markdown without a Tool Schema (no methods) is not a tool — rejected.
  var noSchema = saveProgram({ name: name + "y", source: "x", markdown: "## Tool Description\n\nOnly a description." });
  h.check("saveProgram rejects md without methods", noSchema && noSchema.ok === false, JSON.stringify(noSchema));

  // invalid identifier rejected
  var badName = saveProgram({ name: "has-dash", source: "x" });
  h.check("saveProgram rejects bad identifier", badName && badName.ok === false, JSON.stringify(badName));

  // getProgram round-trips the SPLIT docs: source byte-exact, description =
  // the Tool Description body only, methods = ordered records.
  var got = c.getProgram(name, "v1");
  h.check("getProgram found", !!got, JSON.stringify(got));
  h.check("getProgram source exact", got && got.source === src, JSON.stringify(got && got.source));
  h.check("getProgram description is split body", got && got.description === "A probe program.", JSON.stringify(got && got.description));
  h.check("getProgram markdown aliases description", got && got.markdown === got.description);
  h.check("getProgram methods split", got && got.methods && got.methods.length === 2 &&
    got.methods[0].bareName === "main" && got.methods[0].name === "main()" && got.methods[0].kind === "getter" &&
    got.methods[1].bareName === "probe" && got.methods[1].name === "probe(x)" && got.methods[1].kind === "mutator" &&
    got.methods[1].text === "writes x.",
    JSON.stringify(got && got.methods));

  // getToolDocs reads the same split off the datasets directly.
  var docs = c.getToolDocs(got.id);
  h.check("getToolDocs description", docs && docs.description === "A probe program.", JSON.stringify(docs));
  h.check("getToolDocs methods ordered", docs && docs.methods.length === 2 && docs.methods[0].pos === 0 && docs.methods[1].pos === 1);

  // any_tool gates discovery: tool saves are listed, getTools includes them.
  var progs = c.listPrograms();
  var mine = progs.filter(function (p) { return p.name === name; })[0];
  h.check("listPrograms carries anyTool", mine && mine.anyTool === true, JSON.stringify(mine));
  var tools = c.getTools();
  h.check("getTools includes tool", tools.some(function (t) { return t.programName === name; }),
    JSON.stringify(tools.map(function (t) { return t.programName; })));

  // update path: re-save changes source, same id; docs survive a tool re-save.
  var src2 = src + "\n// v2";
  var saved2 = saveProgram({ name: name, source: src2, markdown: md });
  h.check("saveProgram update ok + same id", saved2 && saved2.ok && saved2.object.id === saved.object.id, JSON.stringify(saved2));
  h.check("getProgram reflects update", c.getProgram(name, "v1").source === src2);

  // method removed from docs → its record reconciles away.
  var mdOneMethod = "## Tool Description\n\nA probe program.\n\n## Tool Schema\n\n### main() [getter]\n\nreturns 2.";
  saveProgram({ name: name, source: src2, markdown: mdOneMethod });
  var afterReconcile = c.getProgram(name, "v1");
  h.check("stale method record reconciled", afterReconcile.methods.length === 1 && afterReconcile.methods[0].bareName === "main",
    JSON.stringify(afterReconcile.methods));

  // source-only save: a program with no docs is NOT a tool.
  var plainName = name + "p";
  var savedPlain = saveProgram({ name: plainName, source: src });
  h.check("source-only saveProgram ok", savedPlain && savedPlain.ok, JSON.stringify(savedPlain));
  var plainListed = c.listPrograms().filter(function (p) { return p.name === plainName; })[0];
  h.check("source-only program anyTool false", plainListed && plainListed.anyTool === false, JSON.stringify(plainListed));
  h.check("getTools excludes source-only program", !c.getTools().some(function (t) { return t.programName === plainName; }));

  // source-only RE-save of a tool leaves docs and any_tool untouched.
  saveProgram({ name: name, source: src2 + "\n// v3" });
  var afterSourceOnly = c.getProgram(name, "v1");
  h.check("source-only resave keeps methods", afterSourceOnly.methods.length === 1, JSON.stringify(afterSourceOnly.methods));
  h.check("source-only resave keeps anyTool",
    c.listPrograms().filter(function (p) { return p.name === name; })[0].anyTool === true);

  return h.done();
}
