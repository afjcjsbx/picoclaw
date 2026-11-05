import { useCallback, useEffect, useRef, useState } from "react"

import {
  type CronJob,
  deleteCronJob,
  getCronJobs,
  runCronJob,
  setCronJobEnabled,
} from "@/api/cron"

const POLL_INTERVAL_MS = 15_000

export function useCronJobs({ enabled }: { enabled: boolean }) {
  const [jobs, setJobs] = useState<CronJob[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [busyIds, setBusyIds] = useState<Set<string>>(() => new Set())
  const requestIDRef = useRef(0)

  const refresh = useCallback(async (silent = false) => {
    const requestID = ++requestIDRef.current
    if (!silent) setIsLoading(true)
    try {
      const response = await getCronJobs()
      if (requestID !== requestIDRef.current) return
      setJobs(response.jobs)
      setLoadError(null)
    } catch (err) {
      if (requestID !== requestIDRef.current) return
      setLoadError(err instanceof Error ? err.message : String(err))
    } finally {
      if (requestID === requestIDRef.current) setIsLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    void refresh()
    const timer = window.setInterval(() => {
      void refresh(true)
    }, POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [enabled, refresh])

  const setJobBusy = useCallback((id: string, busy: boolean) => {
    setBusyIds((current) => {
      const next = new Set(current)
      if (busy) {
        next.add(id)
      } else {
        next.delete(id)
      }
      return next
    })
  }, [])

  const toggleJob = useCallback(
    async (job: CronJob) => {
      setJobBusy(job.id, true)
      try {
        const updated = await setCronJobEnabled(job.id, !job.enabled)
        setJobs((current) =>
          current.map((item) => (item.id === updated.id ? updated : item)),
        )
        setLoadError(null)
      } finally {
        setJobBusy(job.id, false)
      }
    },
    [setJobBusy],
  )

  const triggerJob = useCallback(
    async (job: CronJob) => {
      setJobBusy(job.id, true)
      try {
        await runCronJob(job.id)
        void refresh(true)
        window.setTimeout(() => {
          void refresh(true)
        }, 1500)
        setLoadError(null)
      } finally {
        setJobBusy(job.id, false)
      }
    },
    [refresh, setJobBusy],
  )

  const removeJob = useCallback(
    async (job: CronJob) => {
      setJobBusy(job.id, true)
      try {
        await deleteCronJob(job.id)
        setJobs((current) => current.filter((item) => item.id !== job.id))
        setLoadError(null)
        return true
      } finally {
        setJobBusy(job.id, false)
      }
    },
    [setJobBusy],
  )

  return {
    jobs,
    isLoading,
    loadError,
    busyIds,
    refresh,
    toggleJob,
    triggerJob,
    removeJob,
  }
}
