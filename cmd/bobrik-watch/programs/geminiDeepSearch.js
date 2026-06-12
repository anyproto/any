// __main_source
// Gemini Deep Search — grounded research via Gemini API with Google Search.
// Phase 1: Initial search → Phase 2: Decompose follow-ups → Phase 3: Parallel follow-up searches
// Creates interlinked Anytype objects: collection, overview page, sub-pages, bookmarks.

import { main as getConfig } from "config@v1";
import { createClient } from "anyHelper@v1";
import { createLLM, parseJSON } from "llm@v1";

var GEMINI_MODEL = "gemini-2.5-flash";
var GEMINI_BASE = "https://generativelanguage.googleapis.com/v1beta/models/";
var MAX_FOLLOW_UPS = 7;

// ── Guarded chatReply — works inside assistant runtime, no-ops standalone ──

function reply(msg) {
  // done:false — these are mid-turn progress updates; the run's terminal
  // chatReply (with done:true) comes from the kernel loop, not from here.
  if (typeof chatReply === "function") chatReply({ text: msg, done: false });
}

// ── Gemini API helpers ──────────────────────────────────────────────────────

function buildGeminiFetchArgs(geminiKey, model, systemPrompt, userPrompt) {
  var url = GEMINI_BASE + model + ":generateContent";
  return [url, {
    method: "POST",
    headers: {
      "x-goog-api-key": geminiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      system_instruction: { parts: [{ text: systemPrompt }] },
      contents: [{ role: "user", parts: [{ text: userPrompt }] }],
      tools: [{ google_search: {} }]
    })
  }];
}

function parseGeminiResponse(resp) {
  if (!resp || !resp.ok) {
    var errBody = resp && resp.body;
    var errMsg = (errBody && errBody.error && errBody.error.message)
      ? errBody.error.message
      : "HTTP " + (resp ? resp.status : "unknown");
    return { ok: false, error: errMsg };
  }

  var data = resp.body;
  var candidate = data.candidates && data.candidates[0];
  if (!candidate || !candidate.content || !candidate.content.parts) {
    return { ok: false, error: "empty response from Gemini" };
  }

  var answer = "";
  var parts = candidate.content.parts;
  for (var i = 0; i < parts.length; i++) {
    if (parts[i].text) answer += parts[i].text;
  }

  var sources = [];
  var gm = candidate.groundingMetadata;
  if (gm && gm.groundingChunks) {
    var seenDomain = {};
    for (var ci = 0; ci < gm.groundingChunks.length; ci++) {
      var chunk = gm.groundingChunks[ci];
      if (chunk.web) {
        var domain = chunk.web.domain || chunk.web.title || "";
        if (!seenDomain[domain]) {
          seenDomain[domain] = true;
          sources.push({
            url: chunk.web.uri || "",
            title: chunk.web.title || domain,
            domain: domain
          });
        }
      }
    }
  }

  var searchQueries = (gm && gm.webSearchQueries) ? gm.webSearchQueries : [];
  var usage = data.usageMetadata || {};

  return { ok: true, answer: answer, sources: sources, searchQueries: searchQueries, usage: usage };
}

// ── Extract <title> from HTML response ───────────────────────────────────────

function extractTitle(resp) {
  if (!resp || !resp.ok) return null;
  var body = resp.body;
  if (typeof body !== "string") return null;
  var match = body.match(/<title[^>]*>([^<]+)<\/title>/i);
  if (!match || !match[1]) return null;
  var title = match[1].trim();
  return title.length > 0 && title.length < 200 ? title : null;
}

// ── Follow-up decomposition via LLM classify ────────────────────────────────

