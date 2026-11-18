import { describe, expect, it } from "vitest"

import {
  parseAssistantMessageCreateState,
  parseAssistantMessageUpdateState,
} from "./assistant-message-state"

const TOOL_CALL = {
  id: "call-1",
  type: "function",
  function: { name: "exec", arguments: "{}" },
}

describe("parseAssistantMessageCreateState", () => {
  it("treats plain text as a normal message", () => {
    expect(parseAssistantMessageCreateState({ content: "hello" })).toEqual({
      content: "hello",
      kind: "normal",
      toolCalls: undefined,
    })
  })

  it("falls back to an empty content string", () => {
    expect(parseAssistantMessageCreateState({})).toEqual({
      content: "",
      kind: "normal",
      toolCalls: undefined,
    })
  })

  it("infers tool_calls from a tool_calls payload", () => {
    expect(
      parseAssistantMessageCreateState({
        content: "running",
        tool_calls: [TOOL_CALL],
      }),
    ).toEqual({
      content: "running",
      kind: "tool_calls",
      toolCalls: [TOOL_CALL],
    })
  })

  it("normalizes an explicit kind", () => {
    expect(
      parseAssistantMessageCreateState({ content: "hmm", kind: " THOUGHT " }),
    ).toEqual({
      content: "hmm",
      kind: "thought",
      toolCalls: undefined,
    })
  })

  it("keeps tool_feedback as an explicit kind", () => {
    expect(
      parseAssistantMessageCreateState({
        content: "result",
        kind: "tool_feedback",
      }),
    ).toEqual({
      content: "result",
      kind: "tool_feedback",
      toolCalls: undefined,
    })
  })

  it("parses legacy tool feedback content", () => {
    const parsed = parseAssistantMessageCreateState({
      content: "🔧 `exec`\n```json\n{}\n```",
    })

    expect(parsed.kind).toBe("tool_calls")
    expect(parsed.toolCalls).toEqual([
      {
        type: "function",
        function: { name: "exec", arguments: "{}" },
      },
    ])
  })
})

describe("parseAssistantMessageUpdateState", () => {
  it("uses the explicit kind and drops tool calls for non tool kinds", () => {
    expect(
      parseAssistantMessageUpdateState({
        content: "done",
        kind: "normal",
      }),
    ).toEqual({ content: "done", kind: "normal", toolCalls: undefined })
  })

  it("keeps tool calls for an explicit tool_calls kind", () => {
    expect(
      parseAssistantMessageUpdateState({
        content: "running",
        kind: "tool_calls",
        tool_calls: [TOOL_CALL],
      }),
    ).toEqual({
      content: "running",
      kind: "tool_calls",
      toolCalls: [TOOL_CALL],
    })
  })

  it("infers tool_calls when a payload arrives without a kind", () => {
    expect(
      parseAssistantMessageUpdateState({
        content: "running",
        tool_calls: [TOOL_CALL],
      }),
    ).toEqual({
      content: "running",
      kind: "tool_calls",
      toolCalls: [TOOL_CALL],
    })
  })

  it("resets a thought message to normal when the update has no details", () => {
    expect(
      parseAssistantMessageUpdateState(
        { content: "answer" },
        { kind: "thought", toolCalls: undefined },
      ),
    ).toEqual({ content: "answer", kind: "normal", toolCalls: undefined })
  })

  it("preserves a normal kind while clearing stale tool calls", () => {
    expect(
      parseAssistantMessageUpdateState(
        { content: "final" },
        { kind: "normal", toolCalls: [TOOL_CALL] },
      ),
    ).toEqual({ content: "final", kind: "normal", toolCalls: undefined })
  })

  it("keeps the existing kind when nothing else is known", () => {
    expect(
      parseAssistantMessageUpdateState(
        { content: "more" },
        { kind: "tool_feedback", toolCalls: undefined },
      ),
    ).toEqual({ content: "more", kind: "tool_feedback", toolCalls: undefined })
    expect(parseAssistantMessageUpdateState({ content: "more" })).toEqual({
      content: "more",
      kind: "normal",
      toolCalls: undefined,
    })
  })
})
