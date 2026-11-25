import { describe, expect, it } from "vitest"

import { validateModelSampling } from "./model-sampling"

describe("validateModelSampling", () => {
  it("treats empty fields as inheriting the global defaults", () => {
    expect(
      validateModelSampling({
        maxTokens: "",
        temperature: "",
        contextWindow: "",
      }),
    ).toEqual({
      ok: true,
      payload: { max_tokens: 0, temperature: null, context_window: 0 },
    })
  })

  it("parses explicit overrides", () => {
    expect(
      validateModelSampling({
        maxTokens: " 8192 ",
        temperature: "0.7",
        contextWindow: "131072",
      }),
    ).toEqual({
      ok: true,
      payload: { max_tokens: 8192, temperature: 0.7, context_window: 131072 },
    })
  })

  it("keeps an explicit temperature of 0", () => {
    expect(
      validateModelSampling({
        maxTokens: "",
        temperature: "0",
        contextWindow: "",
      }),
    ).toEqual({
      ok: true,
      payload: { max_tokens: 0, temperature: 0, context_window: 0 },
    })
  })

  it("rejects non-numeric values", () => {
    expect(
      validateModelSampling({
        maxTokens: "many",
        temperature: "",
        contextWindow: "",
      }),
    ).toEqual({ ok: false, field: "maxTokens", reason: "number" })
  })

  it("rejects negative values", () => {
    expect(
      validateModelSampling({
        maxTokens: "",
        temperature: "-0.1",
        contextWindow: "",
      }),
    ).toEqual({ ok: false, field: "temperature", reason: "negative" })
  })

  it("rejects fractional token values", () => {
    expect(
      validateModelSampling({
        maxTokens: "",
        temperature: "",
        contextWindow: "1.5",
      }),
    ).toEqual({ ok: false, field: "contextWindow", reason: "integer" })
  })
})
