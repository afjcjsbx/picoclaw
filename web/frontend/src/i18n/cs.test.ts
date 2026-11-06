import dayjs from "dayjs"
import { afterEach, describe, expect, it } from "vitest"

import i18n from "./index"
import cs from "./locales/cs.json"
import en from "./locales/en.json"

function flatten(
  value: Record<string, unknown>,
  prefix = "",
): Record<string, string> {
  return Object.fromEntries(
    Object.entries(value).flatMap(([key, entry]) => {
      const path = prefix ? `${prefix}.${key}` : key
      if (typeof entry === "string") return [[path, entry]]
      if (entry && typeof entry === "object" && !Array.isArray(entry)) {
        return Object.entries(flatten(entry as Record<string, unknown>, path))
      }
      throw new Error(`Unexpected locale value at ${path}`)
    }),
  )
}

function placeholders(value: string): string[] {
  return [...value.matchAll(/\{\{\s*([^}]+?)\s*\}\}/g)]
    .map((match) => match[1])
    .sort()
}

describe("Czech locale", () => {
  afterEach(async () => {
    await i18n.changeLanguage("en")
  })

  it("covers exactly the current English keys with non-empty strings", () => {
    const english = flatten(en)
    const czech = flatten(cs)
    expect(Object.keys(czech).sort()).toEqual(Object.keys(english).sort())
    for (const [key, value] of Object.entries(czech)) {
      expect(value.trim(), key).not.toBe("")
    }
  })

  it("preserves every interpolation variable from the English source", () => {
    const czech = flatten(cs)
    for (const [key, value] of Object.entries(flatten(en))) {
      expect(placeholders(czech[key]), key).toEqual(placeholders(value))
    }
  })

  it("resolves cs-CZ to Czech labels and date formatting", async () => {
    await i18n.changeLanguage("cs-CZ")
    expect(i18n.resolvedLanguage).toBe("cs")
    expect(i18n.t("chat.enableCodeWrap")).toBe("Zalamovat řádky")
    expect(i18n.t("pages.agent.mcp.catalog.title")).toBe("Katalog MCP")
    expect(i18n.t("cron.scheduleAt", { time: "12:30" })).toBe("Jednou v 12:30")
    expect(dayjs("2026-11-06T12:00:00").format("dddd")).toBe("pátek")
  })

  it("retains English fallback for unsupported browser languages", async () => {
    await i18n.changeLanguage("de-DE")
    expect(i18n.resolvedLanguage).toBe("en")
    expect(i18n.t("chat.enableCodeWrap")).toBe("Wrap lines")
    expect(dayjs.locale()).toBe("en")
  })

  it("retains Chinese regional language and date formatting", async () => {
    await i18n.changeLanguage("zh-CN")
    expect(i18n.resolvedLanguage).toBe("zh")
    expect(dayjs.locale()).toBe("zh-cn")
  })
})
