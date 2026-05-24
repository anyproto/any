// __main_source
// Tracer v1 — lightweight debugging subsystem for failed code execution.
// Replays execution with mocks and uses LLM (codegen tier) to diagnose/fix errors.
// Called from assistant's executePlanStep after js.eval failure, before consuming a retry slot.
import { createLLM } from "llm@v1";
import { applySearchReplace, extractSearchReplaceBlocks } from "applyDiff@v1";
var llm = createLLM();

// ============================================================================
// ERROR DETECTION
// ============================================================================

// Detect whether an evalResult represents a failure.
// Returns { hasError, errorType, message } where errorType is one of:
//
//   "runtime"     — JS exception (TypeError, ReferenceError, etc.) from evalResult.error.
//                   Tracer engages: Haiku attempts fix via mocked replay.
//
//   "child_error" — A child js.eval (e.g. runProgram) threw an error. Found in traces["js.eval"].
//                   NOT retryable: the bug is in external/user code, not in the LLM-generated wrapper.
//                   executePlanStep returns immediately with the error for honest reporting.
//
//   "api_error"   — An anyHelper method returned ok:false (mutation methods), or logged an
//                   error via console.log (query methods like getObject). Found in
//                   traces["anyHelper.*"] and traces["console.log"].
//                   Tracer engages: the LLM-generated code called the API wrong (wrong type key,
//                   missing property, bad ID, etc.) and Haiku can attempt to fix it.
//
//   "empty"       — Result is null/undefined/empty string. Falls through to validateStepResult.
//
//   "none"        — No error detected. Success path.
//
export function detectError(evalResult) {
  // Category 1: Runtime exception (TypeError, ReferenceError, etc.)
  if (evalResult.error) {
    return { hasError: true, errorType: "runtime", message: evalResult.error };
  }

  // Category 2: Child program errors — js.eval entries in traces that have errors.
  // This catches cases like runProgram executing a faulty external program.
  // The wrapper code ran fine but the inner program failed.
  if (evalResult.traces && evalResult.traces["js.eval"]) {
    var jsEvalRecord = evalResult.traces["js.eval"];
    for (var jsSource in jsEvalRecord) {
      var jsOutputs = jsEvalRecord[jsSource];
      for (var ji = 0; ji < jsOutputs.length; ji++) {
        var jsOut = jsOutputs[ji];
        // js.eval trace outputs can be strings or objects
        var parsed = typeof jsOut === "string" ? null : jsOut;
        if (!parsed && typeof jsOut === "string") {
          try { parsed = JSON.parse(jsOut); } catch (e) {}
        }
        if (parsed && parsed.error) {
          return { hasError: true, errorType: "child_error", message: "Child program error: " + parsed.error };
        }
      }
    }
  }

  // Category 3: anyHelper method errors — scan traces for ok:false returns.
  // Trace outputs are strings containing JSON like '{"ok":false,"error":"human readable"}'.
  // We parse them to extract the error string directly for cleaner messages.
  if (evalResult.traces) {
    for (var traceKey in evalResult.traces) {
      if (traceKey.indexOf("anyHelper.") !== 0) continue;
      var methodRecord = evalResult.traces[traceKey];
      var methodName = traceKey.substring("anyHelper.".length);
      for (var methodInput in methodRecord) {
        var methodOutputs = methodRecord[methodInput];
        for (var mi = 0; mi < methodOutputs.length; mi++) {
          var mRaw = methodOutputs[mi];
          var mOut = typeof mRaw === "string" ? mRaw : JSON.stringify(mRaw);

          // Try to parse as JSON to extract structured error info
          var mParsed = typeof mRaw === "object" && mRaw !== null ? mRaw : null;
          if (!mParsed && typeof mRaw === "string") {
            try { mParsed = JSON.parse(mRaw); } catch (e) {}
          }

          // Check parsed object for ok:false (mutation methods)
          if (mParsed && mParsed.ok === false) {
            // Extract the error string directly — much cleaner than dumping raw JSON
            var errMsg = typeof mParsed.error === "string"
              ? mParsed.error
              : (mParsed.error ? JSON.stringify(mParsed.error) : mOut.substring(0, 300));
            return { hasError: true, errorType: "api_error", message: methodName + " failed: " + errMsg };
          }

          // Fallback: string search for ok:false (catches edge cases, e.g. nested structures)
          if (!mParsed && (mOut.indexOf('"ok":false') !== -1 || mOut.indexOf('"ok": false') !== -1)) {
            return { hasError: true, errorType: "api_error", message: methodName + " failed: " + mOut.substring(0, 300) };
          }
        }
      }
    }
  }

  // Category 3.5: console.log error messages from anyHelper methods.
  // Query methods (getObject, getObjects) log errors via console.log when they
  // can't return structured error info (e.g. getObject returns null).
  // Pattern: "methodName(args) error: message"
  if (evalResult.traces && evalResult.traces["console.log"]) {
    var consoleRecord = evalResult.traces["console.log"];
    for (var consoleInput in consoleRecord) {
      var consoleOutputs = consoleRecord[consoleInput];
      for (var ci = 0; ci < consoleOutputs.length; ci++) {
        var logMsg = typeof consoleOutputs[ci] === "string" ? consoleOutputs[ci] : String(consoleOutputs[ci]);
        // Match pattern: "getObject(id) error: message" or "getObjects(...) error: message"
        var errorMatch = logMsg.match(/^(\w+)\(.*?\)\s+error:\s+(.+)$/);
        if (errorMatch) {
          return { hasError: true, errorType: "api_error", message: errorMatch[1] + " failed: " + errorMatch[2] };
        }
      }
    }
  }

  // Category 4: Raw fetch errors — fallback when anyHelper.* traces aren't available.
  // Scan fetch trace for 400/500 status codes indicating API failures.
  if (evalResult.traces && evalResult.traces["fetch"]) {
    var hasWrapped = false;
    for (var wk in evalResult.traces) {
      if (wk.indexOf("anyHelper.") === 0) { hasWrapped = true; break; }
    }
    // Only check raw fetch if no wrapped method traces exist (avoid double-reporting)
    if (!hasWrapped) {
      var fetchRecord = evalResult.traces["fetch"];
      var errorFetches = [];
      for (var fInput in fetchRecord) {
        var fOutputs = fetchRecord[fInput];
        for (var fi = 0; fi < fOutputs.length; fi++) {
          var fOut = typeof fOutputs[fi] === "string" ? fOutputs[fi] : JSON.stringify(fOutputs[fi]);
          // Check for 4xx/5xx status
          var statusMatch = fOut.match(/"status"\s*:\s*(\d+)/);
          if (statusMatch) {
            var status = parseInt(statusMatch[1], 10);
            if (status >= 400) {
              // Extract path from input key
              var fPath = fInput;
              try {
                var fParsed = JSON.parse(fInput);
                if (fParsed && fParsed[0]) {
                  var fPathMatch = fParsed[0].match(/\/v1\/.*/);
                  fPath = fPathMatch ? fPathMatch[0] : fParsed[0];
                }
              } catch (e) {}
              errorFetches.push(status + " " + fPath.substring(0, 80));
            }
          }
        }
      }
      if (errorFetches.length > 0) {
        return { hasError: true, errorType: "api_error", message: "API errors: " + errorFetches.join("; ") };
      }
    }
  }

  // Category 5: Empty/null result
  if (evalResult.result === null || evalResult.result === undefined) {
    return { hasError: true, errorType: "empty", message: "Result is null/undefined" };
  }
  var s = typeof evalResult.result === "string" ? evalResult.result : JSON.stringify(evalResult.result);
  if (s.trim().length === 0) {
    return { hasError: true, errorType: "empty", message: "Result is empty string" };
  }

  return { hasError: false, errorType: "none", message: "" };
}

