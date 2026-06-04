// anyPrograms: create/update/edit + doc-section editing over the program
// datasets.
//
// Runs under cmd/any-agent-runtime (built by jsrunner_test.go), which wires
// the SAME anySDK module loader bobrik-watch uses in production — so
// createProgram/updateProgram's live import probe (_verifyImportable)
// resolves the just-saved program from the space and we can assert it
// strictly. Run via jsrunner_test.go.

import {
  createProgram, updateProgram, editProgram, getProgram,
  upsertDescription, upsertMethodDescription,
  getProgramDescription, getMethodDescription, listPrograms
} from "anyPrograms@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}

export function main(args) {
  var h = mkHarness();
  var name = "probeTool" + ("" + new Date().getTime()).slice(-7);
  var src = "// __main_source\nexport function main(args){ return 42; }\nexport function helper(){ return 1; }";
  var md = "## Tool Description\nA probe tool.\n\n## Tool Schema\n### main()\nreturns 42.";

  // create — the import probe resolves from the space via the anySDK
  // loader, so a clean ok WITH the export list is required.
  var c = createProgram({ name: name, source: src, markdown: md });
  h.check("createProgram ok", c && c.ok, JSON.stringify(c));
  h.check("createProgram import probe lists exports",
    c && c.methods && c.methods.indexOf("main") !== -1 && c.methods.indexOf("helper") !== -1,
    JSON.stringify(c && c.methods));

  // missing main export rejected
  var noMain = createProgram({ name: name + "n", source: "export function helper(){}", markdown: md });
  h.check("createProgram rejects missing main", noMain && noMain.ok === false, JSON.stringify(noMain));

  // missing markdown rejected
  var noMd = createProgram({ name: name + "m", source: src });
  h.check("createProgram requires markdown", noMd && noMd.ok === false, JSON.stringify(noMd));

  // getProgram round-trip
  var got = getProgram(name);
  h.check("getProgram source exact", got && got.source === src, JSON.stringify(got && got.source));

  // editProgram: str-replace in source, re-imports. Source-only — the
  // method docs must SURVIVE the edit (the old markdown round-trip through
  // a tool save would have wiped program_methods).
  var methodsBefore = (getProgram(name).methods || []).length;
  var ed = editProgram(name, { oldString: "return 42;", newString: "return 43;" });
  h.check("editProgram ok", ed && ed.ok, JSON.stringify(ed));
  h.check("editProgram applied", getProgram(name).source.indexOf("return 43;") !== -1);
  h.check("editProgram preserves method docs",
    (getProgram(name).methods || []).length === methodsBefore,
    JSON.stringify(getProgram(name).methods));

  // upsertDescription replaces the description body (program_description)
  var ud = upsertDescription(name, "An updated probe tool.");
  h.check("upsertDescription ok", ud && ud.ok, JSON.stringify(ud));
  h.check("getProgramDescription reflects update",
    (getProgramDescription(name) || "").indexOf("updated probe tool") !== -1,
    JSON.stringify(getProgramDescription(name)));

  // upsertMethodDescription adds/updates one program_methods record.
  // Body only — no heading line; the heading is reconstructed on read.
  var um = upsertMethodDescription(name, "helper", "returns 1.");
  h.check("upsertMethodDescription ok", um && um.ok, JSON.stringify(um));
  var mdoc = getMethodDescription(name, "helper");
  h.check("getMethodDescription reads section",
    mdoc && mdoc.indexOf("### helper()") === 0 && mdoc.indexOf("returns 1.") !== -1,
    JSON.stringify(mdoc));
  // updating an existing method keeps its heading/kind, replaces the body
  var um2 = upsertMethodDescription(name, "helper", "returns one.");
  h.check("upsertMethodDescription update ok", um2 && um2.ok && um2.created === false, JSON.stringify(um2));
  h.check("method body replaced", (getMethodDescription(name, "helper") || "").indexOf("returns one.") !== -1);

  // updateProgram changes source via the dedicated path (probe asserts too)
  var up = updateProgram({ name: name, source: src + "\n// touched" });
  h.check("updateProgram ok", up && up.ok, JSON.stringify(up));
  h.check("updateProgram applied source", getProgram(name).source.indexOf("// touched") !== -1);

  // listPrograms includes it
  h.check("listPrograms includes it", listPrograms().some(function (p) { return p.name === name; }));

  return h.done();
}
