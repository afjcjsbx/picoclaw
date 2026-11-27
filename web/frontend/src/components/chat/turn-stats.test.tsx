import { fireEvent, render, screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import type { TokenUsage } from "@/store/chat"

import { TurnStats } from "./turn-stats"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => {
      const template =
        typeof options?.defaultValue === "string" ? options.defaultValue : key
      return template.replace(/{{\s*(\w+)\s*}}/g, (_, name: string) =>
        String(options?.[name] ?? ""),
      )
    },
  }),
}))

const USAGE: TokenUsage = {
  input_tokens: 47_500,
  output_tokens: 3500,
  total_tokens: 51_000,
  cache_read_tokens: 47_025,
  requests: 14,
  duration_ms: 48_300,
  elapsed_ms: 85_000,
  tool_duration_ms: 36_000,
  average_ttft_ms: 2000,
}

describe("TurnStats", () => {
  it("reveals the turn breakdown on hover", async () => {
    render(
      <TurnStats
        usage={USAGE}
        modelName="opencode-go/hungerg-free"
        isDefaultModel
      />,
    )

    fireEvent.pointerMove(
      screen.getByRole("button", { name: "chat.turnStats.open" }),
      { pointerType: "mouse" },
    )

    const tooltip = await screen.findByRole("tooltip")
    expect(tooltip).toHaveTextContent("chat.turnStats.title")
    expect(tooltip).toHaveTextContent("opencode-go/hungerg-free")
    expect(tooltip).toHaveTextContent("(default)")
    expect(tooltip).toHaveTextContent("~72 tok/s")
    expect(tooltip).toHaveTextContent("~41 tok/s")
    expect(tooltip).toHaveTextContent("1m 25s")
    expect(tooltip).toHaveTextContent("48.3s")
    expect(tooltip).toHaveTextContent("36.0s")
    expect(tooltip).toHaveTextContent("2.0s")
    expect(tooltip).toHaveTextContent("14")
    expect(tooltip).toHaveTextContent("47.5K ↑ · 3.5K ↓")
    expect(tooltip).toHaveTextContent("99%")
  })

  it("keeps the layout usable when optional metrics are unavailable", async () => {
    render(<TurnStats usage={{ total_tokens: 100 }} />)

    fireEvent.pointerMove(
      screen.getByRole("button", { name: "chat.turnStats.open" }),
      { pointerType: "mouse" },
    )

    const tooltip = await screen.findByRole("tooltip")
    expect(tooltip).toHaveTextContent("chat.turnStats.model")
    expect(tooltip).toHaveTextContent("chat.turnStats.averageTtft")
    expect(within(tooltip).getAllByText("—").length).toBeGreaterThan(0)
    expect(tooltip).not.toHaveTextContent("NaN")
  })
})
