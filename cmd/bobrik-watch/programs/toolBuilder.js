// __main_source
// toolBuilder v1 — algorithmically controlled pipeline for creating tools.
// Runs: WRITE → TEST → TRACE → SCHEMA → REGISTER → DISCOVER → INVOKE
// Each step is hardcoded, not LLM-planned. Called when classify detects
// "build a new tool" intent (external API integration signal).

import { createLLM } from "llm@v1";
import { trace as runTracer, detectError } from "tracer@v1";
import { extractToolMethods } from "assistant@v4";

var llm = createLLM();

// Build a new tool from a user request.
// opts.codeGenBase: the assistant's assembled codegen prompt (skills + tool docs + method index).
//   If provided, used as context for WRITE and SCHEMA steps instead of a standalone prompt.
// Returns: { ok, toolName, steps[], error? }
//   steps[]: [{ name, ok, detail }] — log of what happened at each stage
export function buildTool(client, request, args, opts) {
  if (!opts) opts = {};
  var log = [];
  function step(name, ok, detail) {
    log.push({ name: name, ok: ok, detail: detail });
    console.log("[toolBuilder] " + name + ": " + (ok ? "OK" : "FAIL") + " — " + detail);
    return ok;
  }

  // ─── 1. WRITE — LLM generates program source ──────────────────────
  var writePrompt = buildWritePrompt(request, opts.codeGenBase);
  var writeResp;
  try {
    writeResp = llm.codegen(writePrompt);
  } catch (e) {
    step("WRITE", false, "LLM error: " + (e.message || e));
    return { ok: false, steps: log, error: "WRITE failed" };
  }

  if (!writeResp) {
    step("WRITE", false, "empty LLM response");
    return { ok: false, steps: log, error: "WRITE failed" };
  }

  var source = extractCodeBlock(writeResp);
  if (!source || source.trim().length === 0) {
    step("WRITE", false, "no code block in LLM response");
    return { ok: false, steps: log, error: "WRITE failed" };
  }

  // Extract tool name from the response or generate from request
  var toolName = extractToolName(writeResp, request);
  step("WRITE", true, toolName + " (" + source.split("\n").length + " lines)");

  // ─── 2. TEST — js.eval with real args ──────────────────────────────
  var testArgs = {
    apiBaseUrl: args.apiBaseUrl || env.ANYTYPE_API_URL,
    apiKey: args.apiKey || env.ANYTYPE_API_KEY,
    spaceId: args.spaceId || env.ANYTYPE_SPACE_ID
  };
  // Merge any extra args the user's request implies (parsed from LLM response)
  var extraArgs = extractTestArgs(writeResp);
  for (var k in extraArgs) testArgs[k] = extraArgs[k];

  var evalResult = js.eval(source, testArgs);
  var detected = detectError(evalResult);

  if (!detected.hasError) {
    var resPreview = typeof evalResult.result === "string"
      ? evalResult.result.substring(0, 100)
      : JSON.stringify(evalResult.result).substring(0, 100);
    step("TEST", true, resPreview);
  } else {
    step("TEST", false, detected.errorType + ": " + detected.message.substring(0, 100));

    // ─── 3. TRACE — tracer attempts fix ────────────────────────────
    var tracerResult;
    try {
      tracerResult = runTracer({
        code: source,
        args: testArgs,
        error: detected.message,
        result: evalResult.result,
        traces: evalResult.traces,
        stepTitle: "Fix tool: " + request
      });
    } catch (e) {
      step("TRACE", false, "tracer error: " + (e.message || e));
      return { ok: false, steps: log, error: "TRACE failed" };
    }

    if (tracerResult.fixed) {
      // Verify fix with real execution
      var verifyResult = js.eval(tracerResult.code, testArgs);
      if (!verifyResult.error) {
        source = tracerResult.code;
        step("TRACE", true, "fixed in " + tracerResult.iterations + " iteration(s)");
      } else {
        step("TRACE", false, "fix failed real execution: " + verifyResult.error.substring(0, 100));
        return { ok: false, steps: log, error: "TRACE could not fix the code" };
      }
    } else {
      step("TRACE", false, tracerResult.diagnosis.substring(0, 150));
      return { ok: false, steps: log, error: "TRACE could not fix the code" };
    }
  }

  // ─── 4. SCHEMA — LLM generates Tool Description + Tool Schema ─────
  var schemaPrompt = buildSchemaPrompt(toolName, source, request, opts.codeGenBase);
  var schemaResp;
  try {
    schemaResp = llm.codegen(schemaPrompt);
  } catch (e) {
    step("SCHEMA", false, "LLM error: " + (e.message || e));
    return { ok: false, steps: log, error: "SCHEMA failed" };
  }

  if (!schemaResp) {
    step("SCHEMA", false, "empty LLM response");
    return { ok: false, steps: log, error: "SCHEMA failed" };
  }

  // Extract the schema markdown (everything between the markers or the whole response)
  var schema = extractSchema(schemaResp);
  if (!schema) {
    step("SCHEMA", false, "could not extract schema from response");
    return { ok: false, steps: log, error: "SCHEMA failed" };
  }
  step("SCHEMA", true, schema.substring(0, 80) + "...");

  // ─── 5. REGISTER — saveTool with schema ───────────────────────────
  var saveResult = client.saveTool({
    name: toolName,
    source: source,
    schema: schema,
    title: toolName
  });

  if (!saveResult.ok) {
    step("REGISTER", false, saveResult.error);
    return { ok: false, steps: log, error: "REGISTER failed: " + saveResult.error };
  }
  step("REGISTER", true, "saved as " + toolName + "@" + (saveResult.version || "v1"));

  // ─── 6. DISCOVER — verify getTools finds it ──────────────────────
  var tools = client.getTools();
  var discovered = false;
  for (var i = 0; i < tools.length; i++) {
    if (tools[i].programName === toolName) {
      discovered = true;
      break;
    }
  }

  if (!discovered) {
    step("DISCOVER", false, "getTools() does not find " + toolName);
    return { ok: false, steps: log, error: "DISCOVER failed" };
  }

  // Verify extractToolMethods parses the schema
  var methods = extractToolMethods(client, saveResult.object.id);
  if (!methods || !methods.methods || methods.methods.length === 0) {
    step("DISCOVER", false, "extractToolMethods returned no methods");
    return { ok: false, steps: log, error: "DISCOVER failed — schema not parseable" };
  }
  step("DISCOVER", true, methods.methods.length + " method(s), description: " + (methods.description || "").substring(0, 60));

  // ─── 7. INVOKE — run the tool via runProgram ─────────────────────
  var invokeResult = client.runProgram(toolName, testArgs);
  if (!invokeResult.ok) {
    step("INVOKE", false, invokeResult.error || "runProgram failed");
    return { ok: false, steps: log, error: "INVOKE failed" };
  }

  var invokePreview = typeof invokeResult.result === "string"
    ? invokeResult.result.substring(0, 100)
    : JSON.stringify(invokeResult.result).substring(0, 100);
  step("INVOKE", true, invokePreview);

  return { ok: true, toolName: toolName, steps: log };
}