// ============================================================================
// TRACE FORMATTING
// ============================================================================

// Format a traceDiff object for LLM consumption.
// Format a traceDiff for LLM consumption.
// If anyHelper.* entries are present in the trace, skip raw fetch entries —
// the method-level trace is more meaningful and much smaller.
// Signal: presence of any key starting with "anyHelper." means methods are wrapped.
export function formatTraceDiff(traceDiff) {
  if (!traceDiff) return "";
  var lines = [];

  // Detect if anyHelper methods are wrapped (trace has anyHelper.* keys)
  var hasWrappedMethods = false;
  for (var key in traceDiff) {
    if (key.indexOf("anyHelper.") === 0) { hasWrappedMethods = true; break; }
  }

  // console.log entries — primary source of exploration data
  if (traceDiff["console.log"]) {
    var logRecord = traceDiff["console.log"];
    for (var logInput in logRecord) {
      var logOutputs = logRecord[logInput];
      for (var i = 0; i < logOutputs.length; i++) {
        var msg = typeof logOutputs[i] === "string" ? logOutputs[i] : JSON.stringify(logOutputs[i]);
        if (msg.length > 200) msg = msg.substring(0, 200) + "...";
        lines.push("console.log: " + msg);
      }
    }
  }

  // anyHelper.* entries — method-level trace (input args → output summary)
  for (var hKey in traceDiff) {
    if (hKey.indexOf("anyHelper.") !== 0) continue;
    var methodName = hKey.substring("anyHelper.".length);
    var hRecord = traceDiff[hKey];
    for (var hInput in hRecord) {
      var hOutputs = hRecord[hInput];
      for (var hi = 0; hi < hOutputs.length; hi++) {
        var hOut = typeof hOutputs[hi] === "string" ? hOutputs[hi] : JSON.stringify(hOutputs[hi]);
        // Use wider truncation for error outputs so error messages aren't clipped
        var hOutLimit = (hOut.indexOf('"ok":false') !== -1 || hOut.indexOf('"error"') !== -1) ? 300 : 150;
        if (hOut.length > hOutLimit) hOut = hOut.substring(0, hOutLimit) + "...";
        // Compact input args
        var hArgs = hInput;
        if (hArgs.length > 80) hArgs = hArgs.substring(0, 80) + "...";
        lines.push(methodName + "(" + hArgs + ") → " + hOut);
      }
    }
  }

  // Raw fetch entries — only if anyHelper methods are NOT wrapped.
  // When methods are wrapped, fetch is redundant (lower-level detail).
  if (!hasWrappedMethods && traceDiff["fetch"]) {
    var fetchRecord = traceDiff["fetch"];
    for (var input in fetchRecord) {
      var method = "GET";
      var path = input;
      try {
        var parsed = JSON.parse(input);
        if (parsed && parsed.length > 0) {
          var url = parsed[0];
          var pathMatch = url.match(/\/v1\/.*/);
          path = pathMatch ? pathMatch[0] : url;
          if (parsed[1] && parsed[1].method) method = parsed[1].method;
        }
      } catch (e) {
        path = input.substring(0, 80);
      }
      var outputs = fetchRecord[input];
      for (var i = 0; i < outputs.length; i++) {
        var status = "?";
        try {
          var outputStr = typeof outputs[i] === "string" ? outputs[i] : JSON.stringify(outputs[i]);
          var resp = JSON.parse(outputStr);
          if (resp.status !== undefined) status = String(resp.status);
        } catch (e) {}
        lines.push("fetch: " + method + " " + path + " → " + status);
      }
    }
  }

  return lines.join("\n");
}

