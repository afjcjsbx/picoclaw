import { describe, expect, it } from "vitest"

import {
  estimateUsageCostUsd,
  formatTokenCount,
  formatUsageCostUsd,
  parseTokenUsage,
  resolveModelPricing,
} from "./usage"

describe("parseTokenUsage", () => {
  it("keeps the three counters the backend always sends together", () => {
    expect(
      parseTokenUsage({
        usage: { input_tokens: 1234, output_tokens: 567, total_tokens: 1801 },
      }),
    ).toEqual({ input_tokens: 1234, output_tokens: 567, total_tokens: 1801 })
  })

  it("derives total_tokens when the backend omits it", () => {
    expect(
      parseTokenUsage({ usage: { input_tokens: 10, output_tokens: 5 } }),
    ).toEqual({
      input_tokens: 10,
      output_tokens: 5,
      total_tokens: 15,
    })
  })

  it("keeps prompt-cache counters when present", () => {
    expect(
      parseTokenUsage({
        usage: {
          input_tokens: 100,
          output_tokens: 50,
          total_tokens: 150,
          cache_read_tokens: 40,
          cache_creation_tokens: 10,
        },
      }),
    ).toEqual({
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      cache_read_tokens: 40,
      cache_creation_tokens: 10,
    })
  })

  it("preserves backend-reported cost", () => {
    expect(
      parseTokenUsage({
        usage: {
          input_tokens: 100,
          output_tokens: 50,
          total_tokens: 150,
          cost_usd: 0.0123,
        },
      }),
    ).toEqual({
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      cost_usd: 0.0123,
    })
  })

  it("allows cost without token counters", () => {
    expect(parseTokenUsage({ usage: { cost_usd: 0.0123 } })).toEqual({
      total_tokens: 0,
      cost_usd: 0.0123,
    })
  })

  it("ignores missing, malformed and zeroed usage objects", () => {
    expect(parseTokenUsage(undefined)).toBeUndefined()
    expect(parseTokenUsage({})).toBeUndefined()
    expect(parseTokenUsage({ usage: "1" })).toBeUndefined()
    expect(parseTokenUsage({ usage: [] })).toBeUndefined()
    expect(
      parseTokenUsage({ usage: { input_tokens: 0, output_tokens: 0 } }),
    ).toBeUndefined()
    expect(
      parseTokenUsage({ usage: { input_tokens: -1, output_tokens: -2 } }),
    ).toBeUndefined()
  })
})

describe("resolveModelPricing", () => {
  it("matches aliases and provider-prefixed ids", () => {
    expect(resolveModelPricing("gpt-4o-mini")).toEqual({
      inputPerMillionUsd: 0.15,
      outputPerMillionUsd: 0.6,
    })
    expect(resolveModelPricing("openai/gpt-4o")).toEqual({
      inputPerMillionUsd: 2.5,
      outputPerMillionUsd: 10,
    })
    expect(resolveModelPricing("Claude Sonnet 4")).toEqual({
      inputPerMillionUsd: 3,
      outputPerMillionUsd: 15,
    })
  })

  it("returns undefined for unknown and missing models", () => {
    expect(resolveModelPricing("local-llama")).toBeUndefined()
    expect(resolveModelPricing(undefined)).toBeUndefined()
  })
})

describe("estimateUsageCostUsd", () => {
  it("prefers a reported cost over local list prices", () => {
    expect(
      estimateUsageCostUsd(
        {
          input_tokens: 1_000_000,
          output_tokens: 1_000_000,
          total_tokens: 2_000_000,
          cost_usd: 0.5,
        },
        "gpt-4o",
      ),
    ).toBe(0.5)
  })

  it("estimates from list prices when no cost is reported", () => {
    expect(
      estimateUsageCostUsd(
        {
          input_tokens: 1_000_000,
          output_tokens: 1_000_000,
          total_tokens: 2_000_000,
        },
        "gpt-4o",
      ),
    ).toBe(12.5)
  })

  it("returns undefined when the model has no known price", () => {
    expect(
      estimateUsageCostUsd({
        input_tokens: 1_000,
        output_tokens: 500,
        total_tokens: 1_500,
      }),
    ).toBeUndefined()
  })
})

describe("formatting helpers", () => {
  it("keeps token counts readable", () => {
    expect(formatTokenCount(1801)).toBe("1,801")
  })

  it("keeps tiny and normal costs readable", () => {
    expect(formatUsageCostUsd(0)).toBe("$0.00")
    expect(formatUsageCostUsd(0.00001)).toBe("<$0.0001")
    expect(formatUsageCostUsd(0.0042)).toBe("$0.0042")
    expect(formatUsageCostUsd(0.125)).toBe("$0.125")
    expect(formatUsageCostUsd(1.5)).toBe("$1.50")
  })
})
