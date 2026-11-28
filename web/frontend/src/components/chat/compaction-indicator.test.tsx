import { act, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { CompactionIndicator } from "./compaction-indicator"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, options?: Record<string, unknown>) => {
      if (typeof options?.defaultValue === "string") {
        return options.defaultValue
      }
      return key
    },
  }),
}))

afterEach(() => {
  vi.useRealTimers()
})

describe("CompactionIndicator", () => {
  it("stays hidden for brief compactions", () => {
    vi.useFakeTimers()
    render(<CompactionIndicator />)

    act(() => {
      vi.advanceTimersByTime(100)
    })

    expect(screen.queryByRole("status")).toBeNull()
  })

  it("shows a compacting divider after the delay", () => {
    vi.useFakeTimers()
    render(<CompactionIndicator />)

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(screen.getByRole("status")).toHaveTextContent("Compacting...")
  })

  it("shows the completed marker immediately", () => {
    render(<CompactionIndicator phase="completed" />)

    expect(screen.getByRole("status")).toHaveTextContent("Context compacted")
  })
})
