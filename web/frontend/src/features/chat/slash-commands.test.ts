import {
  type SlashPaletteEntry,
  filterSlashPaletteItems,
  nextSlashRecents,
  slashCommandInsertion,
  slashQueryFromInput,
} from "./slash-commands.ts"

function expectEqual(actual: unknown, expected: unknown) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(
      `Expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`,
    )
  }
}

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

// slashQueryFromInput: only a single leading "/" token opens the palette.
expectEqual(slashQueryFromInput(""), null)
expectEqual(slashQueryFromInput("hello /help"), null)
expectEqual(slashQueryFromInput("/"), "")
expectEqual(slashQueryFromInput("/HeL"), "hel")
expectEqual(slashQueryFromInput("/help "), null)
expectEqual(slashQueryFromInput("/btw what"), null)

// filterSlashPaletteItems: empty query keeps all entries (catalog order).
expectEqual(
  filterSlashPaletteItems(ENTRIES, "", []).map((item) => item.command),
  ["/help", "/btw", "/clear", "/voice"],
)

// Non-empty query matches command, title, description and argument hint.
expectEqual(
  filterSlashPaletteItems(ENTRIES, "cl", []).map((item) => item.command),
  ["/clear"],
)
expectEqual(
  filterSlashPaletteItems(ENTRIES, "tts", []).map((item) => item.command),
  ["/voice"],
)
expectEqual(
  filterSlashPaletteItems(ENTRIES, "question", []).map((item) => item.command),
  ["/btw"],
)
expectEqual(filterSlashPaletteItems(ENTRIES, "nope", []), [])

// Recent commands float to the top of the blank menu and are flagged.
expectEqual(
  filterSlashPaletteItems(ENTRIES, "", ["/voice", "/clear"]).map((item) => [
    item.command,
    item.recent,
  ]),
  [
    ["/voice", true],
    ["/clear", true],
    ["/help", false],
    ["/btw", false],
  ],
)

// A typed query keeps catalog order even when recents exist.
expectEqual(
  filterSlashPaletteItems(ENTRIES, "e", ["/voice"]).map((item) => item.command),
  ["/help", "/btw", "/clear", "/voice"],
)

// The menu is capped to 8 visible entries.
const MANY: SlashPaletteEntry[] = Array.from({ length: 12 }, (_, index) => ({
  command: `/cmd${index}`,
  title: `Cmd ${index}`,
  description: "",
  argHint: "",
}))
expectEqual(filterSlashPaletteItems(MANY, "", []).length, 8)

// nextSlashRecents: most recent first, no duplicates, capped to 5.
expectEqual(nextSlashRecents([], "/help"), ["/help"])
expectEqual(nextSlashRecents(["/help", "/btw"], "/btw"), ["/btw", "/help"])
expectEqual(nextSlashRecents(["/a", "/b", "/c", "/d", "/e"], "/f"), [
  "/f",
  "/a",
  "/b",
  "/c",
  "/d",
])

// slashCommandInsertion: arguments get a trailing space.
expectEqual(slashCommandInsertion(ENTRIES[0]), "/help")
expectEqual(slashCommandInsertion(ENTRIES[1]), "/btw ")
