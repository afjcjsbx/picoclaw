import { describe, expect, it } from "vitest"

import {
  parseToolCallsFromContent,
  parseToolCallsValue,
  toolCallsSignature,
} from "./tool-calls"

describe("parseToolCallsValue", () => {
  it("ignores values that are not arrays", () => {
    expect(parseToolCallsValue(undefined)).toBeUndefined()
    expect(parseToolCallsValue("[]")).toBeUndefined()
    expect(parseToolCallsValue({})).toBeUndefined()
  })

  it("drops entries that carry no usable fields", () => {
    expect(parseToolCallsValue([null, 42, "text", {}])).toBeUndefined()
    expect(parseToolCallsValue([{ function: { name: "" } }])).toBeUndefined()
  })

  it("keeps id, type, function and extra content fields", () => {
    const toolCalls = parseToolCallsValue([
      {
        id: "call-1",
        type: "function",
        function: { name: "exec", arguments: '{"command":"ls"}' },
        extra_content: { tool_feedback_explanation: "listing files" },
      },
    ])

    expect(toolCalls).toEqual([
      {
        id: "call-1",
        type: "function",
        function: { name: "exec", arguments: '{"command":"ls"}' },
        extraContent: { toolFeedbackExplanation: "listing files" },
      },
    ])
  })

  it("keeps a function that only carries arguments", () => {
    expect(parseToolCallsValue([{ function: { arguments: "{}" } }])).toEqual([
      { function: { arguments: "{}" } },
    ])
  })

  it("ignores non-string extra content explanations", () => {
    expect(
      parseToolCallsValue([
        { id: "call-1", extra_content: { tool_feedback_explanation: 7 } },
      ]),
    ).toEqual([{ id: "call-1" }])
  })
})

describe("parseToolCallsFromContent", () => {
  it("parses legacy tool feedback blocks with arguments and explanation", () => {
    const content = [
      "🔧 `exec`",
      "Listing the workspace",
      "```json",
      '{"command":"ls"}',
      "```",
    ].join("\n")

    expect(parseToolCallsFromContent(content)).toEqual([
      {
        type: "function",
        function: { name: "exec", arguments: '{"command":"ls"}' },
        extraContent: { toolFeedbackExplanation: "Listing the workspace" },
      },
    ])
  })

  it("keeps the tool name when no code fence is present", () => {
    expect(
      parseToolCallsFromContent("🔧 `web_search`\nSearch the web"),
    ).toEqual([
      {
        type: "function",
        function: { name: "web_search" },
        extraContent: { toolFeedbackExplanation: "Search the web" },
      },
    ])
  })

  it("returns undefined for regular assistant text", () => {
    expect(parseToolCallsFromContent("Just a normal answer")).toBeUndefined()
    expect(parseToolCallsFromContent("")).toBeUndefined()
  })
})

describe("toolCallsSignature", () => {
  it("returns an empty string when there are no tool calls", () => {
    expect(toolCallsSignature()).toBe("")
    expect(toolCallsSignature([])).toBe("")
  })

  it("is stable for structurally equal tool calls", () => {
    const toolCalls = [
      {
        id: "call-1",
        function: { name: "exec", arguments: "{}" },
      },
    ]

    expect(toolCallsSignature(toolCalls)).toBe(toolCallsSignature(toolCalls))
    expect(toolCallsSignature(toolCalls)).toBe(
      toolCallsSignature([
        { id: "call-1", function: { name: "exec", arguments: "{}" } },
      ]),
    )
  })

  it("changes when any tracked field changes", () => {
    const base = [{ id: "call-1", function: { name: "exec" } }]

    expect(
      toolCallsSignature([{ id: "call-2", function: { name: "exec" } }]),
    ).not.toBe(toolCallsSignature(base))
    expect(
      toolCallsSignature([{ id: "call-1", function: { name: "shell" } }]),
    ).not.toBe(toolCallsSignature(base))
    expect(
      toolCallsSignature([
        {
          id: "call-1",
          function: { name: "exec" },
          extraContent: { toolFeedbackExplanation: "why" },
        },
      ]),
    ).not.toBe(toolCallsSignature(base))
  })
})
