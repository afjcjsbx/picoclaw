import { describe, expect, it } from "vitest"

import type { ChatMessage } from "@/store/chat"

import { mergeHistoryMessages } from "./history"

function message(overrides: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: "message-1",
    role: "assistant",
    content: "hello",
    timestamp: 1_700_000_000_000,
    ...overrides,
  }
}

describe("mergeHistoryMessages", () => {
  it("returns an empty list when both inputs are empty", () => {
    expect(mergeHistoryMessages([], [])).toEqual([])
  })

  it("prepends history messages and keeps current ones", () => {
    const history = [
      message({ id: "hist-1", content: "old", timestamp: 1_600_000_000_000 }),
    ]
    const current = [
      message({ id: "local-1", content: "new", timestamp: 1_700_000_000_000 }),
    ]

    expect(
      mergeHistoryMessages(history, current).map((item) => item.id),
    ).toEqual(["hist-1", "local-1"])
  })

  it("drops history entries whose id already exists locally", () => {
    const history = [message({ id: "same", content: "from history" })]
    const current = [message({ id: "same", content: "from current" })]

    expect(mergeHistoryMessages(history, current)).toEqual(current)
  })

  it("drops history entries that match a local message signature", () => {
    const history = [message({ id: "hist-1" })]
    const current = [message({ id: "local-1" })]

    expect(mergeHistoryMessages(history, current)).toEqual(current)
  })

  it("keeps messages whose attachments differ", () => {
    const history = [
      message({
        id: "hist-1",
        attachments: [{ type: "image", url: "a.png", filename: "a.png" }],
      }),
    ]
    const current = [
      message({
        id: "local-1",
        attachments: [{ type: "image", url: "a.png", filename: "b.png" }],
      }),
    ]

    expect(mergeHistoryMessages(history, current)).toHaveLength(2)
  })

  it("sorts by normalized timestamp across seconds, milliseconds and ISO strings", () => {
    const history = [
      message({ id: "seconds", content: "seconds", timestamp: 1_700_000_000 }),
      message({
        id: "iso",
        content: "iso",
        timestamp: "2023-11-14T22:13:21.000Z",
      }),
    ]
    const current = [
      message({
        id: "millis",
        content: "millis",
        timestamp: 1_699_999_999_000,
      }),
    ]

    expect(
      mergeHistoryMessages(history, current).map((item) => item.id),
    ).toEqual(["millis", "seconds", "iso"])
  })

  it("treats unparseable timestamps as the earliest messages", () => {
    const history = [message({ id: "invalid", timestamp: "not-a-date" })]
    const current = [message({ id: "valid", timestamp: 1_700_000_000_000 })]

    expect(
      mergeHistoryMessages(history, current).map((item) => item.id),
    ).toEqual(["invalid", "valid"])
  })
})
