// JS-level integration test for anyHelper, run against a live `any` server.
//
// Contract with the Go runner (anyhelper_js_test.go):
//   - export main(args) where args has { apiBaseUrl, spaceId }
//   - run assertions through the inline harness below
//   - the LAST thing main does is console.log("HARNESS_RESULT " + JSON.stringify(summary))
//   - summary shape: { pass: <int>, fail: <int>, failures: [<string>...] }
// The runner greps stdout for the HARNESS_RESULT line and fails the Go test
// if fail > 0 (or the line is missing). Every test file follows this shape.
//
// Run standalone:
//   anytype-agent-runtime -e <env> -m <bobrik-dir> \
//     cmd/bobrik-watch/tests/js/anyhelper_smoke_test.js \
//     apiBaseUrl=http://127.0.0.1:7003 spaceId=<spaceId>

import { createClient } from "anyHelper@v1";

// mkHarness — minimal per-file assertion kit. Kept inline (not a shared
// import) because the runtime resolves only one -m directory, which must
// hold anyHelper.js; a shared testkit module can't live there too.
function mkHarness() {
  var summary = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (name, cond, detail) {
      if (cond) { summary.pass++; return; }
      summary.fail++;
      summary.failures.push(detail ? name + " — " + detail : name);
    },
    done: function () {
      console.log("HARNESS_RESULT " + JSON.stringify(summary));
      return summary;
    }
  };
}

export function main(args) {
  var h = mkHarness();
  var c = createClient({ apiBaseUrl: args.apiBaseUrl, spaceId: args.spaceId, noTrace: true });

  // Boot sanity: the client talks to the server and sees the builtin catalog.
  var types = c.getTypes();
  h.check("getTypes returns array", Array.isArray(types));
  h.check("getTypes includes the `any` builtin",
    types.some(function (t) { return t && t.id === "any"; }),
    "got " + JSON.stringify((types || []).map(function (t) { return t && t.id; })));

  return h.done();
}