function decomposeFollowUps(answer, question, llm) {
  var prompt = "You are analyzing the results of an initial web research.\n\n"
    + "Original question: " + question + "\n\n"
    + "Initial answer (summary):\n" + answer.substring(0, 2000) + "\n\n"
    + "Tasks:\n"
    + "1. Generate 3-7 follow-up questions that would deepen this research. "
    + "Use fewer (3-4) for narrow topics, more (5-7) for broad ones. "
    + "Each should explore a specific aspect, fill a gap, or go deeper into something the initial answer only touched on. "
    + "Make them concrete and searchable.\n"
    + "2. Generate a short, descriptive collection name (3-6 words) for this research.\n\n"
    + "Respond in JSON only:\n"
    + '{"followUps": ["question1", "question2", ...], "collectionName": "Short Name"}';

  var result = llm.classify(prompt);
  var parsed = parseJSON(result);
  if (!parsed || !parsed.followUps || !Array.isArray(parsed.followUps)) {
    return null;
  }
  return {
    followUps: parsed.followUps.slice(0, MAX_FOLLOW_UPS),
    collectionName: parsed.collectionName || "Research: " + question.substring(0, 40)
  };
}

// ── System/user prompts ─────────────────────────────────────────────────────

function buildSystemPrompt() {
  return "You are a thorough research assistant. Your task is to research the given question using Google Search grounding and provide a comprehensive, well-structured answer.\n\n"
    + "Guidelines:\n"
    + "- Search broadly — use multiple angles and phrasings\n"
    + "- Cite specific facts with the sources you find\n"
    + "- Structure the answer with clear headings (## sections)\n"
    + "- Include concrete details: numbers, dates, names, comparisons\n"
    + "- If sources conflict, note the disagreement\n"
    + "- End with a brief summary of key findings";
}

function buildUserPrompt(question) {
  return "Research this question thoroughly:\n\n" + question + "\n\n"
    + "Search from multiple angles to build a complete picture. "
    + "Provide a detailed, well-organized answer with clear section headings.";
}

// ── Main research function ──────────────────────────────────────────────────

