import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback, useState } from "react"

import {
  type CronJob,
  deleteCronJob,
  getCronJobs,
  runCronJob,
  setCronJobEnabled,
} from "@/api/cron"

const CRON_JOBS_QUERY_KEY = ["cron", "jobs"] as const

export function useCronJobs() {
  const queryClient = useQueryClient()
  const [busyIds, setBusyIds] = useState<Set<string>>(() => new Set())
  const query = useQuery({
    queryKey: CRON_JOBS_QUERY_KEY,
    queryFn: getCronJobs,
    refetchInterval: 5000,
    refetchOnWindowFocus: true,
    retry: false,
  })

  const refresh = useCallback(() => query.refetch(), [query])
  const invalidate = useCallback(
    () =>
      queryClient.invalidateQueries({
        queryKey: CRON_JOBS_QUERY_KEY,
      }),
    [queryClient],
  )

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
        await setCronJobEnabled(job.id, !job.enabled)
        await invalidate()
      } finally {
        setJobBusy(job.id, false)
      }
    },
    [invalidate, setJobBusy],
  )

  const triggerJob = useCallback(
    async (job: CronJob) => {
      setJobBusy(job.id, true)
      try {
        await runCronJob(job.id)
        void invalidate()
        window.setTimeout(() => {
          void invalidate()
        }, 1500)
      } finally {
        setJobBusy(job.id, false)
      }
    },
    [invalidate, setJobBusy],
  )

  const removeJob = useCallback(
    async (job: CronJob) => {
      setJobBusy(job.id, true)
      try {
        await deleteCronJob(job.id)
        await invalidate()
      } finally {
        setJobBusy(job.id, false)
      }
    },
    [invalidate, setJobBusy],
  )

  return {
    jobs: query.data?.jobs ?? [],
    runtimeAvailable: query.data?.runtime_available ?? false,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    loadError:
      query.error instanceof Error
        ? query.error.message
        : query.error
          ? String(query.error)
          : null,
    busyIds,
    refresh,
    toggleJob,
    triggerJob,
    removeJob,
  }
}
