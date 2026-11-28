import type { DailyRow, Totals } from "@/api/usage"

export interface TokenCategories {
  freshInput: number
  cacheRead: number
  cacheWrite: number
  answer: number
  reasoning: number
}

/** Split totals into 5 display categories.
 * cache_read + cache_creation are PART of input_tokens.
 * reasoning_tokens is PART of output_tokens. */
export function tokenCategories(t: Totals): TokenCategories {
  const input = t.input_tokens ?? 0
  const output = t.output_tokens ?? 0
  const cacheRead = t.cache_read_tokens ?? 0
  const cacheWrite = t.cache_creation_tokens ?? 0
  const reasoning = t.reasoning_tokens ?? 0
  return {
    freshInput: Math.max(0, input - cacheRead - cacheWrite),
    cacheRead,
    cacheWrite,
    answer: Math.max(0, output - reasoning),
    reasoning,
  }
}

/** Share as percentage with one decimal; "<0.1%" for tiny nonzero. */
export function sharePercent(part: number, total: number): string {
  if (total <= 0) return "0.0%"
  const pct = (part / total) * 100
  if (pct > 0 && pct < 0.1) return "<0.1%"
  return `${pct.toFixed(1)}%`
}

/** Compact token count: 1,234 → "1.2K"; 1,234,567 → "1.2M". */
export function compactTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(Math.round(n))
}

/** Format cost: undefined/0 → "—"; positive value → "$X.XX". */
export function formatCost(cost: number | undefined): string {
  if (cost === undefined || cost === 0) return "—"
  if (cost < 0.0001) return "<$0.0001"
  if (cost < 0.01) return `$${cost.toFixed(4)}`
  if (cost < 1) return `$${cost.toFixed(3)}`
  return `$${cost.toFixed(2)}`
}

/** Integer with thousands separators. */
export function fmtInt(n: number | undefined): string {
  if (n === undefined) return "—"
  return Math.round(n).toLocaleString("en-US")
}

function localDateStr(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, "0")
  const day = String(d.getDate()).padStart(2, "0")
  return `${y}-${m}-${day}`
}

function nextDate(yyyymmdd: string): string {
  const parts = yyyymmdd.split("-")
  const y = Number(parts[0])
  const m = Number(parts[1])
  const d = Number(parts[2])
  // Local Date constructor – no timezone conversion, safe to increment
  return localDateStr(new Date(y, m - 1, d + 1))
}

/**
 * Fill gaps in period.daily so the chart has a continuous x axis.
 * `sinceRfc3339` is the RFC3339 start date; take the first 10 chars (YYYY-MM-DD).
 * Rows are ordered oldest → newest. Missing days get zero-value rows.
 */
export function fillDays(daily: DailyRow[], sinceRfc3339: string): DailyRow[] {
  const startDate = sinceRfc3339.slice(0, 10)
  // The server picks the days in its own time zone; a browser behind it must
  // not cut off the server's latest day.
  const lastReported = daily.reduce(
    (max, row) => (row.date > max ? row.date : max),
    "",
  )
  const today = [localDateStr(new Date()), lastReported].reduce((a, b) =>
    a > b ? a : b,
  )

  const byDate = new Map<string, DailyRow>()
  for (const row of daily) byDate.set(row.date, row)

  const result: DailyRow[] = []
  let cur = startDate
  while (cur <= today) {
    result.push(byDate.get(cur) ?? { date: cur })
    cur = nextDate(cur)
  }
  return result
}
