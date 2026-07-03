// Tiered tool injection (toolcall_core@v1): pinned core modules keep their
// full description in the system prompt; every other module is summarized
// (first paragraph, capped) with the full doc one `describe()` call away —
// so the prompt stops growing linearly with the tool set. Pure unit test.

import { _toolSummary, _buildToolsPromptSection } from "toolcall_core@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}
var h = mkHarness();

// --- _toolSummary -------------------------------------------------------------
h.check("first paragraph only",
  _toolSummary("Short intro line.\n\nSecond paragraph with detail.") === "Short intro line.");
var long = new Array(30).join("word word word word "); // > 400 chars, one paragraph
var s = _toolSummary(long);
h.check("single-paragraph doc capped", s.length <= 401 && s.slice(-1) === "…", "len=" + s.length);
h.check("empty/absent safe", _toolSummary("") === "" && _toolSummary(null) === "");

// --- _buildToolsPromptSection ---------------------------------------------------
function tool(desc) {
  return { toolId: "x", programVersion: "v1", createdDate: "", description: desc,
           methods: [{ bareName: "go", signature: "go(a, b)", content: "### go\n\ndoc" }] };
}
var FULL_PINNED = "anyHelper full documentation body.\n\nSecond paragraph that must stay inline for the pinned module.";
var FULL_OTHER = "gmail summary line: send and search mail.\n\nLong auth details paragraph that should NOT be in the prompt verbatim because it belongs to tier 2.";
var out = _buildToolsPromptSection({
  anyHelper: tool(FULL_PINNED),
  gmail: tool(FULL_OTHER)
});

h.check("pinned module keeps full description",
  out.indexOf("Second paragraph that must stay inline") !== -1);
h.check("non-pinned module is summarized",
  out.indexOf("gmail summary line: send and search mail.") !== -1 &&
  out.indexOf("Long auth details paragraph") === -1);
h.check("summary carries describe() pointer",
  out.indexOf("gmail.describe()") !== -1);
h.check("method signatures still listed", out.indexOf("Methods: go(a, b)") !== -1);

// A short single-paragraph description needs no pointer — nothing was elided.
var out2 = _buildToolsPromptSection({
  anyHelper: tool(FULL_PINNED),
  ui: tool("Open a space or object in the connected UI window.")
});
h.check("untrimmed summary has no describe() pointer", out2.indexOf("ui.describe()") === -1);

h.done();