// ============================================================================
// TRACE SUMMARY (for adjust prompt context)
// ============================================================================

// Format method-level trace entries (anyHelper.*) for the adjust prompt.
// Shows full data shapes so Haiku can understand the actual structure of values.
// Truncates individual entries at a per-entry limit to keep total size reasonable.
var METHOD_TRACE_ENTRY_LIMIT = 500;

function formatMethodTrace(traces) {
  if (!traces) return "";
  var lines = [];
  for (var key in traces) {
    if (key.indexOf("anyHelper.") !== 0) continue;
    var methodName = key.substring("anyHelper.".length);
    var record = traces[key];
    for (var input in record) {
      var outputs = record[input];
      for (var i = 0; i < outputs.length; i++) {
        var out = typeof outputs[i] === "string" ? outputs[i] : JSON.stringify(outputs[i]);
        if (out.length > METHOD_TRACE_ENTRY_LIMIT) out = out.substring(0, METHOD_TRACE_ENTRY_LIMIT) + "...";
        var args = input;
        if (args.length > 100) args = args.substring(0, 100) + "...";
        lines.push(methodName + "(" + args + ") → " + out);
      }
    }
  }
  return lines.join("\n");
}

// ============================================================================
// CODE EXTRACTION
// ============================================================================

// Extract JS code from a markdown code block in LLM response (fallback for full rewrites)
function extractCodeBlock(text) {
  var start = text.indexOf("```javascript");
  if (start === -1) start = text.indexOf("```js");
  if (start === -1) start = text.indexOf("```");
  if (start === -1) return text;

  var codeStart = text.indexOf("\n", start);
  if (codeStart === -1) return text;
  codeStart += 1;

  var codeEnd = text.indexOf("```", codeStart);
  if (codeEnd === -1) return text.substring(codeStart);

  return text.substring(codeStart, codeEnd);
}

