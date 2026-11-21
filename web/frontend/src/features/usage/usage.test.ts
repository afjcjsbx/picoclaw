import { describe, expect, it } from "vitest"

import {
  compactTokens,
  fillDays,
  fmtInt,
  formatCost,
  sharePercent,
  tokenCategories,
} from "./usage"

describe("tokenCategories", () => {
  it("computes fresh input = input minus cache tokens", () => {
    const cats = tokenCategories({
      input_tokens: 1000,
      output_tokens: 200,
      cache_read_tokens: 400,
      cache_creation_tokens: 100,
      reasoning_tokens: 50,
    })
    expect(cats.freshInput).toBe(500)
    expect(cats.cacheRead).toBe(400)
    expect(cats.cacheWrite).toBe(100)
    expect(cats.answer).toBe(150)
    expect(cats.reasoning).toBe(50)
  })

  it("clamps freshInput and answer to 0 when cache exceeds input", () => {
    const cats = tokenCategories({
      input_tokens: 100,
      output_tokens: 10,
      cache_read_tokens: 60,
      cache_creation_tokens: 60,
      reasoning_tokens: 20,
    })
    expect(cats.freshInput).toBe(0)
    expect(cats.answer).toBe(0)
  })

  it("handles missing optional fields", () => {
    const cats = tokenCategories({ input_tokens: 500, output_tokens: 100 })
    expect(cats.freshInput).toBe(500)
    expect(cats.cacheRead).toBe(0)
    expect(cats.cacheWrite).toBe(0)
    expect(cats.answer).toBe(100)
    expect(cats.reasoning).toBe(0)
  })

  it("handles empty totals", () => {
    const cats = tokenCategories({})
    expect(cats.freshInput).toBe(0)
    expect(cats.answer).toBe(0)
  })
})

describe("sharePercent", () => {
  it("returns one decimal", () => {
    expect(sharePercent(1, 3)).toBe("33.3%")
    expect(sharePercent(500, 1000)).toBe("50.0%")
  })

  it("returns <0.1% for tiny nonzero", () => {
    expect(sharePercent(1, 10_000)).toBe("<0.1%")
  })

  it("returns 0.0% when total is 0", () => {
    expect(sharePercent(0, 0)).toBe("0.0%")
  })
})

describe("compactTokens", () => {
  it("formats millions", () => {
    expect(compactTokens(1_234_567)).toBe("1.2M")
    expect(compactTokens(10_000_000)).toBe("10.0M")
  })

  it("formats thousands", () => {
    expect(compactTokens(1_234)).toBe("1.2K")
    expect(compactTokens(999_900)).toBe("999.9K")
  })

  it("passes through small counts", () => {
    expect(compactTokens(999)).toBe("999")
    expect(compactTokens(0)).toBe("0")
  })
})

describe("formatCost", () => {
  it("returns dash for undefined or 0", () => {
    expect(formatCost(undefined)).toBe("—")
    expect(formatCost(0)).toBe("—")
  })

  it("formats tiny cost", () => {
    expect(formatCost(0.00001)).toBe("<$0.0001")
  })

  it("formats sub-cent costs with 4 decimals", () => {
    expect(formatCost(0.0042)).toBe("$0.0042")
  })

  it("formats sub-dollar costs with 3 decimals", () => {
    expect(formatCost(0.125)).toBe("$0.125")
  })

  it("formats dollar+ costs with 2 decimals", () => {
    expect(formatCost(1.5)).toBe("$1.50")
    expect(formatCost(12.34)).toBe("$12.34")
  })
})

describe("fmtInt", () => {
  it("formats with thousands separators", () => {
    expect(fmtInt(1_234_567)).toBe("1,234,567")
  })

  it("returns dash for undefined", () => {
    expect(fmtInt(undefined)).toBe("—")
  })
})

describe("fillDays", () => {
  it("fills missing days between since and entries", () => {
    const rows = fillDays(
      [{ date: "2024-01-03", total_tokens: 100 }],
      "2024-01-01T00:00:00Z",
    )
    expect(rows[0].date).toBe("2024-01-01")
    expect(rows[0].total_tokens).toBeUndefined()
    expect(rows[1].date).toBe("2024-01-02")
    expect(rows[1].total_tokens).toBeUndefined()
    expect(rows[2].date).toBe("2024-01-03")
    expect(rows[2].total_tokens).toBe(100)
  })

  it("includes today as the last entry", () => {
    const d = new Date()
    const y = d.getFullYear()
    const mo = String(d.getMonth() + 1).padStart(2, "0")
    const dy = String(d.getDate()).padStart(2, "0")
    const today = `${y}-${mo}-${dy}`

    const rows = fillDays([], `${today}T00:00:00Z`)
    expect(rows.length).toBeGreaterThanOrEqual(1)
    expect(rows[rows.length - 1].date).toBe(today)
  })

  it("keeps a reported day that is ahead of the browser's today", () => {
    const rows = fillDays(
      [{ date: "2999-01-02", total_tokens: 5 }],
      "2999-01-01T00:00:00Z",
    )
    expect(rows.map((r) => r.date)).toEqual(["2999-01-01", "2999-01-02"])
    expect(rows[1].total_tokens).toBe(5)
  })

  it("handles month and year rollover", () => {
    const rows = fillDays(
      [{ date: "2024-01-01", total_tokens: 1 }],
      "2023-12-31T00:00:00Z",
    )
    expect(rows[0].date).toBe("2023-12-31")
    expect(rows[1].date).toBe("2024-01-01")
  })

  it("preserves existing rows", () => {
    const rows = fillDays(
      [
        { date: "2024-03-01", total_tokens: 10 },
        { date: "2024-03-03", total_tokens: 20 },
      ],
      "2024-03-01T00:00:00Z",
    )
    expect(rows.find((r) => r.date === "2024-03-01")?.total_tokens).toBe(10)
    expect(
      rows.find((r) => r.date === "2024-03-02")?.total_tokens,
    ).toBeUndefined()
    expect(rows.find((r) => r.date === "2024-03-03")?.total_tokens).toBe(20)
  })
})
