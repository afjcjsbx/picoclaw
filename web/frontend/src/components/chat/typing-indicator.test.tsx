import { act, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { TypingIndicator } from "./typing-indicator"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => {
      const count = Number(options?.count ?? 0)
      const template =
        key === "chat.activityThoughtsCount"
          ? count === 1
            ? "{{count}} thought"
            : "{{count}} thoughts"
          : key === "chat.activityToolsCount"
            ? count === 1
              ? "{{count}} tool"
              : "{{count}} tools"
            : typeof options?.defaultValue === "string"
              ? options.defaultValue
              : key
      return template.replace(/{{\s*(\w+)\s*}}/g, (_, name: string) =>
        String(options?.[name] ?? ""),
      )
    },
  }),
}))

afterEach(() => {
  vi.useRealTimers()
})

describe("TypingIndicator", () => {
  it("shows final duration with reasoning and tool counts", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-11-27T16:00:00Z"))
    const { rerender } = render(
      <TypingIndicator isTyping reasoningCount={2} toolCount={3} />,
    )

    act(() => {
      vi.advanceTimersByTime(7_000)
    })
    rerender(
      <TypingIndicator isTyping={false} reasoningCount={2} toolCount={3} />,
    )

    expect(screen.getByRole("status")).toHaveTextContent(
      "Worked for 7s · 2 thoughts · 3 tools",
    )
  })

  it("uses singular count labels", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-11-27T16:00:00Z"))
    const { rerender } = render(
      <TypingIndicator isTyping reasoningCount={1} toolCount={1} />,
    )

    act(() => {
      vi.advanceTimersByTime(1_000)
    })
    rerender(
      <TypingIndicator isTyping={false} reasoningCount={1} toolCount={1} />,
    )

    expect(screen.getByRole("status")).toHaveTextContent(
      "Worked for 1s · 1 thought · 1 tool",
    )
  })

  it("keeps the active label free of final counts", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-11-27T16:00:00Z"))
    render(<TypingIndicator isTyping reasoningCount={2} toolCount={3} />)

    expect(screen.getByRole("status")).toHaveTextContent("Working for 0s")
    expect(screen.getByRole("status")).not.toHaveTextContent("thoughts")
    expect(screen.getByRole("status")).not.toHaveTextContent("tools")
  })

  it("shows the persisted worked duration after a reload", () => {
    render(<TypingIndicator isTyping={false} workedDurationMs={12_000} />)

    expect(screen.getByRole("status")).toHaveTextContent("Worked for 12s")
  })

  it("starts the active timer from the server start time", () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date("2026-11-27T16:00:00Z"))
    const startedAt = Date.now() - 5_000

    render(<TypingIndicator isTyping startedAt={startedAt} />)

    expect(screen.getByRole("status")).toHaveTextContent("Working for 5s")
  })
})