// Try to apply search/replace blocks from LLM response to current code.
// Returns:
//   { ok: true, code: string }  — edit applied successfully
//   { ok: false, found: true }  — blocks found but couldn't apply (don't fall back to code block)
//   { ok: false, found: false } — no blocks found (safe to try code block fallback)
function tryApplyEdit(llmResponse, currentCode) {
  var blocks = extractSearchReplaceBlocks(llmResponse);
  if (blocks.length === 0) return { ok: false, found: false };

  var result = applySearchReplace(currentCode, blocks);
  if (result.ok) return { ok: true, code: result.result };

  console.log("applySearchReplace failed: " + result.error);
  return { ok: false, found: true };
}

// ============================================================================
// LLM PROMPT BUILDERS
// ============================================================================

function buildAdjustPrompt(opts, iteration, rollingContext, currentCode) {
  var prompt = "You are debugging a failing JavaScript program for the Anytype API.\n"
    + "The program runs in a synchronous JS engine (Sobek, not Node.js).\n"
    + "No async/await. fetch() is synchronous and auto-parses JSON (use resp.body, never JSON.parse).\n\n";

  prompt += "## Task\n" + opts.stepTitle + "\n\n";

  if (opts.toolDocs) {
    prompt += "## Tool Reference\n" + opts.toolDocs + "\n\n";
  }

  var codeToShow = currentCode || opts.code;
  prompt += "## Failing Code\n```javascript\n" + codeToShow + "\n```\n\n";
  prompt += "## Error\n" + opts.error + "\n\n";

  // If anyHelper methods are traced, show full data shapes so Haiku
  // understands the actual structure of values (e.g. obj.type is an object with .name).
  var methodSummary = formatMethodTrace(opts.traces);
  if (methodSummary) {
    prompt += "## API calls before error\n" + methodSummary + "\n\n";
  }

  // Quality guidelines for generated code.
  // NOTE: keep analyzing generated code quality and adjust these guidelines as patterns emerge.
  // Known issues so far:
  //   - Haiku adds unnecessary defensive checks (null guards, Array.isArray) for simple fixes
  //   - Haiku may guess property names (e.g. tag→tags) without evidence
  //   - console.log inside loops produces massive output for large collections
  var codeGuidelines = "\n\n## Code Guidelines\n"
    + "- Make minimal changes to fix the error. Do not add unnecessary null checks or defensive code.\n"
    + "- Do NOT place console.log inside loops that iterate over object collections. "
    + "Instead, log a single summary before/after the loop, or log only the first item.\n"
    + "- Keep console.log output short and precise: log typeof and key field names, not full objects. "
    + "For arrays, log .length instead of the full array. "
    + "Example: console.log('type of obj.type:', typeof obj.type, 'keys:', Object.keys(obj.type)) "
    + "instead of console.log('obj:', obj). "
    + "Example: console.log('objects count:', objects.length) instead of console.log('objects:', objects).\n"
    + "- Do not rename properties unless the error message indicates a wrong name. "
    + "Keep the original property keys from the failing code.\n";

  var outputFormat = "\nOutput ONLY search/replace blocks to fix the code. "
    + "Each block replaces one section. Include enough context lines in SEARCH to match uniquely.\n\n"
    + "<<<<<<< SEARCH\nexact lines from the current code\n=======\nreplacement lines\n>>>>>>> REPLACE\n\n"
    + "You may output multiple blocks for multiple changes. Do NOT output the full program.\n";

  if (iteration === 0) {
    // Iteration 0: fix + explore
    prompt += "## Instructions\n"
      + "Fix the code based on the error message above.\n"
      + "Also add console.log() calls around suspicious values to explore what's happening.\n"
      + codeGuidelines
      + outputFormat;
  } else {
    // Iteration 1+: adjust based on exploration results
    prompt += "## Previous Exploration Results\n";
    for (var i = 0; i < rollingContext.length; i++) {
      var ctx = rollingContext[i];
      prompt += "### Attempt " + (i + 1) + "\n";
      if (ctx.traceDiff) {
        prompt += "Console output and effects:\n" + ctx.traceDiff + "\n";
      }
      if (ctx.verdict) {
        prompt += "Analysis: " + ctx.verdict + "\n";
      }
      prompt += "\n";
    }
    prompt += "## Instructions\n"
      + "Based on the exploration results and analysis above, adjust the code to fix the error.\n"
      + "You may add more console.log() calls if you need to explore further.\n"
      + codeGuidelines
      + outputFormat;
  }

  return prompt;
}


// ============================================================================
// MAIN TRACE FUNCTION
// ============================================================================

