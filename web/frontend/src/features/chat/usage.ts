import type { TokenUsage } from "@/store/chat"

export interface ModelPricing {
  inputPerMillionUsd: number
  outputPerMillionUsd: number
}

/**
 * Approximate public list prices in USD per 1M tokens.
 *
 * These are only a UI fallback used to show a rough per-turn cost when the
 * backend does not attach `usage.cost_usd`. Prices are provider list prices and
 * change over time; keep entries ordered from most specific to least specific
 * and update them when a provider changes its pricing.
 */
const MODEL_PRICING: ReadonlyArray<readonly [RegExp, ModelPricing]> = [
  [/gpt-4o-mini/, { inputPerMillionUsd: 0.15, outputPerMillionUsd: 0.6 }],
  [/gpt-4o/, { inputPerMillionUsd: 2.5, outputPerMillionUsd: 10 }],
  [/gpt-4\.1-nano/, { inputPerMillionUsd: 0.1, outputPerMillionUsd: 0.4 }],
  [/gpt-4\.1-mini/, { inputPerMillionUsd: 0.4, outputPerMillionUsd: 1.6 }],
  [/gpt-4\.1/, { inputPerMillionUsd: 2, outputPerMillionUsd: 8 }],
  [/(?:^|[-_/])o4-mini/, { inputPerMillionUsd: 1.1, outputPerMillionUsd: 4.4 }],
  [/(?:^|[-_/])o3-mini/, { inputPerMillionUsd: 1.1, outputPerMillionUsd: 4.4 }],
  [
    /(?:^|[-_/])o3(?:$|[-_/])/,
    { inputPerMillionUsd: 2, outputPerMillionUsd: 8 },
  ],
  [/claude-3[-.]5-haiku/, { inputPerMillionUsd: 0.8, outputPerMillionUsd: 4 }],
  [/claude-.*haiku/, { inputPerMillionUsd: 1, outputPerMillionUsd: 5 }],
  [/claude-.*sonnet/, { inputPerMillionUsd: 3, outputPerMillionUsd: 15 }],
  [/claude-.*opus/, { inputPerMillionUsd: 15, outputPerMillionUsd: 75 }],
  [/gemini-2\.5-pro/, { inputPerMillionUsd: 1.25, outputPerMillionUsd: 10 }],
  [/gemini-2\.5-flash/, { inputPerMillionUsd: 0.3, outputPerMillionUsd: 2.5 }],
  [
    /deepseek-reasoner/,
    { inputPerMillionUsd: 0.55, outputPerMillionUsd: 2.19 },
  ],
  [/deepseek-chat/, { inputPerMillionUsd: 0.27, outputPerMillionUsd: 1.1 }],
  [/grok-4/, { inputPerMillionUsd: 3, outputPerMillionUsd: 15 }],
  [/grok-3/, { inputPerMillionUsd: 3, outputPerMillionUsd: 15 }],
]

function toTokenCount(value: unknown): number | undefined {
  if (value === null || value === undefined || value === "") {
    return undefined
  }
  const numeric = typeof value === "number" ? value : Number(value)
  if (!Number.isFinite(numeric) || numeric < 0) {
    return undefined
  }
  return Math.trunc(numeric)
}

function toCostUsd(value: unknown): number | undefined {
  if (value === null || value === undefined || value === "") {
    return undefined
  }
  const numeric = typeof value === "number" ? value : Number(value)
  if (!Number.isFinite(numeric) || numeric < 0) {
    return undefined
  }
  return numeric
}

/** Parse the `usage` object emitted on finalized Pico messages. */
export function parseTokenUsage(
  payload: Record<string, unknown> | undefined,
): TokenUsage | undefined {
  const raw = payload?.usage
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return undefined
  }

  const usage = raw as Record<string, unknown>
  const input = toTokenCount(usage.input_tokens)
  const output = toTokenCount(usage.output_tokens)
  const reportedTotal = toTokenCount(usage.total_tokens)
  const total =
    reportedTotal && reportedTotal > 0
      ? reportedTotal
      : (input ?? 0) + (output ?? 0)
  if (total <= 0) {
    return undefined
  }

  const cost = toCostUsd(usage.cost_usd)
  const cacheRead = toTokenCount(usage.cache_read_tokens)
  const cacheCreation = toTokenCount(usage.cache_creation_tokens)

  return {
    ...(input !== undefined ? { input_tokens: input } : {}),
    ...(output !== undefined ? { output_tokens: output } : {}),
    total_tokens: total,
    ...(cacheRead ? { cache_read_tokens: cacheRead } : {}),
    ...(cacheCreation ? { cache_creation_tokens: cacheCreation } : {}),
    ...(cost !== undefined ? { cost_usd: cost } : {}),
  }
}

function normalizeModelName(modelName: string): string {
  return modelName
    .trim()
    .toLowerCase()
    .replace(/^[^/]+\//, "")
    .replace(/\s+/g, "-")
}

/** Resolve approximate list pricing for a configured model alias/id. */
export function resolveModelPricing(
  modelName: string | undefined,
): ModelPricing | undefined {
  if (!modelName) {
    return undefined
  }
  const normalized = normalizeModelName(modelName)
  if (!normalized) {
    return undefined
  }
  for (const [pattern, pricing] of MODEL_PRICING) {
    if (pattern.test(normalized)) {
      return pricing
    }
  }
  return undefined
}

/**
 * Return the turn cost in USD. A backend-provided `usage.cost_usd` always wins
 * over the local list-price estimate.
 */
export function estimateUsageCostUsd(
  usage: TokenUsage,
  modelName?: string,
): number | undefined {
  if (usage.cost_usd !== undefined) {
    return usage.cost_usd
  }
  const pricing = resolveModelPricing(modelName)
  if (!pricing) {
    return undefined
  }
  const input = usage.input_tokens ?? 0
  const output = usage.output_tokens ?? 0
  return (
    (input * pricing.inputPerMillionUsd +
      output * pricing.outputPerMillionUsd) /
    1_000_000
  )
}

export function formatTokenCount(value: number): string {
  return Math.max(0, Math.round(value)).toLocaleString("en-US")
}

export function formatUsageCostUsd(cost: number): string {
  if (!Number.isFinite(cost) || cost <= 0) {
    return "$0.00"
  }
  if (cost < 0.0001) {
    return "<$0.0001"
  }
  if (cost < 0.01) {
    return `$${cost.toFixed(4)}`
  }
  if (cost < 1) {
    return `$${cost.toFixed(3)}`
  }
  return `$${cost.toFixed(2)}`
}
