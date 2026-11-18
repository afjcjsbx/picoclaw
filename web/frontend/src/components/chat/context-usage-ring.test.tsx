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

describe("ContextUsageRing", () => {
  it("shows the current usage percentage", () => {
    render(<ContextUsageRing usage={USAGE} />)

    expect(screen.getByRole("button")).toHaveTextContent("21")
  })

  it("reveals the context breakdown on hover", async () => {
    const { container } = render(<ContextUsageRing usage={USAGE} />)

    fireEvent.mouseEnter(container.firstElementChild as Element)

    expect(await screen.findByText("chat.contextTitle")).toBeInTheDocument()
    expect(screen.getByText("12.0k")).toBeInTheDocument()
    expect(screen.getByText("chat.contextCompressAt")).toBeInTheDocument()
    expect(screen.getByText("chat.contextSummarizeAt")).toBeInTheDocument()
  })

  it("calls the detail handler when the detail action is clicked", async () => {
    const onDetailClick = vi.fn()
    const { container } = render(
      <ContextUsageRing usage={USAGE} onDetailClick={onDetailClick} />,
    )

    fireEvent.mouseEnter(container.firstElementChild as Element)
    fireEvent.click(
      await screen.findByRole("button", { name: "chat.contextDetail" }),
    )

    expect(onDetailClick).toHaveBeenCalledTimes(1)
  })
})
