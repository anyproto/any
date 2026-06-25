// __main_source
// Config program — set your API keys here



export function main() {
  return {
    CLAUDE_API_KEY: "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",  // paste your Claude API key here
    OPENAI_API_KEY: "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",
    TOGETHER_AI_API_KEY: "ef3cdfa662b8f8043f05d6d70c4198ae60643064e347d430ec22b5aef6f4252f",
    GEMINI_API_KEY: "AQ.Ab8RN6J-qznxzQ-FVKn7foP_VtKSAoRG3S1AKEPJZWHzNdQRnw",
    OPENROUTER_API_KEY: "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",

    // --- Integration connector credentials (Pattern 1: personal tokens) ---
    // Each connector reads its key here; if empty, the tool tells you where to
    // create one and the agent can write it back into this program via
    // anyPrograms.editProgram. See dev space → Integration Docs for setup.
    LINEAR_API_KEY: "",        // linear@v1   — https://linear.app/settings/api (personal API key)
    GITHUB_TOKEN: "",          // github@v1   — https://github.com/settings/tokens?type=beta (fine-grained PAT)
    ATTIO_API_TOKEN: "",       // attio@v1    — Workspace settings → Developers (grant read scopes!)
    INTERCOM_ACCESS_TOKEN: "", // intercom@v1 — Developer Hub app → Configure → Authentication
    FIGMA_TOKEN: "",           // figma@v1    — Settings → Security → Personal access tokens (read scopes)
    GRANOLA_API_KEY: "",       // granola@v1  — grn_… key (Business/Enterprise plans only)

    // --- Google OAuth (Pattern 2: one shared client for gmail/sheets/calendar/drive) ---
    // App credentials from Google Cloud Console → APIs & Services → Credentials
    // (OAuth client, type "Desktop app"). The per-account refresh token is
    // obtained at runtime via the oauthFlow host fn and cached by googleAuth@v1.
    // tolya: my test desktop app account credentials:
    GOOGLE_OAUTH_CLIENT_ID: "519847309896-if7o0qblp6dr4d5elr1ibuhkge8c2mc3.apps.googleusercontent.com",
    GOOGLE_OAUTH_CLIENT_SECRET: "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",
    GOOGLE_OAUTH_REFRESH_TOKEN: "", // filled by googleAuth@v1 after first consent

    // Per-tier routing: "provider/model" pins a specific provider+model
    // "provider" alone uses the default model from MODEL_TIERS in llm.js
    TIERS: {
      // codegen — the toolcaller agent's main loop (llm.chat default tier).
      // classify — one-off structured judgments (geminiDeepSearch).
      classify:  "claude/claude-sonnet-4-6",
      codegen:   "claude/claude-sonnet-4-6",
      // search@v1 tiers (docs/12-rlm-search.md). The orchestrator runs the
      // RLM root loop (coverage judgment, when-to-stop — the capability that
      // matters); search_classify scores snippet batches (model-insensitive).
      // Eval (2026-06-07): glm-5.1 root was best on every axis; haiku-class
      // maps lose nothing. gemini-2.5-flash classify is cheaper/faster still.
      search_orchestrator: "openrouter/z-ai/glm-5.1",
      search_classify:     "openrouter/google/gemini-2.5-flash"
    }
  };
}
