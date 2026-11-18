import { toast } from "sonner"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { getChatState, updateChatStore } from "@/store/chat"

import { handlePicoMessage } from "./protocol"

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }))

const SESSION = "session-1"

beforeEach(() => {
  updateChatStore({
    messages: [],
    isTyping: false,
    contextUsage: undefined,
    activeSessionId: SESSION,
  })
  vi.mocked(toast.error).mockClear()
})

describe("handlePicoMessage session filtering", () => {
  it("ignores messages addressed to another session", () => {
    handlePicoMessage(
      { type: "typing.start", session_id: "another-session" },
      SESSION,
    )

    expect(getChatState().isTyping).toBe(false)
  })

  it("accepts messages without a session id", () => {
    handlePicoMessage({ type: "typing.start" }, SESSION)

    expect(getChatState().isTyping).toBe(true)
  })
})

describe("handlePicoMessage message.create", () => {
  it("appends an assistant message with attachments and context usage", () => {
    handlePicoMessage(
      {
        type: "message.create",
        session_id: SESSION,
        timestamp: 1_700_000_000,
        payload: {
          message_id: "m1",
          content: "hello",
          model_name: "  gpt-4  ",
          attachments: [
            {
              type: "image",
              url: "https://example.test/a.png",
              filename: "a.png",
              content_type: "image/png",
            },
            { type: "image", url: "" },
          ],
          context_usage: {
            used_tokens: 10,
            total_tokens: 100,
            history_tokens: 5,
            compress_at_tokens: 80,
            summarize_at_tokens: 60,
            used_percent: 10,
          },
        },
      },
      SESSION,
    )

    const state = getChatState()
    expect(state.messages).toEqual([
      {
        id: "m1",
        role: "assistant",
        content: "hello",
        kind: "normal",
        modelName: "gpt-4",
        attachments: [
          {
            type: "image",
            url: "https://example.test/a.png",
            filename: "a.png",
            contentType: "image/png",
          },
        ],
        timestamp: 1_700_000_000_000,
      },
    ])
    expect(state.isTyping).toBe(false)
    expect(state.contextUsage).toEqual({
      used_tokens: 10,
      total_tokens: 100,
      history_tokens: 5,
      compress_at_tokens: 80,
      summarize_at_tokens: 60,
      used_percent: 10,
    })
  })

  it("records tool calls from the payload", () => {
    handlePicoMessage(
      {
        type: "message.create",
        session_id: SESSION,
        timestamp: 1_700_000_000_000,
        payload: {
          message_id: "m1",
          content: "running",
          tool_calls: [
            {
              id: "call-1",
              type: "function",
              function: { name: "exec", arguments: "{}" },
            },
          ],
        },
      },
      SESSION,
    )

    expect(getChatState().messages[0]).toMatchObject({
      kind: "tool_calls",
      toolCalls: [
        {
          id: "call-1",
          type: "function",
          function: { name: "exec", arguments: "{}" },
        },
      ],
    })
  })

  it("keeps typing active for placeholders", () => {
    updateChatStore({ isTyping: true })

    handlePicoMessage(
      {
        type: "message.create",
        session_id: SESSION,
        payload: { message_id: "m1", content: "", placeholder: true },
      },
      SESSION,
    )

    expect(getChatState().messages).toHaveLength(1)
    expect(getChatState().isTyping).toBe(true)
  })

  it("stops typing for media.create", () => {
    updateChatStore({ isTyping: true })

    handlePicoMessage(
      {
        type: "media.create",
        session_id: SESSION,
        payload: { message_id: "m1", content: "", kind: "thought" },
      },
      SESSION,
    )

    expect(getChatState().isTyping).toBe(false)
  })

  it("normalizes string second timestamps", () => {
    handlePicoMessage(
      {
        type: "message.create",
        session_id: SESSION,
        timestamp: "1700000000",
        payload: { message_id: "m1", content: "hi" },
      },
      SESSION,
    )

    expect(getChatState().messages[0].timestamp).toBe(1_700_000_000_000)
  })

  it("ignores invalid context usage", () => {
    handlePicoMessage(
      {
        type: "message.create",
        session_id: SESSION,
        payload: {
          message_id: "m1",
          content: "hi",
          context_usage: { used_tokens: 10, total_tokens: 0 },
        },
      },
      SESSION,
    )

    expect(getChatState().contextUsage).toBeUndefined()
  })
})

