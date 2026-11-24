import { describe, expect, it } from "vitest"

import { EMPTY_FORM, buildFormFromConfig } from "./form-model"

describe("buildFormFromConfig auxiliary models", () => {
  it("reads compaction and vision model names", () => {
    const form = buildFormFromConfig({
      agents: {
        defaults: {
          auxiliary: {
            compaction: "cheap-model",
            vision: "vision-model",
          },
        },
      },
    })

    expect(form.auxiliaryCompaction).toBe("cheap-model")
    expect(form.auxiliaryVision).toBe("vision-model")
  })

  it("defaults missing auxiliary slots to empty", () => {
    const form = buildFormFromConfig({
      agents: { defaults: {} },
    })

    expect(form.auxiliaryCompaction).toBe(EMPTY_FORM.auxiliaryCompaction)
    expect(form.auxiliaryVision).toBe(EMPTY_FORM.auxiliaryVision)
  })

  it("ignores non-string auxiliary values", () => {
    const form = buildFormFromConfig({
      agents: {
        defaults: {
          auxiliary: {
            compaction: 42,
            vision: ["not-a-model"],
          },
        },
      },
    })

    expect(form.auxiliaryCompaction).toBe("")
    expect(form.auxiliaryVision).toBe("")
  })
})
