// __main_source
// applyDiff v1 — search/replace block parser and applier for Sobek.
// Format used by aider, Claude Code, SWE-agent — no line numbers, no +/- prefixes.
// Content-anchored: SEARCH text is found by substring match, not position.
// No async, no Node.js APIs — runs in Sobek JS engine.

// ============================================================================
// NORMALIZE (shared fuzzy comparison)
// ============================================================================

// Normalize a line for fuzzy comparison:
// - trim whitespace
// - strip Anytype markdown escape backslashes before _ * ` |
function normalize(s) {
  return s.trim()
    .replace(/\\_/g, "_")
    .replace(/\\\*/g, "*")
    .replace(/\\`/g, "`")
    .replace(/\\\|/g, "|");
}

// ============================================================================
// SEARCH/REPLACE APPLIER
// ============================================================================

// Apply an array of search/replace blocks to source code.
// Returns { ok, result, error }
export function applySearchReplace(source, blocks) {
  if (!blocks || blocks.length === 0) {
    return { ok: false, error: "No search/replace blocks" };
  }

  var current = source;
  for (var i = 0; i < blocks.length; i++) {
    var block = blocks[i];
    var result = applySingleBlock(current, block, i);
    if (!result.ok) return result;
    current = result.result;
  }

  return { ok: true, result: current };
}

// Apply a single { search, replace } block to source.
function applySingleBlock(source, block, blockIndex) {
  var label = "block " + (blockIndex + 1);

  // Strategy 1: Exact substring match
  var exactResult = exactSubstringMatch(source, block.search);
  if (exactResult.count === 1) {
    return {
      ok: true,
      result: source.substring(0, exactResult.start)
        + block.replace
        + source.substring(exactResult.start + block.search.length)
    };
  }
  if (exactResult.count > 1) {
    return { ok: false, error: label + ": ambiguous match, " + exactResult.count + " occurrences" };
  }

  // Strategy 2: Normalized line-by-line match
  var normResult = normalizedLineMatch(source, block.search);
  if (normResult.count === 1) {
    return {
      ok: true,
      result: source.substring(0, normResult.charStart)
        + block.replace
        + source.substring(normResult.charEnd)
    };
  }
  if (normResult.count > 1) {
    return { ok: false, error: label + ": ambiguous normalized match, " + normResult.count + " occurrences" };
  }

  // Strategy 3: Prefix/suffix anchor match (first 2 + last 2 lines)
  var anchorResult = anchorMatch(source, block.search);
  if (anchorResult.count === 1) {
    return {
      ok: true,
      result: source.substring(0, anchorResult.charStart)
        + block.replace
        + source.substring(anchorResult.charEnd)
    };
  }
  if (anchorResult.count > 1) {
    return { ok: false, error: label + ": ambiguous anchor match, " + anchorResult.count + " occurrences" };
  }

  // All strategies failed
  var preview = block.search.split("\n").slice(0, 3).join("\n");
  return { ok: false, error: label + ": no match found in source. Search text:\n" + preview };
}

// Find exact substring match. Returns { count, start }
function exactSubstringMatch(source, search) {
  var first = source.indexOf(search);
  if (first === -1) return { count: 0, start: -1 };

  var second = source.indexOf(search, first + 1);
  if (second !== -1) {
    var count = 2;
    var pos = source.indexOf(search, second + 1);
    while (pos !== -1) { count++; pos = source.indexOf(search, pos + 1); }
    return { count: count, start: first };
  }

  return { count: 1, start: first };
}

// Normalized line-by-line sliding window match.
// Returns { count, charStart, charEnd }
function normalizedLineMatch(source, search) {
  var sourceLines = source.split("\n");
  var searchLines = search.split("\n");
  if (searchLines.length === 0) return { count: 0, charStart: -1, charEnd: -1 };

  var matches = [];
  var windowSize = searchLines.length;

  for (var i = 0; i <= sourceLines.length - windowSize; i++) {
    var match = true;
    for (var j = 0; j < windowSize; j++) {
      if (normalize(sourceLines[i + j]) !== normalize(searchLines[j])) {
        match = false;
        break;
      }
    }
    if (match) matches.push(i);
  }

  if (matches.length === 0) return { count: 0, charStart: -1, charEnd: -1 };

  var charStart = charOffsetOfLine(sourceLines, matches[0]);
  var charEnd = charOffsetOfLine(sourceLines, matches[0] + windowSize);

  return { count: matches.length, charStart: charStart, charEnd: charEnd };
}

// Anchor match: use first 2 and last 2 lines as anchors.
function anchorMatch(source, search) {
  var sourceLines = source.split("\n");
  var searchLines = search.split("\n");
  if (searchLines.length < 4) return { count: 0, charStart: -1, charEnd: -1 };

  var headSize = 2;
  var tailSize = 2;
  var spanLength = searchLines.length;

  var headLines = searchLines.slice(0, headSize);
  var tailLines = searchLines.slice(searchLines.length - tailSize);

  var matches = [];

  for (var i = 0; i <= sourceLines.length - spanLength; i++) {
    var headOk = true;
    for (var h = 0; h < headSize; h++) {
      if (normalize(sourceLines[i + h]) !== normalize(headLines[h])) {
        headOk = false;
        break;
      }
    }
    if (!headOk) continue;

    var tailStart = i + spanLength - tailSize;
    var tailOk = true;
    for (var t = 0; t < tailSize; t++) {
      if (normalize(sourceLines[tailStart + t]) !== normalize(tailLines[t])) {
        tailOk = false;
        break;
      }
    }
    if (tailOk) matches.push(i);
  }

  if (matches.length === 0) return { count: 0, charStart: -1, charEnd: -1 };

  var charStart = charOffsetOfLine(sourceLines, matches[0]);
  var charEnd = charOffsetOfLine(sourceLines, matches[0] + spanLength);

  return { count: matches.length, charStart: charStart, charEnd: charEnd };
}

// Get character offset of line N in the source.
function charOffsetOfLine(lines, lineIdx) {
  var offset = 0;
  for (var i = 0; i < lineIdx && i < lines.length; i++) {
    offset += lines[i].length + 1;
  }
  return offset;
}

// ============================================================================
// SEARCH/REPLACE BLOCK PARSER
// ============================================================================

// Extract all search/replace blocks from LLM response text.
// Returns [{ search: string, replace: string }, ...]
export function extractSearchReplaceBlocks(text) {
  if (!text) return [];

  var stripped = stripCodeFence(text);

  var blocks = [];
  var pos = 0;

  while (pos < stripped.length) {
    var searchStart = findDelimiter(stripped, pos, "SEARCH");
    if (searchStart === -1) break;

    var contentStart = stripped.indexOf("\n", searchStart);
    if (contentStart === -1) break;
    contentStart += 1;

    var sepStart = findSeparator(stripped, contentStart);
    if (sepStart === -1) break;

    var searchText = stripped.substring(contentStart, sepStart);

    var replaceStart = stripped.indexOf("\n", sepStart);
    if (replaceStart === -1) break;
    replaceStart += 1;

    var replaceEnd = findDelimiter(stripped, replaceStart, "REPLACE");
    if (replaceEnd === -1) break;

    var replaceText = stripped.substring(replaceStart, replaceEnd);

    // Trim trailing newline from both
    if (searchText.length > 0 && searchText[searchText.length - 1] === "\n") {
      searchText = searchText.substring(0, searchText.length - 1);
    }
    if (replaceText.length > 0 && replaceText[replaceText.length - 1] === "\n") {
      replaceText = replaceText.substring(0, replaceText.length - 1);
    }

    blocks.push({ search: searchText, replace: replaceText });

    pos = stripped.indexOf("\n", replaceEnd);
    if (pos === -1) break;
    pos += 1;
  }

  return blocks;
}

// Find <<<<<<< SEARCH or >>>>>>> REPLACE delimiter.
function findDelimiter(text, fromPos, type) {
  var char = type === "SEARCH" ? "<" : ">";
  var searchStr = type.toLowerCase();

  var patterns = [
    char.repeat(7) + " " + type,
    char.repeat(7) + type,
    char.repeat(7) + " " + searchStr,
    char.repeat(4) + " " + type,
  ];

  var best = -1;
  for (var i = 0; i < patterns.length; i++) {
    var idx = text.indexOf(patterns[i], fromPos);
    if (idx !== -1 && (best === -1 || idx < best)) {
      best = idx;
    }
  }

  return best;
}

// Find ======= separator line.
function findSeparator(text, fromPos) {
  var lines = text.substring(fromPos).split("\n");
  var offset = fromPos;
  for (var i = 0; i < lines.length; i++) {
    var trimmed = lines[i].trim();
    if (trimmed.length >= 3 && trimmed.replace(/=/g, "").length === 0) {
      return offset;
    }
    offset += lines[i].length + 1;
  }
  return -1;
}

// Strip outermost markdown code fence if present.
function stripCodeFence(text) {
  var trimmed = text.trim();
  if (trimmed.indexOf("```") === 0) {
    var firstNewline = trimmed.indexOf("\n");
    if (firstNewline === -1) return trimmed;
    var inner = trimmed.substring(firstNewline + 1);
    var lastFence = inner.lastIndexOf("```");
    if (lastFence !== -1) {
      inner = inner.substring(0, lastFence);
    }
    return inner;
  }
  return text;
}

// ============================================================================
// MAIN
// ============================================================================

export function main(args) {
  if (!args || !args.source || !args.blocks) {
    return "Usage: applyDiff source=<string> blocks=<json>";
  }
  var blocks = typeof args.blocks === "string" ? JSON.parse(args.blocks) : args.blocks;
  return JSON.stringify(applySearchReplace(args.source, blocks));
}
