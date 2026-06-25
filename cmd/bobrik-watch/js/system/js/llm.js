// __main_source
// LLM completion interface — dispatches to provider from config
// Supports prompt caching: chat(messages, { cachedPrefix: "static part" })
// Supports model tiers: classify(), codegen()
import { main as getConfig } from "config@v1"

// Model tier defaults per provider — only consulted when a config@v1 TIERS
// entry is provider-only (no "/model"), or for the opts.provider override path.
// classify: binary/list selection, structured output (cheapest, fastest)
// codegen: JS code generation — the toolcaller agent's main loop
var MODEL_TIERS = {
  claude: { classify: "claude-haiku-4-5-20251001", codegen: "claude-sonnet-4-6" },
  openai: { classify: "gpt-5-mini", codegen: "gpt-5" },
  together: { classify: "meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo", codegen: "Qwen/Qwen2.5-7B-Instruct-Turbo" },
  openrouter: { classify: "z-ai/glm-5.1", codegen: "z-ai/glm-5.1" },
  local:  { classify: null, codegen: null }
};

function completeClaude(prompt, model, config, opts) {
  var apiKey = config.CLAUDE_API_KEY;
  if (!apiKey) throw new Error("CLAUDE_API_KEY not set in config@v1");
  model = model || config.CLAUDE_MODEL || "claude-sonnet-4-6";

  var messageContent;
  if (opts && opts.cachedPrefix) {
    messageContent = [
      { type: "text", text: opts.cachedPrefix, cache_control: { type: "ephemeral" } },
      { type: "text", text: prompt }
    ];
  } else {
    messageContent = prompt;
  }

  var response = fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "x-api-key": apiKey,
      "anthropic-version": "2023-06-01"
    },
    body: JSON.stringify({
      model: model,
      max_tokens: 4096,
      messages: [{ role: "user", content: messageContent }]
    })
  });

  if (!response.ok) {
    throw new Error("Claude API error: " + response.status + " " + JSON.stringify(response.body));
  }

  var content = response.body.content;
  if (!content || content.length === 0) return null;
  for (var i = 0; i < content.length; i++) {
    if (content[i].type === "text") return content[i].text;
  }
  return null;
}

// chatClaude — multi-turn message API with native tools support.
// Returns the FULL response body (not just text) so callers can read content
// blocks, stop_reason, tool_use blocks, usage, etc.
//
// opts: {
//   system: string | array of content blocks (cache_control supported),
//   tools:  [{name, description, input_schema}, ...],
//   tool_choice: "auto" | "any" | {type: "tool", name: "..."},
//   max_tokens: int (default 4096),
//   model: string (overrides resolved model),
//   disable_parallel_tool_use: bool
// }
function chatClaude(messages, model, config, opts) {
  var apiKey = config.CLAUDE_API_KEY;
  if (!apiKey) throw new Error("CLAUDE_API_KEY not set in config@v1");
  model = model || config.CLAUDE_MODEL || "claude-sonnet-4-6";

  // Default max_tokens: 32768. Sonnet 4.6 supports up to ~64K output;
  // 32K is the usual sweet spot — plenty of headroom for large batch cells
  // or rich-content turns without pushing toward the latency edge. Billing
  // is on actual output so the ceiling costs nothing when unused. Callers
  // can still override via opts.max_tokens for specific calls (e.g. small
  // classify tasks that should cap at 4K).
  var body = {
    model: model,
    max_tokens: (opts && opts.max_tokens) || 32768,
    messages: messages
  };

  if (opts && opts.system) {
    body.system = opts.system;
  }

  if (opts && opts.tools && opts.tools.length > 0) {
    body.tools = opts.tools;
  }

  if (opts && opts.tool_choice) {
    body.tool_choice = opts.tool_choice;
  }

  if (opts && opts.disable_parallel_tool_use) {
    if (!body.tool_choice) body.tool_choice = { type: "auto" };
    body.tool_choice.disable_parallel_tool_use = true;
  }

  var response = fetch("https://api.anthropic.com/v1/messages", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "x-api-key": apiKey,
      "anthropic-version": "2023-06-01"
    },
    body: JSON.stringify(body)
  });

  if (!response.ok) {
    throw new Error("Claude chat API error: " + response.status + " " + JSON.stringify(response.body));
  }

  return response.body;
}

// GPT-5 / o-series reasoning models reject `max_tokens` and require
// `max_completion_tokens`. gpt-4.x still accepts `max_tokens`.
function _openAIUsesCompletionTokens(model) {
  return model.indexOf("gpt-5") === 0 ||
    model.indexOf("o1") === 0 ||
    model.indexOf("o3") === 0 ||
    model.indexOf("o4") === 0;
}

