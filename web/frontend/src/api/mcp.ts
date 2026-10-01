import { launcherFetch } from "@/api/http"

export type MCPConnectionStatus =
  | "connected"
  | "error"
  | "disconnected"
  | "connecting"
  | "disabled"

export interface MCPToolParameter {
  name: string
  type?: string
  required: boolean
  description?: string
}

export interface MCPTool {
  name: string
  description?: string
  parameters: MCPToolParameter[]
}

export interface MCPServer {
  name: string
  enabled: boolean
  configured_enabled: boolean
  deferred: boolean
  transport: string
  status: MCPConnectionStatus
  command?: string
  launch_command?: string
  args: string[]
  error?: string
  tool_count: number
  tools: MCPTool[]
}

export interface MCPDashboardResponse {
  enabled: boolean
  runtime_available: boolean
  servers: MCPServer[]
}

const REDACTED = "********"

export function redactMCPText(value: string | undefined): string {
  if (!value) return ""
  return value
    .replace(
      /((?:api[_-]?key|token|secret|auth(?:orization)?|pass(?:word)?|pwd|credential|dsn|database[_-]?url|db[_-]?(?:url|uri)|connection[_-]?(?:string|uri)|postgres(?:ql)?[_-]?url|mongo(?:db)?[_-]?uri|redis[_-]?url)\s*[=:]\s*)([^\s,;&]+)/gi,
      `$1${REDACTED}`,
    )
    .replace(
      /(--[^\s=]*(?:key|token|secret|auth|pass|pwd|credential|dsn|database[_-]?url|db[_-]?(?:url|uri)|connection[_-]?(?:string|uri)|postgres(?:ql)?[_-]?url|mongo(?:db)?[_-]?uri|redis[_-]?url)[^\s=]*(?:=|\s+))([^\s]+)/gi,
      `$1${REDACTED}`,
    )
    .replace(
      /\b(?:sk-[a-z0-9_-]{4,}|gh[pousr]_[a-z0-9_]{8,}|bearer\s+[a-z0-9._~+/=-]{8,})\b/gi,
      REDACTED,
    )
    .replace(
      /(?:jdbc:)?(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis|rediss|mssql|sqlserver):\/\/[^\s]+/gi,
      REDACTED,
    )
}

function sanitizeResponse(data: MCPDashboardResponse): MCPDashboardResponse {
  return {
    ...data,
    servers: (data.servers ?? []).map((server) => ({
      ...server,
      name: redactMCPText(server.name),
      transport: redactMCPText(server.transport),
      command: redactMCPText(server.command),
      launch_command: redactMCPText(server.launch_command),
      args: (server.args ?? []).map(redactMCPText),
      error: redactMCPText(server.error),
      tools: (server.tools ?? []).map((tool) => ({
        ...tool,
        name: redactMCPText(tool.name),
        description: redactMCPText(tool.description),
        parameters: (tool.parameters ?? []).map((parameter) => ({
          ...parameter,
          name: redactMCPText(parameter.name),
          type: redactMCPText(parameter.type),
          description: redactMCPText(parameter.description),
        })),
      })),
    })),
  }
}

export async function getMCPDashboard(): Promise<MCPDashboardResponse> {
  const response = await launcherFetch("/api/agents/mcp")
  if (!response.ok) {
    throw new Error(`API error: ${response.status} ${response.statusText}`)
  }
  return sanitizeResponse((await response.json()) as MCPDashboardResponse)
}

async function patchMCPServers(
  servers: Record<string, Record<string, unknown> | null>,
): Promise<void> {
  const response = await launcherFetch("/api/config", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tools: { mcp: { servers } } }),
  })
  if (response.ok) return

  const detail = await response.text().catch(() => "")
  throw new Error(
    detail || `API error: ${response.status} ${response.statusText}`,
  )
}

export function updateMCPServerCommand(
  name: string,
  command: string,
  args: string[],
): Promise<void> {
  return patchMCPServers({ [name]: { command, args } })
}

export function updateMCPServerSettings(
  name: string,
  settings: { enabled?: boolean; deferred?: boolean },
): Promise<void> {
  return patchMCPServers({ [name]: settings })
}

export function createMCPServer(
  name: string,
  server: {
    enabled: boolean
    deferred: boolean
    type: "stdio"
    command: string
    args: string[]
  },
): Promise<void> {
  return patchMCPServers({ [name]: server })
}

export function removeMCPServer(name: string): Promise<void> {
  return patchMCPServers({ [name]: null })
}
