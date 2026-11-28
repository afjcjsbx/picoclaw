import { launcherFetch } from "@/api/http"

export interface Totals {
  requests?: number
  turns?: number
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
  cache_read_tokens?: number
  cache_creation_tokens?: number
  reasoning_tokens?: number
  cost_usd?: number
}

export interface ModelTotals extends Totals {
  model: string
}

export interface SessionRow {
  key: string
  label: string
  usage: Totals & { models?: ModelTotals[] }
}

export interface DailyRow extends Totals {
  date: string
}

export interface UsagePeriod {
  days: number
  since: string
  tracked_since?: string
  totals: Totals
  models: ModelTotals[]
  daily: DailyRow[]
}

export interface UsageResponse {
  sessions_dir: string
  session_count: number
  totals: Totals
  models: ModelTotals[]
  sessions: SessionRow[]
  period?: UsagePeriod
}

export async function getUsage(days: number = 30): Promise<UsageResponse> {
  const res = await launcherFetch(`/api/usage?days=${encodeURIComponent(days)}`)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { error?: string; message?: string }
      if (typeof body.message === "string" && body.message.trim() !== "") {
        message = body.message
      } else if (typeof body.error === "string" && body.error.trim() !== "") {
        message = body.error
      }
    } catch {
      // keep default message
    }
    throw new Error(message)
  }
  return res.json() as Promise<UsageResponse>
}