function completeOpenAI(prompt, model, config, opts) {
  var apiKey = config.OPENAI_API_KEY;
  if (!apiKey) throw new Error("OPENAI_API_KEY not set in config@v1");
  model = model || config.OPENAI_MODEL || "gpt-5";

  var fullPrompt = (opts && opts.cachedPrefix) ? opts.cachedPrefix + prompt : prompt;

  var body = {
    model: model,
    messages: [{ role: "user", content: fullPrompt }]
  };
  if (_openAIUsesCompletionTokens(model)) body.max_completion_tokens = 4096;
  else body.max_tokens = 4096;

  var response = fetch("https://api.openai.com/v1/chat/completions", {
    method: "POST",
    headers: {
      "Authorization": "Bearer " + apiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify(body)
  });

  if (!response.ok) {
    throw new Error("OpenAI API error: " + response.status + " " + JSON.stringify(response.body));
  }

  var choices = response.body.choices;
  if (!choices || choices.length === 0) return null;
  return choices[0].message.content;
}

function completeTogether(prompt, model, config, opts) {
  var apiKey = config.TOGETHER_AI_API_KEY;
  if (!apiKey) throw new Error("TOGETHER_AI_API_KEY not set in config@v1");
  model = model || "meta-llama/Llama-3.3-70B-Instruct-Turbo";

  var fullPrompt = (opts && opts.cachedPrefix) ? opts.cachedPrefix + prompt : prompt;

  var response = fetch("https://api.together.xyz/v1/chat/completions", {
    method: "POST",
    headers: {
      "Authorization": "Bearer " + apiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      model: model,
      max_tokens: 4096,
      messages: [{ role: "user", content: fullPrompt }]
    })
  });

  if (!response.ok) {
    throw new Error("Together API error: " + response.status + " " + JSON.stringify(response.body));
  }

  var choices = response.body.choices;
  if (!choices || choices.length === 0) return null;
  var content = choices[0].message.content;
  // Strip <think>...</think> blocks from reasoning models
  if (content && content.indexOf("<think>") !== -1) {
    content = content.replace(/<think>[\s\S]*?<\/think>\s*/g, "");
  }
  return content;
}

function completeOpenRouter(prompt, model, config, opts) {
  var apiKey = config.OPENROUTER_API_KEY;
  if (!apiKey) throw new Error("OPENROUTER_API_KEY not set in config@v1");
  model = model || config.OPENROUTER_MODEL || "z-ai/glm-5.1";

  var fullPrompt = (opts && opts.cachedPrefix) ? opts.cachedPrefix + prompt : prompt;

  var response = fetch("https://openrouter.ai/api/v1/chat/completions", {
    method: "POST",
    headers: {
      "Authorization": "Bearer " + apiKey,
      "Content-Type": "application/json",
      "X-Title": "anytype-agent-runtime"
    },
    body: JSON.stringify({
      model: model,
      max_tokens: 4096,
      messages: [{ role: "user", content: fullPrompt }]
    })
  });

  if (!response.ok) {
    throw new Error("OpenRouter API error: " + response.status + " " + JSON.stringify(response.body));
  }

  var choices = response.body.choices;
  if (!choices || choices.length === 0) return null;
  var content = choices[0].message.content;
  if (content && content.indexOf("<think>") !== -1) {
    content = content.replace(/<think>[\s\S]*?<\/think>\s*/g, "");
  }
  return content;
}

// ── Anthropic ↔ OpenAI shape translators (used by chatOpenRouter) ────────────
// toolcall_core@v1 speaks Anthropic's tools API (system blocks, content blocks,
// tool_use / tool_result blocks, stop_reason). OpenRouter speaks OpenAI's chat
// completions shape (system role message, tool_calls field, role:"tool" reply,
// finish_reason). These functions translate between the two so callers stay
// provider-agnostic.

// Flatten Anthropic system spec (string OR array of {type,text,cache_control})
// into a single OpenAI-style system message string. cache_control is dropped
// (OpenRouter only honors it for some upstreams; not portable to GLM).
function _flattenSystemToString(system) {
  if (!system) return null;
  if (typeof system === "string") return system;
  if (system && system.length) {
    var parts = [];
    for (var i = 0; i < system.length; i++) {
      var b = system[i];
      if (b && b.type === "text" && b.text) parts.push(b.text);
      else if (typeof b === "string") parts.push(b);
    }
    return parts.join("\n\n");
  }
  return null;
}

// Convert one Anthropic tool_result block into one OpenAI {role:"tool"} message.
// Anthropic tool_result.content can be a string OR an array of {type,text,...}.
function _toolResultBlockToOpenAI(block) {
  var content = block.content;
  if (typeof content !== "string") {
    if (content && content.length) {
      var parts = [];
      for (var i = 0; i < content.length; i++) {
        var c = content[i];
        if (c && c.type === "text" && c.text) parts.push(c.text);
        else if (typeof c === "string") parts.push(c);
      }
      content = parts.join("\n");
    } else {
      content = String(content || "");
    }
  }
  if (block.is_error) content = "[ERROR] " + content;
  return { role: "tool", tool_call_id: block.tool_use_id, content: content };
}

