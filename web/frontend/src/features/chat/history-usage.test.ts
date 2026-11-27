import { describe, expect, it, vi } from "vitest"

import { getSessionHistory } from "@/api/sessions"

import { loadSessionMessages } from "./history"

vi.mock("@/api/sessions", () => ({ getSessionHistory: vi.fn() }))

describe("loadSessionMessages usage", () => {
  it("keeps turn timing metrics from reopened history", async () => {
    vi.mocked(getSessionHistory).mockResolvedValue({
      id: "session-1",
      start: 0,
      total: 1,
      messages: [
        {
          role: "assistant",
          content: "hello",
          created_at: "2026-11-27T12:00:00Z",
          usage: {
            input_tokens: 100,
            output_tokens: 50,
            total_tokens: 150,
            duration_ms: 48_300,
            elapsed_ms: 85_000,
            tool_duration_ms: 36_000,
            average_ttft_ms: 2000,
          },
        },
      ],
      summary: "",
      created: "2026-11-27T12:00:00Z",
      updated: "2026-11-27T12:00:00Z",
    })

    const result = await loadSessionMessages("session-1")

    expect(result.messages[0].usage).toEqual({
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      duration_ms: 48_300,
      elapsed_ms: 85_000,
      tool_duration_ms: 36_000,
      average_ttft_ms: 2000,
    })
  })
})
