// __main_source
// assistant@v5 — barebones CodeAct agent for Anytype CRUD via QA testing.
//
// PURPOSE: a clean baseline agent for testing Anytype CRUD operations through
// natural language. No chat history, no persistent memory, no plan/replan, no
// debug pages, no goal/phase abstraction. Each invocation is fully independent.
//
// ARCHITECTURE: single tool exposed to the model — `run_cell(code)`. Each turn
// the model emits one or more tool_use blocks; the harness translates each to
// a js.eval call against the persistent kernel and returns lastValue + traces
// as a tool_result block. Conversation history accumulates as native Anthropic
// messages, so the model sees its own prior code (in tool_use blocks) and
// prior results (in tool_result blocks) without manual step-log rendering.
//
// TERMINATION: Anthropic's stop_reason === "end_turn" — when the model is done,
// it emits an assistant message containing only text (no tool_use). The agent
// loop checks stop_reason and returns the text as the final answer.
//
// This file is a thin wrapper that re-exports the toolcall_core@v1 main loop,
// renamed for distribution as the assistant@v5 program. All behavior is
// identical to toolcall_core@v1; the rename exists so QA testers see a clean
// "assistant" entry point instead of the research-tagged "toolcall_core".
//
// Run:
//   anytype-agent-runtime -m systemjs assistantjs/assistant@v5.js text="..."
//
// or via the deployed program in any space:
//   client.runProgram("assistant", { text: "..." }, "v5")

import { main as toolcallMain } from "toolcall_core@v1";

export function main(args) {
  return toolcallMain(args);
}