// Convert Anthropic messages → OpenAI messages. Splits user messages that
// contain tool_result blocks into one or more {role:"tool"} messages, and
// converts assistant messages with tool_use blocks into {tool_calls:[...]}
// shape.
function _messagesAnthropicToOpenAI(messages) {
  var out = [];
  for (var i = 0; i < messages.length; i++) {
    var m = messages[i];
    var role = m.role;
    var c = m.content;

    if (typeof c === "string") {
      out.push({ role: role, content: c });
      continue;
    }

    if (role === "assistant") {
      // Assistant message: collect text into content, tool_use into tool_calls.
      var textParts = [];
      var toolCalls = [];
      for (var j = 0; j < c.length; j++) {
        var b = c[j];
        if (!b || !b.type) continue;
        if (b.type === "text") {
          if (b.text) textParts.push(b.text);
        } else if (b.type === "tool_use") {
          toolCalls.push({
            id: b.id,
            type: "function",
            function: {
              name: b.name,
              arguments: typeof b.input === "string" ? b.input : JSON.stringify(b.input || {})
            }
          });
        }
      }
      var asst = { role: "assistant", content: textParts.join("\n") || null };
      if (toolCalls.length > 0) asst.tool_calls = toolCalls;
      out.push(asst);
      continue;
    }

    // role === "user": may contain text + tool_result blocks. Tool_result
    // blocks must become standalone {role:"tool"} messages; any text blocks
    // get emitted as a regular user message.
    var userText = [];
    var toolMsgs = [];
    for (var k = 0; k < c.length; k++) {
      var bb = c[k];
      if (!bb || !bb.type) continue;
      if (bb.type === "tool_result") {
        toolMsgs.push(_toolResultBlockToOpenAI(bb));
      } else if (bb.type === "text") {
        if (bb.text) userText.push(bb.text);
      }
    }
    if (userText.length > 0) out.push({ role: "user", content: userText.join("\n") });
    for (var ti = 0; ti < toolMsgs.length; ti++) out.push(toolMsgs[ti]);
  }
  return out;
}

// Convert Anthropic tools[] → OpenAI tools[]
function _toolsAnthropicToOpenAI(tools) {
  var out = [];
  for (var i = 0; i < tools.length; i++) {
    var t = tools[i];
    out.push({
      type: "function",
      function: {
        name: t.name,
        description: t.description || "",
        parameters: t.input_schema || { type: "object", properties: {} }
      }
    });
  }
  return out;
}

// Convert Anthropic tool_choice → OpenAI tool_choice
function _toolChoiceAnthropicToOpenAI(tc) {
  if (!tc) return null;
  if (typeof tc === "string") {
    if (tc === "any") return "required";
    return tc;  // "auto" passes through
  }
  if (tc.type === "tool" && tc.name) {
    return { type: "function", function: { name: tc.name } };
  }
  if (tc.type === "auto") return "auto";
  if (tc.type === "any") return "required";
  return tc;
}

// Map OpenAI finish_reason → Anthropic stop_reason
function _finishReasonToStopReason(fr) {
  if (fr === "stop") return "end_turn";
  if (fr === "tool_calls") return "tool_use";
  if (fr === "length") return "max_tokens";
  if (fr === "content_filter") return "stop_sequence";
  return fr || "end_turn";
}

// Convert one OpenAI choice's message → array of Anthropic content blocks
function _openaiMessageToAnthropicContent(msg) {
  var blocks = [];
  if (msg.content && typeof msg.content === "string" && msg.content.length > 0) {
    blocks.push({ type: "text", text: msg.content });
  } else if (msg.content && msg.content.length) {
    // Some OpenAI-compat servers return array content blocks; flatten text parts.
    var parts = [];
    for (var i = 0; i < msg.content.length; i++) {
      var p = msg.content[i];
      if (p && p.type === "text" && p.text) parts.push(p.text);
      else if (typeof p === "string") parts.push(p);
    }
    if (parts.length > 0) blocks.push({ type: "text", text: parts.join("\n") });
  }
  if (msg.tool_calls && msg.tool_calls.length) {
    for (var j = 0; j < msg.tool_calls.length; j++) {
      var tc = msg.tool_calls[j];
      var fn = tc.function || {};
      var input;
      var rawArgs = fn.arguments;
      if (typeof rawArgs === "object" && rawArgs !== null) {
        input = rawArgs;
      } else if (typeof rawArgs === "string" && rawArgs.length > 0) {
        try { input = JSON.parse(rawArgs); }
        catch (e) { input = { __raw_arguments: rawArgs, __parse_error: e.message }; }
      } else {
        input = {};
      }
      blocks.push({
        type: "tool_use",
        id: tc.id || ("call_" + j + "_" + Date.now()),
        name: fn.name,
        input: input
      });
    }
  }
  return blocks;
}

// _chatOpenAICompat — shared chat() implementation for any OpenAI-compatible
// chat completions endpoint. Accepts Anthropic-shape messages/opts (so
// toolcall_core@v1 stays unchanged) and returns an Anthropic-shape body
// {content, stop_reason, usage}. Per-provider differences (URL, auth headers,
// max-token field name, default ceiling) are passed in via providerCfg:
//   { url, apiKey, extraHeaders, label, defaultMaxTokens, useMaxCompletionTokens }
function _chatOpenAICompat(messages, model, opts, providerCfg) {
  var oaiMessages = _messagesAnthropicToOpenAI(messages);
  var systemStr = (opts && opts.system) ? _flattenSystemToString(opts.system) : null;
  if (systemStr) {
    oaiMessages = [{ role: "system", content: systemStr }].concat(oaiMessages);
  }

  var body = { model: model, messages: oaiMessages };
  var maxTokens = (opts && opts.max_tokens) || providerCfg.defaultMaxTokens;
  // GPT-5 and o-series reasoning models reject `max_tokens` and require
  // `max_completion_tokens`. Other OpenAI-compatible servers (OpenRouter,
  // GLM, etc.) still want `max_tokens`.
  if (providerCfg.useMaxCompletionTokens) body.max_completion_tokens = maxTokens;
  else body.max_tokens = maxTokens;

  if (opts && opts.tools && opts.tools.length > 0) {
    body.tools = _toolsAnthropicToOpenAI(opts.tools);
  }
  if (opts && opts.tool_choice) {
    body.tool_choice = _toolChoiceAnthropicToOpenAI(opts.tool_choice);
  }
  if (opts && opts.disable_parallel_tool_use) {
    body.parallel_tool_calls = false;
  }

  var headers = {
    "Authorization": "Bearer " + providerCfg.apiKey,
    "Content-Type": "application/json"
  };
  if (providerCfg.extraHeaders) {
    for (var hk in providerCfg.extraHeaders) {
      if (providerCfg.extraHeaders.hasOwnProperty(hk)) headers[hk] = providerCfg.extraHeaders[hk];
    }
  }

  var response = fetch(providerCfg.url, {
    method: "POST",
    headers: headers,
    body: JSON.stringify(body)
  });

  if (!response.ok) {
    throw new Error(providerCfg.label + " chat API error: " + response.status + " " + JSON.stringify(response.body));
  }

  var choices = response.body.choices;
  if (!choices || choices.length === 0) {
    return { content: [], stop_reason: "end_turn", usage: response.body.usage || null };
  }
  var choice = choices[0];
  return {
    content: _openaiMessageToAnthropicContent(choice.message || {}),
    stop_reason: _finishReasonToStopReason(choice.finish_reason),
    usage: response.body.usage || null,
    model: response.body.model || model
  };
}

