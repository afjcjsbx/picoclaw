import { describe, expect, it } from "vitest"

import {
  type SlashPaletteEntry,
  filterSlashPaletteItems,
  nextSlashRecents,
  slashCommandInsertion,
  slashQueryFromInput,
} from "./slash-commands"

const ENTRIES: SlashPaletteEntry[] = [
  {
    command: "/help",
    title: "Help",
    description: "Show this help message",
    argHint: "",
  },
  {
    command: "/btw",
    title: "Side question",
    description: "Ask a side question",
    argHint: "<question>",
  },
  {
    command: "/clear",
    title: "Clear chat",
    description: "Clear the chat history",
    argHint: "",
  },
  {
    command: "/voice",
    title: "Voice",
    description: "Set automatic text-to-speech mode",
    argHint: "[off|on|tts|status]",
  },
]

const MANY: SlashPaletteEntry[] = Array.from({ length: 12 }, (_, index) => ({
  command: `/cmd${index}`,
  title: `Cmd ${index}`,
  description: "",
  argHint: "",
}))

describe("slashQueryFromInput", () => {
  it("only opens the palette for a single leading slash token", () => {
    expect(slashQueryFromInput("")).toBeNull()
    expect(slashQueryFromInput("hello /help")).toBeNull()
    expect(slashQueryFromInput("/")).toBe("")
    expect(slashQueryFromInput("/HeL")).toBe("hel")
    expect(slashQueryFromInput("/help ")).toBeNull()
    expect(slashQueryFromInput("/btw what")).toBeNull()
  })
})

describe("filterSlashPaletteItems", () => {
  it("keeps all entries in catalog order for an empty query", () => {
    expect(
      filterSlashPaletteItems(ENTRIES, "", []).map((item) => item.command),
    ).toEqual(["/help", "/btw", "/clear", "/voice"])
  })

  it("matches command, title, description and argument hint", () => {
    expect(
      filterSlashPaletteItems(ENTRIES, "cl", []).map((item) => item.command),
    ).toEqual(["/clear"])
    expect(
      filterSlashPaletteItems(ENTRIES, "tts", []).map((item) => item.command),
    ).toEqual(["/voice"])
    expect(
      filterSlashPaletteItems(ENTRIES, "question", []).map(
        (item) => item.command,
      ),
    ).toEqual(["/btw"])
    expect(filterSlashPaletteItems(ENTRIES, "nope", [])).toEqual([])
  })

  it("floats recent commands to the top of the blank menu and flags them", () => {
    expect(
      filterSlashPaletteItems(ENTRIES, "", ["/voice", "/clear"]).map((item) => [
        item.command,
        item.recent,
      ]),
    ).toEqual([
      ["/voice", true],
      ["/clear", true],
      ["/help", false],
      ["/btw", false],
    ])
  })

  it("keeps catalog order for a typed query even when recents exist", () => {
    expect(
      filterSlashPaletteItems(ENTRIES, "e", ["/voice"]).map(
        (item) => item.command,
      ),
    ).toEqual(["/help", "/btw", "/clear", "/voice"])
  })

  it("caps the menu to eight visible entries", () => {
    expect(filterSlashPaletteItems(MANY, "", [])).toHaveLength(8)
  })
})

describe("nextSlashRecents", () => {
  it("puts the most recent command first without duplicates", () => {
    expect(nextSlashRecents([], "/help")).toEqual(["/help"])
    expect(nextSlashRecents(["/help", "/btw"], "/btw")).toEqual([
      "/btw",
      "/help",
    ])
  })

  it("caps the recent list to five entries", () => {
    expect(nextSlashRecents(["/a", "/b", "/c", "/d", "/e"], "/f")).toEqual([
      "/f",
      "/a",
      "/b",
      "/c",
      "/d",
    ])
  })
})

describe("slashCommandInsertion", () => {
  it("adds a trailing space only when the command takes arguments", () => {
    expect(slashCommandInsertion(ENTRIES[0])).toBe("/help")
    expect(slashCommandInsertion(ENTRIES[1])).toBe("/btw ")
  })
})
