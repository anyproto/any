// __main_source
// Config program — set your API keys here



export function main() {
  return {
    CLAUDE_API_KEY: "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",  // paste your Claude API key here
    OPENAI_API_KEY: "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",
    TOGETHER_AI_API_KEY: "ef3cdfa662b8f8043f05d6d70c4198ae60643064e347d430ec22b5aef6f4252f",
    GEMINI_API_KEY: "AQ.Ab8RN6J-qznxzQ-FVKn7foP_VtKSAoRG3S1AKEPJZWHzNdQRnw",
    OPENROUTER_API_KEY: "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",
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