// chatOpenRouter — Anthropic-shape chat() over OpenRouter.
// GLM-5.1 caps at 65535 output tokens per OpenRouter top_provider metadata;
// default to the cap (billing is on actual output, and toolcall_core has
// max_tokens recovery if hit).
function chatOpenRouter(messages, model, config, opts) {
  var apiKey = config.OPENROUTER_API_KEY;
  if (!apiKey) throw new Error("OPENROUTER_API_KEY not set in config@v1");
  model = model || config.OPENROUTER_MODEL || "z-ai/glm-5.1";
  return _chatOpenAICompat(messages, model, opts, {
    url: "https://openrouter.ai/api/v1/chat/completions",
    apiKey: apiKey,
    extraHeaders: { "X-Title": "anytype-agent-runtime" },
    label: "OpenRouter",
    defaultMaxTokens: 65535,
    useMaxCompletionTokens: false
  });
}

// chatOpenAI — Anthropic-shape chat() over OpenAI's chat completions API.
// GPT-5 and o-series reasoning models require `max_completion_tokens`; the
// older gpt-4.x family still accepts `max_tokens`. Detect by model prefix.
function chatOpenAI(messages, model, config, opts) {
  var apiKey = config.OPENAI_API_KEY;
  if (!apiKey) throw new Error("OPENAI_API_KEY not set in config@v1");
  model = model || config.OPENAI_MODEL || "gpt-5";
  var useCompletionTokens =
    model.indexOf("gpt-5") === 0 ||
    model.indexOf("o1") === 0 ||
    model.indexOf("o3") === 0 ||
    model.indexOf("o4") === 0;
  return _chatOpenAICompat(messages, model, opts, {
    url: "https://api.openai.com/v1/chat/completions",
    apiKey: apiKey,
    extraHeaders: null,
    label: "OpenAI",
    defaultMaxTokens: 16384,
    useMaxCompletionTokens: useCompletionTokens
  });
}

function completeLocal(prompt, model, config, opts) {
  var baseUrl = config.LOCAL_LLM_URL || "http://localhost:8080";
  model = model || config.LOCAL_LLM_MODEL || "";

  var fullPrompt = (opts && opts.cachedPrefix) ? opts.cachedPrefix + prompt : prompt;
  var body = { messages: [{ role: "user", content: fullPrompt }] };
  if (model) body.model = model;

  var headers = { "Content-Type": "application/json" };
  if (config.LLM_API_KEY) {
    headers["Authorization"] = "Bearer " + config.LLM_API_KEY;
  }

  var response = fetch(baseUrl + "/v1/chat/completions", {
    method: "POST",
    headers: headers,
    body: JSON.stringify(body)
  });

  if (!response.ok) {
    throw new Error("Local LLM error: " + response.status + " " + JSON.stringify(response.body));
  }

  var choices = response.body.choices;
  if (!choices || choices.length === 0) return null;
  return choices[0].message.content;
}

var providers = {
  claude: completeClaude,
  openai: completeOpenAI,
  together: completeTogether,
  openrouter: completeOpenRouter,
  local: completeLocal
};

// Parse a tier spec: "openrouter/z-ai/glm-5.1" → {provider:"openrouter", model:"z-ai/glm-5.1"}
//                     "claude" → {provider:"claude", model:null}
function parseTierSpec(spec) {
  if (!spec) return null;
  var slash = spec.indexOf("/");
  if (slash >= 0) {
    return { provider: spec.substring(0, slash), model: spec.substring(slash + 1) };
  }
  return { provider: spec, model: null };
}

// Resolve provider + model for a given tier
// Returns {provider, model}
function resolveTier(config, tier) {
  if (!tier) throw new Error("resolveTier requires a tier name. Use: classify, codegen, or a search_* tier.");
  var spec = config.TIERS && config.TIERS[tier];
  var parsed = parseTierSpec(spec);

  if (!parsed) {
    throw new Error("TIERS." + tier + " not configured in config@v1. Available tiers: " + (config.TIERS ? Object.keys(config.TIERS).join(", ") : "none"));
  }

  // If only provider specified (no model), look up MODEL_TIERS default
  if (!parsed.model) {
    var pt = MODEL_TIERS[parsed.provider];
    parsed.model = (pt && tier && pt[tier]) || (pt && pt["default"]) || null;
  }

  return parsed;
}