export function research(question, opts) {
  if (!question || typeof question !== "string") {
    return { ok: false, error: "question is required" };
  }

  var config = getConfig();
  var geminiKey = config.GEMINI_API_KEY;
  if (!geminiKey) {
    return { ok: false, error: "GEMINI_API_KEY not set in config@v1" };
  }

  var model = (opts && opts.model) || GEMINI_MODEL;
  var systemPrompt = (opts && opts.systemPrompt) || buildSystemPrompt();
  var startMs = Date.now();

  // ── Phase 1: Initial Gemini grounded search ─────────────────────────────

  reply("Researching: " + question.substring(0, 100) + "...");

  var initialArgs = buildGeminiFetchArgs(geminiKey, model, systemPrompt, buildUserPrompt(question));
  var initialResp = fetch(initialArgs[0], initialArgs[1]);
  var initial = parseGeminiResponse(initialResp);

  if (!initial.ok) {
    return { ok: false, error: "Initial search failed: " + initial.error };
  }

  var phase1Ms = Date.now() - startMs;

  // ── Phase 2: Decompose into follow-ups ──────────────────────────────────

  reply("Initial research done, clarifying follow-ups...");

  var llm = createLLM();
  var decomp = decomposeFollowUps(initial.answer, question, llm);

  if (!decomp || decomp.followUps.length === 0) {
    // Fallback: single page, no follow-ups
    reply("Could not decompose — creating single research page.");
    return createSinglePageResult(question, initial, model, Date.now() - startMs);
  }

  var collectionName = decomp.collectionName;
  var followUps = decomp.followUps;
  var phase2Ms = Date.now() - startMs - phase1Ms;

  // ── Phase 3: Parallel follow-up Gemini calls ───────────────────────────

  reply("Researching " + followUps.length + " follow-up topics...");

  var followUpFetchArgs = [];
  for (var fi = 0; fi < followUps.length; fi++) {
    followUpFetchArgs.push(buildGeminiFetchArgs(
      geminiKey, model, systemPrompt, buildUserPrompt(followUps[fi])
    ));
  }

  var followUpResponses = fetchBatch(followUpFetchArgs);
  var followUpResults = [];
  var allSources = [];

  // Collect initial sources
  for (var isi = 0; isi < initial.sources.length; isi++) {
    allSources.push(initial.sources[isi]);
  }

  for (var fri = 0; fri < followUpResponses.length; fri++) {
    var parsed = parseGeminiResponse(followUpResponses[fri]);
    if (parsed.ok) {
      followUpResults.push({
        question: followUps[fri],
        answer: parsed.answer,
        sources: parsed.sources,
        searchQueries: parsed.searchQueries
      });
      for (var fsi = 0; fsi < parsed.sources.length; fsi++) {
        allSources.push(parsed.sources[fsi]);
      }
    }
  }

  var phase3Ms = Date.now() - startMs - phase1Ms - phase2Ms;

  // ── Phase 4: Create Anytype objects ─────────────────────────────────────

  reply("Creating research pages...");

  var client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID
  });

  // Deduplicate all sources by domain
  var seenDomain = {};
  var dedupSources = [];
  for (var dsi = 0; dsi < allSources.length; dsi++) {
    var s = allSources[dsi];
    var key = s.domain || s.url;
    if (!seenDomain[key]) {
      seenDomain[key] = true;
      dedupSources.push(s);
    }
  }

  // Fetch real page titles in parallel via fetchBatch
  var titleFetchArgs = [];
  for (var tfi = 0; tfi < dedupSources.length; tfi++) {
    titleFetchArgs.push([dedupSources[tfi].url, { method: "GET" }]);
  }
  var titleResponses = fetchBatch(titleFetchArgs);
  for (var tri = 0; tri < titleResponses.length; tri++) {
    var pageTitle = extractTitle(titleResponses[tri]);
    if (pageTitle) {
      dedupSources[tri].title = pageTitle;
    }
  }

  // Create bookmarks
  var bookmarkResults = [];
  for (var bi = 0; bi < dedupSources.length; bi++) {
    var src = dedupSources[bi];
    var bm = client.createObject("bookmark", {
      name: src.title || src.domain,
      bookmark: { source: src.url }
    });
    if (bm && bm.ok) {
      bookmarkResults.push({ id: bm.id, name: src.title || src.domain, url: src.url });
    }
  }

  // Create sub-pages (one per follow-up result)
  var subPageResults = [];
  for (var spi = 0; spi < followUpResults.length; spi++) {
    var fur = followUpResults[spi];
    var subMd = fur.answer;
    if (fur.sources.length > 0) {
      subMd += "\n\n---\n\n## Sources\n\n";
      for (var ssi = 0; ssi < fur.sources.length; ssi++) {
        subMd += "- [" + (fur.sources[ssi].title || fur.sources[ssi].domain) + "](" + fur.sources[ssi].url + ")\n";
      }
    }
    var sp = client.createObject("pages", {
      name: fur.question,
      body: subMd
    });
    if (sp && sp.ok) {
      subPageResults.push({ id: sp.id, name: fur.question });
    }
  }

  // Create overview page with links to sub-pages and bookmarks
  var overviewMd = initial.answer;

  if (subPageResults.length > 0) {
    overviewMd += "\n\n---\n\n## Follow-up Topics\n\n";
    for (var lpi = 0; lpi < subPageResults.length; lpi++) {
      overviewMd += "- [" + subPageResults[lpi].name + "](any://" + (client.config.spaceId || "_") + "/" + subPageResults[lpi].id + ")\n";
    }
  }

  if (bookmarkResults.length > 0) {
    overviewMd += "\n## Sources\n\n";
    for (var lbi = 0; lbi < bookmarkResults.length; lbi++) {
      overviewMd += "- [" + bookmarkResults[lbi].name + "](any://" + (client.config.spaceId || "_") + "/" + bookmarkResults[lbi].id + ")\n";
    }
  }

  var totalMs = Date.now() - startMs;
  overviewMd += "\n---\n\n*Gemini grounded search (" + model + ") | "
    + "Phase 1: " + (phase1Ms / 1000).toFixed(1) + "s, "
    + "Phase 2: " + (phase2Ms / 1000).toFixed(1) + "s, "
    + "Phase 3: " + (phase3Ms / 1000).toFixed(1) + "s | "
    + "Total: " + (totalMs / 1000).toFixed(1) + "s*";

  var overviewPage = client.createObject("pages", {
    name: collectionName,
    body: overviewMd
  });

  if (!overviewPage || !overviewPage.ok) {
    return {
      ok: false,
      error: "Failed to create overview page: " + (overviewPage ? overviewPage.error : "unknown"),
      answer: initial.answer,
      sources: dedupSources
    };
  }

  // Create collection and add all objects
  var col = client.createCollection(collectionName, "🔬");

  if (!col || !col.ok) {
    // Collection failed but we still have the overview page
    return {
      ok: true,
      overviewPageId: overviewPage.id,
      overviewPageName: overviewPage.object.name,
      pageId: overviewPage.id,
      pageName: overviewPage.object.name,
      subPages: subPageResults,
      bookmarks: bookmarkResults,
      answer: initial.answer,
      sources: dedupSources,
      timing: { totalMs: totalMs, phase1Ms: phase1Ms, phase2Ms: phase2Ms, phase3Ms: phase3Ms },
      usage: initial.usage
    };
  }

  var allIds = [overviewPage.id];
  for (var ci = 0; ci < subPageResults.length; ci++) allIds.push(subPageResults[ci].id);
  for (var abi = 0; abi < bookmarkResults.length; abi++) allIds.push(bookmarkResults[abi].id);
  client.addToCollection(col.id, allIds);

  return {
    ok: true,
    collectionId: col.id,
    collectionName: col.object.name,
    overviewPageId: overviewPage.id,
    overviewPageName: overviewPage.object.name,
    subPages: subPageResults,
    bookmarks: bookmarkResults,
    // backward compat
    pageId: overviewPage.id,
    pageName: overviewPage.object.name,
    answer: initial.answer,
    sources: dedupSources,
    searchQueries: initial.searchQueries,
    timing: { totalMs: totalMs, phase1Ms: phase1Ms, phase2Ms: phase2Ms, phase3Ms: phase3Ms },
    usage: initial.usage
  };
}

