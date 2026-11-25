import { launcherFetch } from "@/api/http"

export interface CronSchedule {
  kind: "at" | "every" | "cron" | string
  atMs?: number
  everyMs?: number
  expr?: string
  tz?: string
}

export interface CronPayload {
  message: string
  command?: string
  channel?: string
  to?: string
}

export interface CronRunRecord {
  runAtMs: number
  status: string
  error?: string
  durationMs: number
}

export interface CronJobState {
  nextRunAtMs?: number
  lastRunAtMs?: number
  lastStatus?: string
  lastError?: string
  runHistory?: CronRunRecord[]
}

export interface CronJob {
  id: string
  name: string
  enabled: boolean
  schedule: CronSchedule
  payload: CronPayload
  state: CronJobState
  createdAtMs: number
  updatedAtMs: number
  deleteAfterRun: boolean
}

export interface CronJobsResponse {
  jobs: CronJob[]
  runtime_available: boolean
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
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
      // Keep the default message when the response is not JSON.
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export function getCronJobs(): Promise<CronJobsResponse> {
  return request<CronJobsResponse>("/api/cron/jobs")
}

export function setCronJobEnabled(
  id: string,
  enabled: boolean,
): Promise<CronJob> {
  return request<CronJob>(`/api/cron/jobs/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ enabled }),
  })
}

export function runCronJob(id: string): Promise<{ status: string }> {
  return request<{ status: string }>(
    `/api/cron/jobs/${encodeURIComponent(id)}/run`,
    { method: "POST" },
  )
}

export function deleteCronJob(id: string): Promise<{ status: string }> {
  return request<{ status: string }>(
    `/api/cron/jobs/${encodeURIComponent(id)}`,
    { method: "DELETE" },
  )
}
