export const SLASH_RECENTS_STORAGE_KEY = "picoclaw.webui.slashCommandRecents"
export const SLASH_RECENTS_LIMIT = 5
export const SLASH_MENU_LIMIT = 8

export interface SlashPaletteEntry {
  command: string
  title: string
  description: string
  argHint: string
}

export interface SlashPaletteItem extends SlashPaletteEntry {
  recent: boolean
}

// i18n key segment for a command: "/list" -> "list", "/foo-bar" -> "foo_bar".
export function slashCommandI18nKey(command: string): string {
  return command.replace(/^\//, "").replace(/-/g, "_")
}

// Used when no translation exists: "/clear" -> "Clear".
export function slashCommandFallbackTitle(command: string): string {
  const name = slashCommandI18nKey(command)
  return name.charAt(0).toUpperCase() + name.slice(1)
}

// Returns the text typed after a leading "/" while the input is still one token.
// Returns null once the user has typed whitespace (the command is complete).
export function slashQueryFromInput(input: string): string | null {
  if (!input.startsWith("/")) return null
  const token = input.slice(1)
  if (/\s/.test(token)) return null
  return token.toLowerCase()
}

export function readSlashRecents(): string[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.localStorage.getItem(SLASH_RECENTS_STORAGE_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed)
      ? parsed
          .filter((item): item is string => typeof item === "string")
          .slice(0, SLASH_RECENTS_LIMIT)
      : []
  } catch {
    return []
  }
}

export function storeSlashRecents(commands: string[]): void {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(
      SLASH_RECENTS_STORAGE_KEY,
      JSON.stringify(commands.slice(0, SLASH_RECENTS_LIMIT)),
    )
  } catch {
    // Storage can be unavailable (private mode); command insertion still works.
  }
}

export function nextSlashRecents(recents: string[], command: string): string[] {
  return [command, ...recents.filter((item) => item !== command)].slice(
    0,
    SLASH_RECENTS_LIMIT,
  )
}

// Filters the palette entries by the typed query. An empty query lists recently
// used commands first; a non-empty query keeps the catalog order.
export function filterSlashPaletteItems(
  entries: SlashPaletteEntry[],
  query: string,
  recents: string[],
): SlashPaletteItem[] {
  const matches = entries.filter((entry) => {
    const haystack = [
      entry.command,
      entry.argHint,
      entry.title,
      entry.description,
    ]
      .join(" ")
      .toLowerCase()
    return haystack.includes(query)
  })

  if (query === "") {
    matches.sort((a, b) => {
      const aRecent = recents.indexOf(a.command)
      const bRecent = recents.indexOf(b.command)
      if (aRecent === -1 && bRecent === -1) return 0
      if (aRecent === -1) return 1
      if (bRecent === -1) return -1
      return aRecent - bRecent
    })
  }

  return matches.slice(0, SLASH_MENU_LIMIT).map((entry) => ({
    ...entry,
    recent: recents.includes(entry.command),
  }))
}

// Commands that take arguments are completed with a trailing space so the user
// can keep typing; bare commands are inserted as-is.
export function slashCommandInsertion(entry: SlashPaletteEntry): string {
  return entry.argHint ? `${entry.command} ` : entry.command
}