// resolveModel(tier) — public {provider, model} for a config tier, WITHOUT
// making an LLM call. For instrumentation (e.g. stamping which model a tier
// resolves to into stats so it's observable after the fact). Returns
// {provider, model} or null if the tier isn't configured.
export function resolveModel(tier) {
  try {
    return resolveTier(getConfig(), tier);
  } catch (e) {
    return null;
  }
}

// embed(text) — generate embedding vector via OpenAI API
// Returns array of floats, or null on failure
export function embed(text) {
  var config = getConfig();
  var apiKey = config.OPENAI_API_KEY;
  if (!apiKey) throw new Error("OPENAI_API_KEY not set in config@v1");

  var model = config.EMBED_MODEL || "text-embedding-3-small";

  var response = fetch("https://api.openai.com/v1/embeddings", {
    method: "POST",
    headers: {
      "Authorization": "Bearer " + apiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      model: model,
      input: text
    })
  });

  if (!response.ok) {
    throw new Error("OpenAI embed error: " + response.status + " " + JSON.stringify(response.body));
  }

  var data = response.body.data;
  if (!data || data.length === 0) return null;
  return data[0].embedding;
}

// embedBatch(texts[]) — generate embeddings for multiple texts in one API call.
// Returns array of embedding vectors (same order as input), or nulls on failure.
export function embedBatch(texts) {
  if (!texts || texts.length === 0) return [];
  if (texts.length === 1) return [embed(texts[0])];

  var config = getConfig();
  var apiKey = config.OPENAI_API_KEY;
  if (!apiKey) throw new Error("OPENAI_API_KEY not set in config@v1");

  var model = config.EMBED_MODEL || "text-embedding-3-small";

  var response = fetch("https://api.openai.com/v1/embeddings", {
    method: "POST",
    headers: {
      "Authorization": "Bearer " + apiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      model: model,
      input: texts
    })
  });

  if (!response.ok) {
    throw new Error("OpenAI embedBatch error: " + response.status + " " + JSON.stringify(response.body));
  }

  var data = response.body.data;
  if (!data) return texts.map(function() { return null; });

  // API returns embeddings with index field — sort to match input order
  var sorted = [];
  for (var i = 0; i < texts.length; i++) sorted.push(null);
  for (var i = 0; i < data.length; i++) {
    sorted[data[i].index] = data[i].embedding;
  }
  return sorted;
}

// completeBatch(prompts[], tier?) — run multiple LLM completions in parallel via fetchBatch.
// Returns array of response strings (same order as input), or nulls on failure.
// Optional tier: "classify", "codegen", a search_* tier, or null for default.
export function completeBatch(prompts, tier) {
  if (!prompts || prompts.length === 0) return [];
  if (prompts.length === 1) return [_complete(prompts[0], null, tier || null)];

  var config = getConfig();
  var resolved = resolveTier(config, tier || null);

  var fetchArgs = [];
  for (var i = 0; i < prompts.length; i++) {
    var fa = _buildFetchArgs(resolved.provider, prompts[i], resolved.model, config);
    if (fa) fetchArgs.push(fa);
  }

  var responses = fetchBatch(fetchArgs);

  var results = [];
  for (var i = 0; i < responses.length; i++) {
    results.push(_extractResponse(resolved.provider, responses[i]));
  }
  return results;
}

// completeBatchDetailed(prompts[], tier) — completeBatch plus per-prompt token
// usage, for callers that account tokens burned by batched sub-calls (the
// search@v1 RLM loop's stats). Returns [{text, inTokens, outTokens}] in input
// order; text is null on per-prompt failure (usage zeros). Unlike
// completeBatch there is no single-prompt fast path — one prompt is still one
// fetchBatch so usage extraction stays uniform.
export function completeBatchDetailed(prompts, tier, opts) {
  if (!prompts || prompts.length === 0) return [];
  var config = getConfig();
  var resolved = resolveTier(config, tier || "classify");
  var provider = resolved.provider;
  var model = resolved.model;
  // opts.{provider, model} override the tier resolution (per-call A/B without
  // touching config TIERS) — same contract as _chatDispatch.
  if (opts && opts.provider) {
    provider = opts.provider;
    model = opts.model || (MODEL_TIERS[provider] ? MODEL_TIERS[provider][tier || "classify"] : null);
  } else if (opts && opts.model) {
    model = opts.model;
  }

  var fetchArgs = [];
  for (var i = 0; i < prompts.length; i++) {
    var fa = _buildFetchArgs(provider, prompts[i], model, config);
    if (fa) fetchArgs.push(fa);
  }

  var responses = fetchBatch(fetchArgs);

  var results = [];
  for (var j = 0; j < responses.length; j++) {
    var u = _extractUsage(responses[j]);
    results.push({
      text: _extractResponse(provider, responses[j]),
      inTokens: u.inTokens,
      outTokens: u.outTokens
    });
  }
  return results;
}