// ─── Prompt builders ─────────────────────────────────────────────────────────

function buildWritePrompt(request, codeGenBase) {
  var prompt = "";

  // Use the assistant's assembled codegen context if available
  if (codeGenBase) {
    prompt += codeGenBase + "\n\n";
  }

  prompt += "## Task: Write a New Tool Program\n\n"
    + "User request: " + request + "\n\n"
    + "Write a program that fulfills this request as a reusable tool.\n\n"
    + "Requirements:\n"
    + "- The program MUST have: export function main(args) { ... }\n"
    + "- Use args for all configuration (API keys, URLs, etc.) — never hardcode secrets\n"
    + "- args.apiBaseUrl, args.apiKey, args.spaceId are always available (Anytype credentials)\n"
    + "- Return a useful result (string or object), never null/undefined\n"
    + "- Keep it simple — one clear purpose\n\n"
    + "Output:\n"
    + "1. First line: TOOL_NAME=<short_snake_case_name> (e.g. TOOL_NAME=weather_checker)\n"
    + "2. A ```javascript code block with the full program\n"
    + "3. TEST_ARGS: a JSON object with example args for testing (beyond the standard apiBaseUrl/apiKey/spaceId)\n";

  return prompt;
}

function buildSchemaPrompt(toolName, source, request, codeGenBase) {
  var prompt = "";

  // Use assistant context if available — it contains existing tool schemas as examples
  if (codeGenBase) {
    prompt += codeGenBase + "\n\n";
  }

  prompt += "## Task: Generate Tool Schema\n\n"
    + "Generate a Tool Description and Tool Schema for this program.\n\n"
    + "Program name: " + toolName + "\n"
    + "Original request: " + request + "\n\n"
    + "```javascript\n" + source + "\n```\n\n"
    + "Output ONLY the markdown below (no code fences, no explanation):\n\n"
    + "## Tool Description\n"
    + "<one line: what this tool does and when to use it>\n\n"
    + "## Tool Schema\n\n"
    + "### main(args)\n"
    + "<one line: what main does>\n"
    + "- args.param1 (type, required/optional): description\n"
    + "- args.param2 (type, required/optional): description\n"
    + "- Returns: { field: type, ... }\n\n"
    + "**Example (run as child program):**\n"
    + "```js\n"
    + "var result = client.runProgram(\"" + toolName + "\", { param1: \"value\" });\n"
    + "// result.result = { field: \"...\", ... }\n"
    + "```\n\n"
    + "**Example (import as module):**\n"
    + "```js\n"
    + "import { main } from \"" + toolName + "@v1\";\n"
    + "var result = main({ param1: \"value\" });\n"
    + "// result = { field: \"...\", ... }\n"
    + "```\n\n"
    + "#### Example queries\n"
    + "- \"<natural language query>\" → main({ param1: \"value\" })\n"
    + "- \"<another query>\" → main({ param2: \"value\" })\n";

  return prompt;
}

