// anyPrograms: create/update/edit + doc-section editing over the program
// datasets.
//
// Harness caveat: createProgram/updateProgram run a live import probe
// (_verifyImportable) via `import ... from "name@v1"`. That resolves through
// the runtime's module loader — but the STOCK anytype-agent-runtime CLI used
// here ships the anytype-heart loader (GET /v1/spaces/:id/objects), NOT an
// `any`-speaking one. bobrik-watch wires its own newAnySDKLoader in production,
// so the probe works there. Here we assert the program SAVED (object id
// present) and treat a probe-resolution failure as the known harness gap.
// Run via jsrunner_test.go.

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

  // create — saved either way; probe (methods) only resolves under bobrik's
  // own loader, so accept ok OR saved-with-object here.
  var c = createProgram({ name: name, source: src, markdown: md });
  h.check("createProgram saved", c && (c.ok || (c.saved && c.object && c.object.id)), JSON.stringify(c));
  if (c.ok) {
    h.check("createProgram import probe lists exports",
      c.methods && c.methods.indexOf("main") !== -1 && c.methods.indexOf("helper") !== -1,
      JSON.stringify(c.methods));
  }

  // missing main export rejected
  var noMain = createProgram({ name: name + "n", source: "export function helper(){}", markdown: md });
  h.check("createProgram rejects missing main", noMain && noMain.ok === false, JSON.stringify(noMain));

  // missing markdown rejected
  var noMd = createProgram({ name: name + "m", source: src });
  h.check("createProgram requires markdown", noMd && noMd.ok === false, JSON.stringify(noMd));

  // getProgram round-trip
  var got = getProgram(name);
  h.check("getProgram source exact", got && got.source === src, JSON.stringify(got && got.source));

  // editProgram: str-replace in source, re-imports
  var ed = editProgram(name, { oldString: "return 42;", newString: "return 43;" });
  h.check("editProgram ok", ed && ed.ok, JSON.stringify(ed));
  h.check("editProgram applied", getProgram(name).source.indexOf("return 43;") !== -1);

  // upsertDescription replaces the Tool Description section
  var ud = upsertDescription(name, "An updated probe tool.");
  h.check("upsertDescription ok", ud && ud.ok, JSON.stringify(ud));
  h.check("getProgramDescription reflects update",
    (getProgramDescription(name) || "").indexOf("updated probe tool") !== -1,
    JSON.stringify(getProgramDescription(name)));

  // upsertMethodDescription adds/updates a method section
  var um = upsertMethodDescription(name, "helper", "### helper()\nreturns 1.");
  h.check("upsertMethodDescription ok", um && um.ok, JSON.stringify(um));
  var mdoc = getMethodDescription(name, "helper");
  h.check("getMethodDescription reads section", mdoc && mdoc.indexOf("helper") !== -1, JSON.stringify(mdoc));

  // updateProgram changes source via the dedicated path (probe caveat as above)
  var up = updateProgram({ name: name, source: src + "\n// touched" });
  h.check("updateProgram saved", up && (up.ok || (up.saved && up.object)), JSON.stringify(up));
  h.check("updateProgram applied source", getProgram(name).source.indexOf("// touched") !== -1);

  // listPrograms includes it
  h.check("listPrograms includes it", listPrograms().some(function (p) { return p.name === name; }));

  return h.done();
}