// Primary entry point. Runs up to maxIterations of mocked execution to diagnose/fix errors.
// Returns: { fixed, code, result, diagnosis, iterations, log }
export function trace(opts) {
  var maxIter = opts.maxIterations || 2;
  var log = [];
  var rollingContext = [];
  // Track evolving code across iterations — patches apply to latest version
  var currentCode = opts.code;

  for (var i = 0; i < maxIter; i++) {
    // Phase 1: ADJUST — LLM generates diff patch (or falls back to full code)
    var adjustPrompt = buildAdjustPrompt(opts, i, rollingContext, currentCode);
    var adjustResponse;
    try {
      adjustResponse = llm.codegen(adjustPrompt);
    } catch (e) {
      log.push({ iteration: i, code: "", traceDiff: null, verdict: { fixed: false, explanation: "LLM error: " + (e.message || e) } });
      continue;
    }

    if (!adjustResponse) {
      log.push({ iteration: i, code: "", traceDiff: null, verdict: { fixed: false, explanation: "Empty LLM response" } });
      continue;
    }

    // Try search/replace or diff first, fall back to full code block
    var adjustedCode;
    var editResult = tryApplyEdit(adjustResponse, currentCode);
    if (editResult.ok) {
      adjustedCode = editResult.code;
    } else if (!editResult.found) {
      // No edit format in response — LLM output full code
      adjustedCode = extractCodeBlock(adjustResponse);
    } else {
      // Edit found but couldn't apply — don't extract raw blocks as code
      log.push({ iteration: i, code: "", traceDiff: null, verdict: { fixed: false, explanation: "Edit blocks found but could not apply to source" } });
      continue;
    }

    if (!adjustedCode || adjustedCode.trim().length === 0) {
      log.push({ iteration: i, code: "", traceDiff: null, verdict: { fixed: false, explanation: "No code or diff in LLM response" } });
      continue;
    }

    // Update current code for next iteration
    currentCode = adjustedCode;

    // Phase 2: EXECUTE — run with mocked trace (no real API calls)
    var mockResult = js.eval(adjustedCode, opts.args, { mocks: opts.traces });

    // Phase 3: JUDGE — deterministic check instead of LLM.
    // V1: no runtime error from mocked execution = fixed.
    // V2: also check that api_error patterns (ok:false) disappeared from the trace,
    //     and that the result isn't just a control signal (__ask) echoing the failure.
    var diffStr = formatTraceDiff(mockResult.traceDiff);
    var verdict;
    if (!mockResult.error) {
      // Check result is not empty/null
      var resultStr = mockResult.result === null || mockResult.result === undefined
        ? ""
        : typeof mockResult.result === "string" ? mockResult.result : JSON.stringify(mockResult.result);
      if (resultStr.trim().length === 0) {
        verdict = { fixed: false, explanation: "No error but result is empty" };
      } else {
        // V2: re-run detectError on the mocked result to catch persisting api_errors
        var mockDetected = detectError(mockResult);
        if (mockDetected.hasError && mockDetected.errorType === "api_error") {
          verdict = { fixed: false, explanation: "API error still present: " + mockDetected.message };
        } else {
          verdict = { fixed: true, explanation: "Mocked execution succeeded with result: " + resultStr.substring(0, 150) };
        }
      }
    } else {
      verdict = { fixed: false, explanation: "Still errors: " + mockResult.error };
    }

    log.push({
      iteration: i,
      code: adjustedCode,
      traceDiff: mockResult.traceDiff,
      verdict: verdict
    });

    // Update rolling context (keep last 2 for next iteration's prompt)
    rollingContext.push({
      traceDiff: diffStr,
      verdict: verdict.explanation
    });
    if (rollingContext.length > 2) {
      rollingContext.shift();
    }

    if (verdict.fixed) {
      return {
        fixed: true,
        code: adjustedCode,
        result: mockResult.result,
        diagnosis: verdict.explanation,
        iterations: i + 1,
        log: log
      };
    }
  }

  // All iterations exhausted — synthesize diagnosis from accumulated verdicts
  var diagParts = [];
  for (var i = 0; i < log.length; i++) {
    if (log[i].verdict && log[i].verdict.explanation) {
      diagParts.push("Attempt " + (i + 1) + ": " + log[i].verdict.explanation);
    }
  }
  var diagnosis = diagParts.length > 0
    ? diagParts.join(". ")
    : "Tracer could not diagnose the issue after " + log.length + " iterations.";

  return {
    fixed: false,
    code: log.length > 0 ? log[log.length - 1].code : opts.code,
    result: null,
    diagnosis: diagnosis,
    iterations: log.length,
    log: log
  };
}