// Pull token usage out of a raw fetch response body. Anthropic uses
// input_tokens/output_tokens, OpenAI-shaped providers prompt_tokens/
// completion_tokens — read both, first non-zero wins.
function _extractUsage(response) {
  var u = response && response.body && response.body.usage;
  if (!u) return { inTokens: 0, outTokens: 0 };
  return {
    inTokens: u.input_tokens || u.prompt_tokens || 0,
    outTokens: u.output_tokens || u.completion_tokens || 0
  };
}

// Build [url, opts] for a single completion call (used by completeBatch)
function _buildFetchArgs(provider, prompt, model, config) {
  if (provider === "claude") {
    var apiKey = config.CLAUDE_API_KEY;
    model = model || config.CLAUDE_MODEL || "claude-sonnet-4-6";
    return ["https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "x-api-key": apiKey,
        "anthropic-version": "2023-06-01"
      },
      body: JSON.stringify({
        model: model,
        max_tokens: 4096,
        messages: [{ role: "user", content: prompt }]
      })
    }];
  }
  if (provider === "openai") {
    var apiKey = config.OPENAI_API_KEY;
    model = model || config.OPENAI_MODEL || "gpt-5";
    var oaiBody = {
      model: model,
      messages: [{ role: "user", content: prompt }]
    };
    if (_openAIUsesCompletionTokens(model)) oaiBody.max_completion_tokens = 4096;
    else oaiBody.max_tokens = 4096;
    return ["https://api.openai.com/v1/chat/completions", {
      method: "POST",
      headers: {
        "Authorization": "Bearer " + apiKey,
        "Content-Type": "application/json"
      },
      body: JSON.stringify(oaiBody)
    }];
  }
  if (provider === "together") {
    var apiKey = config.TOGETHER_AI_API_KEY;
    model = model || "meta-llama/Llama-3.3-70B-Instruct-Turbo";
    return ["https://api.together.xyz/v1/chat/completions", {
      method: "POST",
      headers: {
        "Authorization": "Bearer " + apiKey,
        "Content-Type": "application/json"
      },
      body: JSON.stringify({
        model: model,
        max_tokens: 4096,
        messages: [{ role: "user", content: prompt }]
      })
    }];
  }
  if (provider === "openrouter") {
    var apiKey = config.OPENROUTER_API_KEY;
    model = model || config.OPENROUTER_MODEL || "z-ai/glm-5.1";
    return ["https://openrouter.ai/api/v1/chat/completions", {
      method: "POST",
      headers: {
        "Authorization": "Bearer " + apiKey,
        "Content-Type": "application/json",
        "X-Title": "anytype-agent-runtime"
      },
      body: JSON.stringify({
        model: model,
        max_tokens: 4096,
        messages: [{ role: "user", content: prompt }]
      })
    }];
  }
  if (provider === "local") {
    var baseUrl = config.LOCAL_LLM_URL || "http://localhost:8080";
    model = model || config.LOCAL_LLM_MODEL || "";
    var body = { messages: [{ role: "user", content: prompt }] };
    if (model) body.model = model;
    var headers = { "Content-Type": "application/json" };
    if (config.LLM_API_KEY) headers["Authorization"] = "Bearer " + config.LLM_API_KEY;
    return [baseUrl + "/v1/chat/completions", {
      method: "POST",
      headers: headers,
      body: JSON.stringify(body)
    }];
  }
  return null;
}

// Extract text response from a raw fetch result based on provider
function _extractResponse(provider, response) {
  if (!response || !response.ok) return null;
  if (provider === "claude") {
    var content = response.body && response.body.content;
    if (!content || content.length === 0) return null;
    for (var i = 0; i < content.length; i++) {
      if (content[i].type === "text") return content[i].text;
    }
    return null;
  }
  // OpenAI, Local — all use choices[0].message.content
  var choices = response.body && response.body.choices;
  if (!choices || choices.length === 0) return null;
  return choices[0].message.content;
}

// Internal dispatch — resolves provider+model from tier + opts, calls provider
function _complete(prompt, opts, tier) {
  var config = getConfig();

  if (typeof opts === "string") {
    opts = { model: opts };
  }

  var resolved = resolveTier(config, tier);
  // Explicit model in opts takes precedence
  var model = (opts && opts.model) ? opts.model : resolved.model;
  var provider = resolved.provider;

  var fn = providers[provider];
  if (!fn) {
    throw new Error("Unknown provider: " + provider + ". Use: claude, openai, openrouter, together, local");
  }

  // Expose resolved provider/model for debug instrumentation
  _lastResolved.provider = provider;
  _lastResolved.model = model;

  return fn(prompt, model, config, opts);
}

// Shared state: last resolved provider/model (read by callers for debug logging)
var _lastResolved = { provider: "", model: "" };

// ── Trace wrapping ──────────────────────────────────────────────────────────
// When ENABLE_LLM_TRACE is true and __wrapTrace is available, each tier call
// is recorded in the runtime trace as "llm.<tier>" with input=prompt, output=response.
var ENABLE_LLM_TRACE = true;

function _wrapTier(tier) {
  if (ENABLE_LLM_TRACE && typeof __wrapTrace === "function") {
    return __wrapTrace("llm." + tier, function(prompt, opts) { return _complete(prompt, opts, tier); });
  }
  return function(prompt, opts) { return _complete(prompt, opts, tier); };
}

// Legacy named exports (backward compat) — prefer createLLM() for new code
export var classify = _wrapTier("classify");
export var codegen = _wrapTier("codegen");

