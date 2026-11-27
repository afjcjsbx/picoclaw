import { describe, expect, it } from "vitest"

import {
  cacheHitRate,
  estimateUsageCostUsd,
  formatCompactTokenCount,
  formatDuration,
  formatTokenCount,
  formatTokensPerSecond,
  formatUsageCostUsd,
  parseTokenUsage,
  resolveModelPricing,
  tokensPerSecond,
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

  it("keeps turn call count and last-call context", () => {
    expect(
      parseTokenUsage({
        usage: {
          input_tokens: 103_631,
          output_tokens: 3_207,
          total_tokens: 106_838,
          requests: 5,
          context_tokens: 24_310,
          duration_ms: 64_200,
        },
      }),
    ).toEqual({
      input_tokens: 103_631,
      output_tokens: 3_207,
      total_tokens: 106_838,
      requests: 5,
      context_tokens: 24_310,
      duration_ms: 64_200,
    })
  })

  it("keeps wall-clock, tool, and first-token timing when present", () => {
    expect(
      parseTokenUsage({
        usage: {
          input_tokens: 100,
          output_tokens: 50,
          total_tokens: 150,
          duration_ms: 48_300,
          elapsed_ms: 85_000,
          tool_duration_ms: 36_000,
          average_ttft_ms: 2000,
        },
      }),
    ).toEqual({
      input_tokens: 100,
      output_tokens: 50,
      total_tokens: 150,
      duration_ms: 48_300,
      elapsed_ms: 85_000,
      tool_duration_ms: 36_000,
      average_ttft_ms: 2000,
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

describe("tokensPerSecond", () => {
  it("divides output tokens by provider time", () => {
    expect(
      tokensPerSecond({
        output_tokens: 500,
        total_tokens: 500,
        duration_ms: 2000,
      }),
    ).toBe(250)
  })

  it("can divide output tokens by whole-turn time", () => {
    expect(
      tokensPerSecond(
        {
          output_tokens: 500,
          total_tokens: 500,
          duration_ms: 2000,
          elapsed_ms: 5000,
        },
        5000,
      ),
    ).toBe(100)
  })

  it("returns undefined without output tokens or a duration", () => {
    expect(tokensPerSecond({ total_tokens: 0 })).toBeUndefined()
    expect(
      tokensPerSecond({ output_tokens: 10, total_tokens: 10 }),
    ).toBeUndefined()
    expect(
      tokensPerSecond({ output_tokens: 10, total_tokens: 10, duration_ms: 0 }),
    ).toBeUndefined()
  })

  it("formats rates at different magnitudes", () => {
    expect(formatTokensPerSecond(250.4)).toBe("250")
    expect(formatTokensPerSecond(42.36)).toBe("42.4")
    expect(formatTokensPerSecond(3.456)).toBe("3.46")
    expect(formatTokensPerSecond(0)).toBe("0")
  })
})

describe("turn stats formatting", () => {
  it("formats sub-minute durations with one decimal", () => {
    expect(formatDuration(0)).toBe("0.0s")
    expect(formatDuration(2000)).toBe("2.0s")
    expect(formatDuration(28_420)).toBe("28.4s")
  })

  it("formats durations of at least one minute as minutes and seconds", () => {
    expect(formatDuration(60_000)).toBe("1m 0s")
    expect(formatDuration(85_000)).toBe("1m 25s")
  })

  it("formats compact token counts", () => {
    expect(formatCompactTokenCount(999)).toBe("999")
    expect(formatCompactTokenCount(2000)).toBe("2.0K")
    expect(formatCompactTokenCount(13_500)).toBe("13.5K")
    expect(formatCompactTokenCount(1_000_000)).toBe("1.0M")
  })
})

describe("cacheHitRate", () => {
  it("returns the share of input tokens served from cache", () => {
    expect(
      cacheHitRate({
        input_tokens: 1000,
        cache_read_tokens: 990,
        total_tokens: 1100,
      }),
    ).toBe(99)
  })

  it("returns undefined without input tokens or cache data", () => {
    expect(cacheHitRate({ total_tokens: 0 })).toBeUndefined()
    expect(
      cacheHitRate({
        input_tokens: 0,
        cache_read_tokens: 10,
        total_tokens: 10,
      }),
    ).toBeUndefined()
  })
})
