import { describe, expect, it } from "vitest"

import {
  asStringArray,
  mergeUniqueStringItems,
  normalizeAllowFromValues,
  parseAllowFromInput,
  parseConservativeStringListInput,
  serializeStringArrayForSubmit,
} from "./channel-array-utils"

describe("asStringArray", () => {
  it("keeps only string items", () => {
    expect(asStringArray([1, "a", null, "b", undefined])).toEqual(["a", "b"])
  })

  it("returns an empty array for non-array values", () => {
    expect(asStringArray("a")).toEqual([])
    expect(asStringArray(undefined)).toEqual([])
  })
})

describe("parseAllowFromInput", () => {
  it("splits on ascii and cjk separators", () => {
    expect(
      parseAllowFromInput("alice, bob，carol、dave；erin;frank\n"),
    ).toEqual(["alice", "bob", "carol", "dave", "erin", "frank"])
  })

  it("trims items, drops empties and removes duplicates", () => {
    expect(parseAllowFromInput("  a , b ,, a ,\t")).toEqual(["a", "b"])
  })

  it("strips zero-width and bidi control characters", () => {
    expect(parseAllowFromInput("ali\u200bce\u200f, bob\ufeff")).toEqual([
      "alice",
      "bob",
    ])
  })

  it("returns an empty array for blank input", () => {
    expect(parseAllowFromInput("  ")).toEqual([])
  })
})

describe("parseConservativeStringListInput", () => {
  it("only splits on commas and newlines", () => {
    expect(parseConservativeStringListInput("a;b、c，d\ne")).toEqual([
      "a;b、c",
      "d",
      "e",
    ])
  })

  it("keeps hidden characters untouched", () => {
    expect(parseConservativeStringListInput("a\u200bb")).toEqual(["a\u200bb"])
  })
})

describe("normalizeAllowFromValues", () => {
  it("normalizes stored arrays", () => {
    expect(normalizeAllowFromValues([" a ", "a", "\u200bb", 7])).toEqual([
      "a",
      "b",
    ])
  })

  it("returns an empty array for non-array values", () => {
    expect(normalizeAllowFromValues("a")).toEqual([])
  })
})

describe("mergeUniqueStringItems", () => {
  it("appends new items without duplicating existing ones", () => {
    expect(mergeUniqueStringItems(["a", "b"], ["b", "c", " a "])).toEqual([
      "a",
      "b",
      "c",
    ])
  })
})

describe("serializeStringArrayForSubmit", () => {
  it("joins normalized items with newlines", () => {
    expect(serializeStringArrayForSubmit(["a", " ", "b", "a"])).toBe("a\nb")
  })

  it("passes non-array values through unchanged", () => {
    expect(serializeStringArrayForSubmit("a,b")).toBe("a,b")
    expect(serializeStringArrayForSubmit(null)).toBeNull()
  })
})