// ── ask / askBatch — minimal text→text surface for tool authors ─────────────
// Routes through the "classify" tier so the default model is one config knob.
// Never throws — returns { ok, text, error } so callers can branch without try/catch.
function _ask(prompt) {
  try {
    var text = _complete(prompt, null, "classify");
    if (text == null || text === "") return { ok: false, text: null, error: "empty response" };
    return { ok: true, text: text, error: null };
  } catch (e) {
    return { ok: false, text: null, error: String((e && e.message) || e) };
  }
}

function _askBatch(prompts) {
  if (!prompts || prompts.length === 0) return [];
  var results;
  try {
    results = completeBatch(prompts, "classify");
  } catch (e) {
    var err = String((e && e.message) || e);
    var out = [];
    for (var i = 0; i < prompts.length; i++) out.push({ ok: false, text: null, error: err });
    return out;
  }
  var wrapped = [];
  for (var j = 0; j < results.length; j++) {
    var t = results[j];
    if (t == null || t === "") {
      wrapped.push({ ok: false, text: null, error: "empty response" });
    } else {
      wrapped.push({ ok: true, text: t, error: null });
    }
  }
  return wrapped;
}

export var ask = (ENABLE_LLM_TRACE && typeof __wrapTrace === "function")
  ? __wrapTrace("llm.ask", _ask)
  : _ask;

export var askBatch = (ENABLE_LLM_TRACE && typeof __wrapTrace === "function")
  ? __wrapTrace("llm.askBatch", _askBatch)
  : _askBatch;

// ── __prepareTraces — drop provider fetches from the visible trace ──────────
// Inner fetch/fetchBatch entries to LLM provider hosts duplicate the outer
// llm.ask / llm.askBatch / llm.chat / llm.<tier> wrapper traces and leak API
// keys via Authorization / x-api-key headers. toolcall_core chains every
// tool's __prepareTraces during tool_result prep.
var _LLM_HOSTS = [
  "https://api.anthropic.com",
  "https://api.openai.com",
  "https://api.together.xyz",
  "https://openrouter.ai"
];

function _isLLMHost(url) {
  if (!url) return false;
  for (var i = 0; i < _LLM_HOSTS.length; i++) {
    if (url.indexOf(_LLM_HOSTS[i]) === 0) return true;
  }
  return false;
}

function _urlFromFetchInput(input) {
  try {
    var parsed = JSON.parse(input);
    if (Array.isArray(parsed) && typeof parsed[0] === "string") return parsed[0];
    if (typeof parsed === "string") return parsed;
  } catch (e) {}
  return "";
}

function _allUrlsLLM(input) {
  try {
    var parsed = JSON.parse(input);
    if (!Array.isArray(parsed) || parsed.length === 0) return false;
    var batch = parsed[0];
    if (!Array.isArray(batch) || batch.length === 0) return false;
    for (var i = 0; i < batch.length; i++) {
      var pair = batch[i];
      var url = "";
      if (Array.isArray(pair) && typeof pair[0] === "string") url = pair[0];
      else if (typeof pair === "string") url = pair;
      if (!_isLLMHost(url)) return false;
    }
    return true;
  } catch (e) { return false; }
}

export function __prepareTraces(traces) {
  if (!traces) return traces;
  var out = {};
  for (var k in traces) {
    if (traces.hasOwnProperty(k)) out[k] = traces[k];
  }

  if (out.fetch) {
    var keptFetch = {};
    var anyFetch = false;
    for (var input in out.fetch) {
      if (!out.fetch.hasOwnProperty(input)) continue;
      if (_isLLMHost(_urlFromFetchInput(input))) continue;
      keptFetch[input] = out.fetch[input];
      anyFetch = true;
    }
    if (anyFetch) out.fetch = keptFetch;
    else delete out.fetch;
  }

  if (out.fetchBatch) {
    var keptBatch = {};
    var anyBatch = false;
    for (var input in out.fetchBatch) {
      if (!out.fetchBatch.hasOwnProperty(input)) continue;
      if (_allUrlsLLM(input)) continue;
      keptBatch[input] = out.fetchBatch[input];
      anyBatch = true;
    }
    if (anyBatch) out.fetchBatch = keptBatch;
    else delete out.fetchBatch;
  }

  return out;
}

// ── createLLM() — config-driven tier interface ──────────────────────────────
// Returns an object with a method for every tier defined in config.TIERS.
// Adding a tier to config is all it takes — no llm.js changes needed.
//
// Usage:
//   import { createLLM } from "llm@v1";
//   var llm = createLLM();
//   llm.classify(prompt);
//   llm.codegen(prompt, { cachedPrefix: "..." });
//   llm.myCustomTier(prompt);  // works if config.TIERS.myCustomTier is set
//
// Also provides: llm.embed(), llm.embedBatch(), llm.completeBatch(prompts, tier),
//   llm.buildCompleteFetchArgs(prompt, tier), llm.parseCompleteResult(resp, provider),
//   llm.buildEmbedFetchArgs(texts), llm.parseEmbedResult(resp)
export function createLLM() {
  var config = getConfig();
  var tiers = config.TIERS || {};
  var tierNames = Object.keys(tiers);
  var llm = {};

  for (var i = 0; i < tierNames.length; i++) {
    llm[tierNames[i]] = _wrapTier(tierNames[i]);
  }

  // Expose last resolved provider/model for debug instrumentation.
  // Callers read llm._resolved after an LLM call to see which provider/model was used.
  llm._resolved = _lastResolved;

  // Attach helpers
  llm.parseJSON = parseJSON;
  llm.ask = ask;
  llm.askBatch = askBatch;
  llm.embed = embed;
  llm.embedBatch = embedBatch;
  llm.completeBatch = completeBatch;
  llm.completeBatchDetailed = completeBatchDetailed;
  llm.buildCompleteFetchArgs = buildCompleteFetchArgs;
  llm.parseCompleteResult = parseCompleteResult;
  llm.buildEmbedFetchArgs = buildEmbedFetchArgs;
  llm.parseEmbedResult = parseEmbedResult;

  // chat — multi-turn message API with native tools support.
  // Returns the FULL response body (content blocks, stop_reason, usage, etc.)
  // in Anthropic's shape. Supports providers: claude (native), openrouter
  // (translated from OpenAI shape).
  // tier defaults to "codegen"; opts can override model and other params.
  llm.chat = function(messages, opts) {
    if (ENABLE_LLM_TRACE && typeof __wrapTrace === "function") {
      var wrapped = __wrapTrace("llm.chat", _chatDispatch);
      return wrapped(messages, opts);
    }
    return _chatDispatch(messages, opts);
  };

  return llm;
}

