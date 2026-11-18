import { launcherFetch } from "@/api/http"

export interface SubagentToolSettings {
  subagent: boolean
  spawn: boolean
  spawn_status: boolean
}

export interface SubagentLimits {
  max_depth: number
  max_concurrent: number
  concurrency_timeout_sec: number
  default_timeout_minutes: number
  default_token_budget: number
}

export interface SubagentModelOverride {
  primary: string
  fallbacks?: string[]
}

export interface SubagentAgent {
  id: string
  name?: string
  default: boolean
  implicit?: boolean
  workspace: string
  model?: string
  model_fallbacks?: string[]
  allow_agents: string[]
  subagent_model?: SubagentModelOverride
}

export interface SubagentDashboard {
  tools: SubagentToolSettings
  limits: SubagentLimits
  agents: SubagentAgent[]
  configured: boolean
}

export interface SubagentAgentUpdate {
  id: string
  allow_agents: string[]
  subagent_model?: SubagentModelOverride
}

export interface SubagentDashboardUpdate {
  tools: SubagentToolSettings
  limits: SubagentLimits
  agents: SubagentAgentUpdate[]
}

export interface SubagentActiveTask {
  turn_id: string
  agent_id: string
  agent_name?: string
  parent_turn_id?: string
  session_key?: string
  channel?: string
  chat_id?: string
  task?: string
  phase: string
  iteration: number
  depth: number
  started_at?: string
}

export interface SubagentActiveResponse {
  runtime_available: boolean
  active: SubagentActiveTask[]
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as {
        error?: string
        errors?: string[]
      }
      if (Array.isArray(body.errors) && body.errors.length > 0) {
        message = body.errors.join("; ")
      } else if (typeof body.error === "string" && body.error.trim() !== "") {
        message = body.error
      }
    } catch {
      // ignore invalid body
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export function getSubagents(): Promise<SubagentDashboard> {
  return request<SubagentDashboard>("/api/agents/subagents")
}

export function getActiveSubagents(): Promise<SubagentActiveResponse> {
  return request<SubagentActiveResponse>("/api/agents/subagents/active")
}

export function updateSubagents(
  payload: SubagentDashboardUpdate,
): Promise<SubagentDashboard> {
  return request<SubagentDashboard>("/api/agents/subagents", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
}
