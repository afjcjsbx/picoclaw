import { launcherFetch } from "@/api/http"

export interface SlashCommand {
  command: string
  description: string
  arg_hint?: string
}

interface SlashCommandsResponse {
  commands: SlashCommand[]
}

export async function listSlashCommands(): Promise<SlashCommand[]> {
  const res = await launcherFetch("/api/commands")
  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`)
  }
  const body = (await res.json()) as SlashCommandsResponse
  return body.commands
}
