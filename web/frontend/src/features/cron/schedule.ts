import type { CronJob, CronRunRecord, CronSchedule } from "@/api/cron"

export interface ScheduleText {
  key: string
  values: Record<string, string>
}

const DURATION_UNITS: Array<[number, string]> = [
  [24 * 60 * 60 * 1000, "d"],
  [60 * 60 * 1000, "h"],
  [60 * 1000, "m"],
  [1000, "s"],
]

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "0s"
  if (ms < 1000) return `${Math.round(ms)}ms`

  let remaining = Math.floor(ms / 1000) * 1000
  const parts: string[] = []
  for (const [unit, label] of DURATION_UNITS) {
    const value = Math.floor(remaining / unit)
    if (value > 0) {
      parts.push(`${value}${label}`)
      remaining -= value * unit
      if (parts.length === 2) break
    }
  }
  return parts.join(" ")
}

export function describeCronSchedule(
  schedule: CronSchedule,
  formatDate: (ms: number) => string,
): ScheduleText {
  switch (schedule.kind) {
    case "at":
      return {
        key: "cron.scheduleAt",
        values: {
          time: schedule.atMs ? formatDate(schedule.atMs) : "-",
        },
      }
    case "every":
      return {
        key: "cron.scheduleEvery",
        values: {
          duration: formatDuration(schedule.everyMs ?? 0),
        },
      }
    case "cron":
      return schedule.tz
        ? {
            key: "cron.scheduleCronWithTz",
            values: { expr: schedule.expr ?? "", tz: schedule.tz },
          }
        : {
            key: "cron.scheduleCron",
            values: { expr: schedule.expr ?? "" },
          }
    default:
      return {
        key: "cron.scheduleUnknown",
        values: { kind: schedule.kind },
      }
  }
}

export function recentRuns(job: CronJob, limit = 5): CronRunRecord[] {
  const history = job.state.runHistory ?? []
  if (history.length > 0) {
    return history.slice(0, limit)
  }
  if (job.state.lastRunAtMs) {
    return [
      {
        runAtMs: job.state.lastRunAtMs,
        status: job.state.lastStatus || "ok",
        error: job.state.lastError,
        durationMs: 0,
      },
    ]
  }
  return []
}
