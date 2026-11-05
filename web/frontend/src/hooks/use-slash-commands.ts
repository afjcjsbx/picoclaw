import { useEffect, useState } from "react"

import { type SlashCommand, listSlashCommands } from "@/api/commands"

export function useSlashCommands(): SlashCommand[] {
  const [commands, setCommands] = useState<SlashCommand[]>([])

  useEffect(() => {
    let cancelled = false
    void listSlashCommands()
      .then((next) => {
        if (!cancelled) setCommands(next)
      })
      .catch((error) => {
        console.error("Failed to load slash commands:", error)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return commands
}