// ─── Extraction helpers ──────────────────────────────────────────────────────

function extractCodeBlock(text) {
  var start = text.indexOf("```javascript");
  if (start === -1) start = text.indexOf("```js");
  if (start === -1) start = text.indexOf("```");
  if (start === -1) return null;

  var codeStart = text.indexOf("\n", start);
  if (codeStart === -1) return null;
  codeStart += 1;

  var codeEnd = text.indexOf("```", codeStart);
  if (codeEnd === -1) return text.substring(codeStart);

  return text.substring(codeStart, codeEnd);
}

function extractToolName(llmResponse, request) {
  // Try TOOL_NAME= line
  var match = llmResponse.match(/TOOL_NAME\s*=\s*(\S+)/);
  if (match) return match[1].replace(/[^a-z0-9_]/g, "");

  // Generate from request: first few words, snake_case
  var words = request.toLowerCase().replace(/[^a-z0-9\s]/g, "").split(/\s+/).slice(0, 3);
  return words.join("_") || "custom_tool";
}

function extractTestArgs(llmResponse) {
  var match = llmResponse.match(/TEST_ARGS:\s*(\{[^}]+\})/);
  if (!match) return {};
  try {
    return JSON.parse(match[1]);
  } catch (e) {
    return {};
  }
}

function extractSchema(llmResponse) {
  // The response should be raw markdown starting with "## Tool Description"
  // But it might be wrapped in a code fence
  var text = llmResponse.trim();

  // Strip code fence if present
  if (text.indexOf("```") === 0) {
    var firstNl = text.indexOf("\n");
    if (firstNl !== -1) {
      text = text.substring(firstNl + 1);
      var lastFence = text.lastIndexOf("```");
      if (lastFence !== -1) text = text.substring(0, lastFence);
    }
  }

  // Verify it has the required sections
  if (text.indexOf("## Tool Description") !== -1 && text.indexOf("## Tool Schema") !== -1) {
    return text.trim();
  }

  // Try to find it within a larger response
  var descIdx = text.indexOf("## Tool Description");
  if (descIdx !== -1) {
    return text.substring(descIdx).trim();
  }

  return null;
}

export function main(args) {
  return "toolBuilder module — use buildTool(client, request, args)";
}
