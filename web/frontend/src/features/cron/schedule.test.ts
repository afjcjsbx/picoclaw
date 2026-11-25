import { describe, expect, it } from "vitest"

import type { CronJob } from "@/api/cron"
import {
  describeCronSchedule,
  formatDuration,
  recentRuns,
} from "@/features/cron/schedule"

const formatDate = (ms: number) => new Date(ms).toISOString()

function makeJob(overrides: Partial<CronJob> = {}): CronJob {
  return {
    id: "job-1",
    name: "Daily",
    enabled: true,
    schedule: { kind: "every", everyMs: 5 * 60 * 1000 },
    payload: { message: "hello" },
    state: {},
    createdAtMs: 0,
    updatedAtMs: 0,
    deleteAfterRun: false,
    ...overrides,
  }
}

describe("formatDuration", () => {
  it("formats milliseconds, seconds, minutes and hours", () => {
    expect(formatDuration(0)).toBe("0s")
    expect(formatDuration(500)).toBe("500ms")
    expect(formatDuration(5_000)).toBe("5s")
    expect(formatDuration(90_000)).toBe("1m 30s")
    expect(formatDuration(3_600_000)).toBe("1h")
    expect(formatDuration(3_900_000)).toBe("1h 5m")
    expect(formatDuration(86_400_000)).toBe("1d")
  })
})

describe("describeCronSchedule", () => {
  it("describes every and cron schedules", () => {
    expect(
      describeCronSchedule({ kind: "every", everyMs: 60_000 }, formatDate),
    ).toEqual({
      key: "cron.scheduleEvery",
      values: { duration: "1m" },
    })
    expect(
      describeCronSchedule({ kind: "cron", expr: "0 9 * * *" }, formatDate),
    ).toEqual({
      key: "cron.scheduleCron",
      values: { expr: "0 9 * * *" },
    })
    expect(
      describeCronSchedule(
        { kind: "cron", expr: "0 9 * * *", tz: "Europe/Rome" },
        formatDate,
      ),
    ).toEqual({
      key: "cron.scheduleCronWithTz",
      values: { expr: "0 9 * * *", tz: "Europe/Rome" },
    })
  })

  it("formats one-time schedules with the supplied date formatter", () => {
    const atMs = Date.UTC(2026, 0, 2, 3, 4, 5)
    expect(describeCronSchedule({ kind: "at", atMs }, formatDate)).toEqual({
      key: "cron.scheduleAt",
      values: { time: "2026-01-02T03:04:05.000Z" },
    })
  })
})

describe("recentRuns", () => {
  it("prefers the stored history and limits it", () => {
    const job = makeJob({
      state: {
        runHistory: [
          { runAtMs: 3, status: "ok", durationMs: 10 },
          { runAtMs: 2, status: "error", error: "boom", durationMs: 20 },
          { runAtMs: 1, status: "ok", durationMs: 30 },
        ],
      },
    })

    expect(recentRuns(job, 2).map((run) => run.runAtMs)).toEqual([3, 2])
  })

  it("falls back to the last run for older jobs", () => {
    const job = makeJob({
      state: { lastRunAtMs: 42, lastStatus: "error", lastError: "boom" },
    })

    expect(recentRuns(job)).toEqual([
      { runAtMs: 42, status: "error", error: "boom", durationMs: 0 },
    ])
  })

  it("returns an empty list when the job never ran", () => {
    expect(recentRuns(makeJob())).toEqual([])
  })
})
