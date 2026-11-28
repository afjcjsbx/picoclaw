import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { ContextUsageRing } from "./context-usage-ring"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

const USAGE = {
  used_tokens: 42_000,
  total_tokens: 200_000,
  history_tokens: 12_000,
  compress_at_tokens: 160_000,
  summarize_at_tokens: 120_000,
  used_percent: 21,
}

const CONVERSATION_USAGE = {
  inputTokens: 120_400,
  outputTokens: 19_800,
  costUsd: 0.04,
  cacheHitPercent: 100,
}

describe("ContextUsageRing", () => {
  it("shows the context window percentage in the accessible label", () => {
    render(
      <ContextUsageRing usage={USAGE} conversationUsage={CONVERSATION_USAGE} />,
    )

    expect(
      screen.getByRole("button", { name: /21% chat\.contextUsed/ }),
    ).toBeInTheDocument()
  })

  it("reveals the conversation breakdown on hover", async () => {
    const { container } = render(
      <ContextUsageRing usage={USAGE} conversationUsage={CONVERSATION_USAGE} />,
    )

    fireEvent.mouseEnter(container.firstElementChild as Element)

    expect(await screen.findByText("chat.contextWindow")).toBeInTheDocument()
    expect(screen.getByText(/42k \/ 200k/)).toBeInTheDocument()
    expect(screen.getByText("120.4K ↑ · 19.8K ↓")).toBeInTheDocument()
    expect(screen.getByText("100%")).toBeInTheDocument()
    expect(screen.getByText("$0.04")).toBeInTheDocument()
  })

  it("calls the detail handler when the detail action is clicked", async () => {
    const onDetailClick = vi.fn()
    const { container } = render(
      <ContextUsageRing
        usage={USAGE}
        conversationUsage={CONVERSATION_USAGE}
        onDetailClick={onDetailClick}
      />,
    )

    fireEvent.mouseEnter(container.firstElementChild as Element)
    fireEvent.click(
      await screen.findByRole("button", { name: "chat.contextDetail" }),
    )

    expect(onDetailClick).toHaveBeenCalledTimes(1)
  })
})
