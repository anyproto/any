// Engine probe — run with the runtime directly, no API needed:
//
//   anytype-agent-runtime cmd/bobrik-watch/tests/engine_string_semantics.js
//
// Demonstrates WHY a regex authored as a string literal arrives "mangled" in a
// stored program, and proves it is ordinary ECMAScript semantics — not a bug in
// our engine or pipeline. A program's source is built as a JS string (usually a
// backtick template literal) and that string is what gets stored. Inside any JS
// string literal, an unrecognized escape like `\s` drops the backslash:
// `"\s" === "s"`. So `[\s\S]` written inside `` `...` `` becomes `[sS]`.
//
// A regex *literal* (`/[\s\S]/`) is parsed by the regex grammar, not the string
// grammar, so its `.source` keeps the backslashes. That asymmetry is the whole
// trap: code that works when pasted into run_cell (regex literal) breaks once
// the same text is wrapped in a template literal and handed to createProgram.
//
// Fix for authors: double-escape inside the string (`[\\s\\S]`), or build the
// RegExp from a string with doubled backslashes. There is nothing to "remove"
// server-side — see program_roundtrip_test.go for the storage-fidelity half.
export function main(args) {
  var tl = `[\s\S]*?<\/item>`;   // template literal: \s -> s, \S -> S, \/ -> /
  var dq = "[\s\S]*?<\/item>";   // double-quoted: identical collapse
  var esc = `[\\s\\S]*?<\/item>`; // doubled backslashes: survives as [\s\S]
  var lit = /[\s\S]*?<\/item>/;  // regex literal: backslashes preserved

  return {
    fromTemplateLiteral: tl,            // "[sS]*?</item>"  <- the trap
    fromDoubleQuoted: dq,               // "[sS]*?</item>"
    fromDoubledEscape: esc,             // "[\s\S]*?</item>" <- the fix
    fromRegexLiteralSource: lit.source, // "[\s\S]*?<\/item>"
    // proof the collapse is semantic, not cosmetic: build a RegExp from each
    // string and run the techcrunch-style "capture item body" match.
    sample: "<item>\n\t<title>x</title>\n</item>",
    badCaptures: captureBody(tl),       // "" — [sS] can't span the newline body
    goodCaptures: captureBody(esc),     // full body — [\s\S] matches any char
  };
}

function captureBody(itemInner) {
  var re = new RegExp("<item>(" + itemInner.replace("</item>", "") + ")", "");
  var m = re.exec("<item>\n\t<title>x</title>\n</item>");
  return m ? m[1] : "(no match)";
}
