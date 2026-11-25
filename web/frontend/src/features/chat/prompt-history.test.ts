import { describe, expect, it } from "vitest"

import type { ChatMessage } from "@/store/chat"

import { getPromptHistory } from "./prompt-history"

function message(role: ChatMessage["role"], content: string): ChatMessage {
  return {
    id: `${role}-${content}`,
    role,
    content,
    timestamp: 1,
  }
}

describe("getPromptHistory", () => {
  it("returns non-empty user prompts from newest to oldest", () => {
    const messages = [
      message("user", "first"),
      message("assistant", "answer"),
      message("user", "  preserve spacing  "),
      message("user", "   "),
      message("assistant", "latest answer"),
    ]

    expect(getPromptHistory(messages)).toEqual([
      "  preserve spacing  ",
      "first",
    ])
  })

  it("collapses only consecutive duplicate prompts", () => {
    const messages = [
      message("user", "repeat"),
      message("user", "repeat"),
      message("assistant", "answer"),
      message("user", "different"),
      message("user", "repeat"),
    ]

    expect(getPromptHistory(messages)).toEqual([
      "repeat",
      "different",
      "repeat",
    ])
  })

  it("returns an empty history when there are no text prompts", () => {
    expect(
      getPromptHistory([
        message("assistant", "answer"),
        { ...message("user", ""), attachments: [] },
      ]),
    ).toEqual([])
  })
})
