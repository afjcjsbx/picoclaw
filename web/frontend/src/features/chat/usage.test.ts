import {
  estimateUsageCostUsd,
  formatTokenCount,
  formatUsageCostUsd,
  parseTokenUsage,
  resolveModelPricing,
} from "./usage.ts"

function expectEqual(actual: unknown, expected: unknown) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(
      `Expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`,
    )
  }
}

// parseTokenUsage: the backend always sends the three counters together.
expectEqual(
  parseTokenUsage({
    usage: { input_tokens: 1234, output_tokens: 567, total_tokens: 1801 },
  }),
  { input_tokens: 1234, output_tokens: 567, total_tokens: 1801 },
)

// total_tokens is derived when the backend omits it.
expectEqual(
  parseTokenUsage({ usage: { input_tokens: 10, output_tokens: 5 } }),
  {
    input_tokens: 10,
    output_tokens: 5,
    total_tokens: 15,
  },
)

// Prompt-cache counters survive persistence when present.
expectEqual(
  parseTokenUsage({
    usage: {
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      cache_read_tokens: 40,
      cache_creation_tokens: 10,
    },
  }),
  {
    input_tokens: 100,
    output_tokens: 50,
    total_tokens: 150,
    cache_read_tokens: 40,
    cache_creation_tokens: 10,
  },
)

// Backend-reported cost is preserved.
expectEqual(
  parseTokenUsage({
    usage: {
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      cost_usd: 0.0123,
    },
  }),
  {
    input_tokens: 100,
    output_tokens: 50,
    total_tokens: 150,
    cost_usd: 0.0123,
  },
)

// Missing, malformed and zeroed usage objects are ignored.
expectEqual(parseTokenUsage(undefined), undefined)
expectEqual(parseTokenUsage({}), undefined)
expectEqual(parseTokenUsage({ usage: "1" }), undefined)
expectEqual(parseTokenUsage({ usage: [] }), undefined)
expectEqual(
  parseTokenUsage({ usage: { input_tokens: 0, output_tokens: 0 } }),
  undefined,
)
expectEqual(
  parseTokenUsage({ usage: { input_tokens: -1, output_tokens: -2 } }),
  undefined,
)

// resolveModelPricing: aliases and provider-prefixed ids both match.
expectEqual(resolveModelPricing("gpt-4o-mini"), {
  inputPerMillionUsd: 0.15,
  outputPerMillionUsd: 0.6,
})
expectEqual(resolveModelPricing("openai/gpt-4o"), {
  inputPerMillionUsd: 2.5,
  outputPerMillionUsd: 10,
})
expectEqual(resolveModelPricing("Claude Sonnet 4"), {
  inputPerMillionUsd: 3,
  outputPerMillionUsd: 15,
})
expectEqual(resolveModelPricing("local-llama"), undefined)
expectEqual(resolveModelPricing(undefined), undefined)

// estimateUsageCostUsd: reported cost wins over local list prices.
expectEqual(
  estimateUsageCostUsd(
    {
      input_tokens: 1_000_000,
      output_tokens: 1_000_000,
      total_tokens: 2_000_000,
      cost_usd: 0.5,
    },
    "gpt-4o",
  ),
  0.5,
)
expectEqual(
  estimateUsageCostUsd(
    {
      input_tokens: 1_000_000,
      output_tokens: 1_000_000,
      total_tokens: 2_000_000,
    },
    "gpt-4o",
  ),
  12.5,
)
expectEqual(
  estimateUsageCostUsd({
    input_tokens: 1_000,
    output_tokens: 500,
    total_tokens: 1_500,
  }),
  undefined,
)

// Formatting helpers keep token counts and tiny costs readable.
expectEqual(formatTokenCount(1801), "1,801")
expectEqual(formatUsageCostUsd(0), "$0.00")
expectEqual(formatUsageCostUsd(0.00001), "<$0.0001")
expectEqual(formatUsageCostUsd(0.0042), "$0.0042")
expectEqual(formatUsageCostUsd(0.125), "$0.125")
expectEqual(formatUsageCostUsd(1.5), "$1.50")