// Shared chat dispatch — tier/provider resolution + provider call, no trace
// wrapper. createLLM().chat wraps this with __wrapTrace; chatUntraced exposes
// it bare.
function _chatDispatch(messages, opts) {
  opts = opts || {};
  var config = getConfig();
  var tier = opts.tier || "codegen";
  var resolved = resolveTier(config, tier);
  var provider = resolved.provider;
  var model = opts.model || resolved.model;
  // opts.provider overrides the tier's provider (e.g. A/B-ing an openrouter
  // model without editing config TIERS). Model falls back to the provider's
  // tier default when not given explicitly.
  if (opts.provider) {
    provider = opts.provider;
    model = opts.model || (MODEL_TIERS[provider] ? MODEL_TIERS[provider][tier] : null);
  }

  _lastResolved.provider = provider;
  _lastResolved.model = model;

  if (provider === "claude") return chatClaude(messages, model, config, opts);
  if (provider === "openrouter") return chatOpenRouter(messages, model, config, opts);
  if (provider === "openai") return chatOpenAI(messages, model, config, opts);
  throw new Error("llm.chat() supports providers: claude, openai, openrouter. Got: " + provider);
}

// chatUntraced — llm.chat WITHOUT the __wrapTrace wrapper, for callers that
// run their own inner LLM loops and must not flood the calling cell's
// Effects digest with one trace entry per inner turn (search@v1's RLM root
// loop is the canonical consumer; see docs/12-rlm-search.md). The underlying
// provider fetches are still recorded as fetch/fetchBatch effects, but this
// module's __prepareTraces drops LLM-host fetches from the digest — so an
// untraced caller's LLM traffic is fully invisible in tool results while
// remaining visible to provider billing and the runtime's raw trace files.
export function chatUntraced(messages, opts) {
  return _chatDispatch(messages, opts);
}

// ── parseJSON — strip markdown fences and parse JSON from LLM responses ──────
// LLMs often wrap JSON in ```json ... ``` fences. This strips them and parses.
// Returns parsed object, or null on failure.
export function parseJSON(resp) {
  if (!resp) return null;
  var cleaned = resp.replace(/^\`\`\`json\s*/m, "").replace(/\s*\`\`\`$/m, "").trim();
  try {
    return JSON.parse(cleaned);
  } catch (e) {
    return null;
  }
}

// ── Build/parse helpers for fetchBatch parallelism ──────────────────────────
// These let callers combine completion + embedding calls into a single fetchBatch.

// Build [url, fetchOpts] for a completion call (usable with fetchBatch)
// Returns [url, fetchOpts, provider] — provider needed for parseCompleteResult
export function buildCompleteFetchArgs(prompt, tier) {
  var config = getConfig();
  var resolved = resolveTier(config, tier);
  var args = _buildFetchArgs(resolved.provider, prompt, resolved.model, config);
  if (args) args.push(resolved.provider);
  return args;
}

// Parse a fetchBatch response from a completion call → text string
// provider: pass the provider from buildCompleteFetchArgs, or omit to resolve from config
export function parseCompleteResult(response, provider) {
  if (!provider) {
    var config = getConfig();
    var resolved = resolveTier(config, null);
    provider = resolved.provider;
  }
  return _extractResponse(provider, response);
}

// Build [url, fetchOpts] for an embedding call (usable with fetchBatch)
export function buildEmbedFetchArgs(texts) {
  var config = getConfig();
  var apiKey = config.OPENAI_API_KEY;
  if (!apiKey) throw new Error("OPENAI_API_KEY not set in config@v1");
  var model = config.EMBED_MODEL || "text-embedding-3-small";
  return ["https://api.openai.com/v1/embeddings", {
    method: "POST",
    headers: {
      "Authorization": "Bearer " + apiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({ model: model, input: texts })
  }];
}

// Parse a fetchBatch response from an embedding call → array of vectors
export function parseEmbedResult(response) {
  if (!response || !response.ok) return [];
  var data = response.body && response.body.data;
  if (!data) return [];
  var sorted = new Array(data.length);
  for (var i = 0; i < data.length; i++) {
    sorted[data[i].index] = data[i].embedding;
  }
  return sorted;
}
