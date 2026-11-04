import { beforeEach, describe, expect, it, vi } from "vitest"

import {
  ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
  DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
  LEGACY_SHOW_ASSISTANT_DETAILS_STORAGE_KEY,
  assistantDetailVisibilityStorage,
  resolveAssistantDetailVisibilityPreference,
  shouldShowAssistantMessage,
  syncAssistantDetailVisibilityStorage,
} from "./detail-visibility"

function memoryStorage(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial))

  return {
    data,
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => {
      data.set(key, value)
    },
    removeItem: (key: string) => {
      data.delete(key)
    },
  }
}

beforeEach(() => {
  localStorage.clear()
})

describe("resolveAssistantDetailVisibilityPreference", () => {
  it("defaults to all when nothing is stored", () => {
    expect(resolveAssistantDetailVisibilityPreference(null, null)).toEqual({
      value: DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
      newValueAction: "keep",
      removeLegacyValue: false,
    })
  })

  it("keeps an already canonical value untouched", () => {
    expect(
      resolveAssistantDetailVisibilityPreference('"thought"', null),
    ).toEqual({
      value: "thought",
      newValueAction: "keep",
      removeLegacyValue: false,
    })
  })

  it("normalizes a raw stored value and rewrites it", () => {
    expect(
      resolveAssistantDetailVisibilityPreference("TOOL_CALLS", null),
    ).toEqual({
      value: "tool_calls",
      newValueAction: "write",
      removeLegacyValue: false,
    })
  })

  it("migrates the legacy boolean preference", () => {
    expect(resolveAssistantDetailVisibilityPreference(null, "true")).toEqual({
      value: "all",
      newValueAction: "write",
      removeLegacyValue: true,
    })
    expect(resolveAssistantDetailVisibilityPreference(null, "false")).toEqual({
      value: "none",
      newValueAction: "write",
      removeLegacyValue: true,
    })
  })

  it("prefers the canonical value over the legacy one", () => {
    expect(
      resolveAssistantDetailVisibilityPreference('"none"', "true"),
    ).toEqual({
      value: "none",
      newValueAction: "keep",
      removeLegacyValue: true,
    })
  })

  it("removes an invalid canonical value", () => {
    expect(resolveAssistantDetailVisibilityPreference('"bogus"', null)).toEqual(
      {
        value: "all",
        newValueAction: "remove",
        removeLegacyValue: false,
      },
    )
  })

  it("removes an invalid legacy value without touching the canonical one", () => {
    expect(resolveAssistantDetailVisibilityPreference(null, "maybe")).toEqual({
      value: "all",
      newValueAction: "keep",
      removeLegacyValue: true,
    })
  })
})

describe("syncAssistantDetailVisibilityStorage", () => {
  it("migrates the legacy preference and removes the old key", () => {
    const storage = memoryStorage({
      [LEGACY_SHOW_ASSISTANT_DETAILS_STORAGE_KEY]: "true",
    })

    expect(syncAssistantDetailVisibilityStorage(storage)).toBe("all")
    expect(storage.data.get(ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY)).toBe(
      '"all"',
    )
    expect(storage.data.has(LEGACY_SHOW_ASSISTANT_DETAILS_STORAGE_KEY)).toBe(
      false,
    )
  })

  it("removes an invalid canonical value", () => {
    const storage = memoryStorage({
      [ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY]: '"bogus"',
    })

    expect(syncAssistantDetailVisibilityStorage(storage)).toBe("all")
    expect(storage.data.has(ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY)).toBe(
      false,
    )
  })

  it("falls back to the default when storage access throws", () => {
    const storage = {
      getItem: () => {
        throw new Error("denied")
      },
      setItem: vi.fn(),
      removeItem: vi.fn(),
    }

    expect(syncAssistantDetailVisibilityStorage(storage)).toBe(
      DEFAULT_ASSISTANT_DETAIL_VISIBILITY,
    )
  })
})

describe("assistantDetailVisibilityStorage", () => {
  it("writes canonical values and clears the legacy key", () => {
    localStorage.setItem(LEGACY_SHOW_ASSISTANT_DETAILS_STORAGE_KEY, "true")

    assistantDetailVisibilityStorage.setItem(
      ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
      "tool_calls",
    )

    expect(localStorage.getItem(ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY)).toBe(
      '"tool_calls"',
    )
    expect(
      localStorage.getItem(LEGACY_SHOW_ASSISTANT_DETAILS_STORAGE_KEY),
    ).toBeNull()
  })

  it("reads the migrated preference", () => {
    localStorage.setItem(LEGACY_SHOW_ASSISTANT_DETAILS_STORAGE_KEY, "false")

    expect(assistantDetailVisibilityStorage.getItem()).toBe("none")
  })

  it("removes the stored preference", () => {
    assistantDetailVisibilityStorage.setItem(
      ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
      "all",
    )

    assistantDetailVisibilityStorage.removeItem(
      ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY,
    )

    expect(
      localStorage.getItem(ASSISTANT_DETAIL_VISIBILITY_STORAGE_KEY),
    ).toBeNull()
  })
})

describe("shouldShowAssistantMessage", () => {
  it("always shows regular and tool feedback messages", () => {
    for (const visibility of [
      "none",
      "thought",
      "tool_calls",
      "all",
    ] as const) {
      expect(shouldShowAssistantMessage(visibility, "normal")).toBe(true)
      expect(shouldShowAssistantMessage(visibility, "tool_feedback")).toBe(true)
      expect(shouldShowAssistantMessage(visibility, undefined)).toBe(true)
    }
  })

  it("hides all details for none", () => {
    expect(shouldShowAssistantMessage("none", "thought")).toBe(false)
    expect(shouldShowAssistantMessage("none", "tool_calls")).toBe(false)
  })

  it("shows only the selected detail kind", () => {
    expect(shouldShowAssistantMessage("thought", "thought")).toBe(true)
    expect(shouldShowAssistantMessage("thought", "tool_calls")).toBe(false)
    expect(shouldShowAssistantMessage("tool_calls", "thought")).toBe(false)
    expect(shouldShowAssistantMessage("tool_calls", "tool_calls")).toBe(true)
  })

  it("shows every detail for all", () => {
    expect(shouldShowAssistantMessage("all", "thought")).toBe(true)
    expect(shouldShowAssistantMessage("all", "tool_calls")).toBe(true)
  })
})
