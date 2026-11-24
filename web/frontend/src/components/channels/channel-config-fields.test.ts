import { describe, expect, it } from "vitest"

import {
  buildEditConfig,
  getA2ATokenUpdate,
} from "@/components/channels/channel-config-fields"

describe("A2A channel config", () => {
  it("initializes hidden token editor state", () => {
    const config = buildEditConfig("a2a", { host: "127.0.0.1", port: 9900 })

    expect(config._tokens).toEqual([])
    expect(config._tokens_cleared).toBe(false)
  })

  it("omits tokens when the editor is untouched", () => {
    expect(getA2ATokenUpdate(buildEditConfig("a2a", {}))).toBeUndefined()
  })

  it("serializes new tokens as an array", () => {
    expect(getA2ATokenUpdate({ _tokens: ["token-a", "token-b"] })).toEqual([
      "token-a",
      "token-b",
    ])
  })

  it("sends an empty array only when tokens are explicitly cleared", () => {
    expect(getA2ATokenUpdate({ _tokens: [], _tokens_cleared: true })).toEqual(
      [],
    )
  })
})