describe("handlePicoMessage message.update", () => {
  it("updates an existing message and keeps unspecified fields", () => {
    handlePicoMessage(
      {
        type: "message.create",
        session_id: SESSION,
        timestamp: 1_700_000_000_000,
        payload: { message_id: "m1", content: "a", model_name: "gpt-4" },
      },
      SESSION,
    )

    handlePicoMessage(
      {
        type: "message.update",
        session_id: SESSION,
        payload: { message_id: "m1", content: "b", kind: "thought" },
      },
      SESSION,
    )

    expect(getChatState().messages).toEqual([
      {
        id: "m1",
        role: "assistant",
        content: "b",
        kind: "thought",
        modelName: "gpt-4",
        timestamp: 1_700_000_000_000,
      },
    ])
  })

  it("appends the message when the id is unknown", () => {
    handlePicoMessage(
      {
        type: "message.update",
        session_id: SESSION,
        timestamp: 1_700_000_000_000,
        payload: { message_id: "m9", content: "late" },
      },
      SESSION,
    )

    expect(getChatState().messages).toEqual([
      {
        id: "m9",
        role: "assistant",
        content: "late",
        kind: "normal",
        toolCalls: undefined,
        timestamp: 1_700_000_000_000,
      },
    ])
  })

  it("ignores updates without a message id", () => {
    handlePicoMessage(
      {
        type: "message.update",
        session_id: SESSION,
        payload: { content: "x" },
      },
      SESSION,
    )

    expect(getChatState().messages).toEqual([])
  })
})

describe("handlePicoMessage message.delete", () => {
  it("removes the matching message", () => {
    updateChatStore({
      messages: [
        {
          id: "m1",
          role: "assistant",
          content: "a",
          timestamp: 1,
        },
        {
          id: "m2",
          role: "assistant",
          content: "b",
          timestamp: 2,
        },
      ],
    })

    handlePicoMessage(
      {
        type: "message.delete",
        session_id: SESSION,
        payload: { message_id: "m1" },
      },
      SESSION,
    )

    expect(getChatState().messages.map((message) => message.id)).toEqual(["m2"])
  })

  it("ignores deletes without a message id", () => {
    updateChatStore({
      messages: [{ id: "m1", role: "assistant", content: "a", timestamp: 1 }],
    })

    handlePicoMessage(
      { type: "message.delete", session_id: SESSION, payload: {} },
      SESSION,
    )

    expect(getChatState().messages).toHaveLength(1)
  })
})

describe("handlePicoMessage typing and error events", () => {
  it("toggles the typing flag", () => {
    handlePicoMessage({ type: "typing.start", session_id: SESSION }, SESSION)
    expect(getChatState().isTyping).toBe(true)

    handlePicoMessage({ type: "typing.stop", session_id: SESSION }, SESSION)
    expect(getChatState().isTyping).toBe(false)
  })

  it("removes the failed request, stops typing and reports the error", () => {
    updateChatStore({
      isTyping: true,
      messages: [
        { id: "m1", role: "assistant", content: "a", timestamp: 1 },
        { id: "m2", role: "assistant", content: "b", timestamp: 2 },
      ],
    })

    handlePicoMessage(
      {
        type: "error",
        session_id: SESSION,
        payload: { request_id: "m1", message: "boom" },
      },
      SESSION,
    )

    expect(getChatState().messages.map((message) => message.id)).toEqual(["m2"])
    expect(getChatState().isTyping).toBe(false)
    expect(toast.error).toHaveBeenCalledWith("boom")
  })

  it("does not toast when the error has no message", () => {
    handlePicoMessage(
      { type: "error", session_id: SESSION, payload: { request_id: "m1" } },
      SESSION,
    )

    expect(toast.error).not.toHaveBeenCalled()
  })
})