// ── Fallback: single page (no follow-ups) ───────────────────────────────────

function createSinglePageResult(question, initial, model, elapsedMs) {
  var md = initial.answer;

  if (initial.sources.length > 0) {
    md += "\n\n---\n\n## Sources\n\n";
    for (var si = 0; si < initial.sources.length; si++) {
      var src = initial.sources[si];
      md += "- [" + (src.title || src.domain) + "](" + src.url + ")\n";
    }
  }

  md += "\n---\n\n*Gemini grounded search (" + model + ") | Time: " + (elapsedMs / 1000).toFixed(1) + "s*";

  var client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID
  });

  var page = client.createObject("pages", {
    name: "Research: " + question.substring(0, 80),
    body: md
  });

  if (!page || !page.ok) {
    return {
      ok: false,
      error: "Failed to create page: " + (page ? page.error : "unknown"),
      answer: initial.answer,
      sources: initial.sources
    };
  }

  return {
    ok: true,
    pageId: page.id,
    pageName: page.object.name,
    overviewPageId: page.id,
    overviewPageName: page.object.name,
    answer: initial.answer,
    sources: initial.sources,
    searchQueries: initial.searchQueries,
    timing: { totalMs: elapsedMs },
    usage: initial.usage
  };
}

export function main(args) {
  if (args && args.question) {
    return research(args.question, args);
  }
  return { ok: false, error: "question arg required" };
}
