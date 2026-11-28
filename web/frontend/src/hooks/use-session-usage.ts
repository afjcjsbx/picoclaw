import { useCallback, useEffect, useState } from "react"

import { type SessionUsageSummary, getSessionUsage } from "@/api/sessions"

interface SessionUsageState {
  summary?: SessionUsageSummary
  refresh: () => void
}

/**
 * Loads the cumulative token usage of a stored session. The summary is
 * refreshed whenever refreshKey changes or refresh() is called, which lets the
 * composer update the context popover after a turn or when it opens.
 */
export function useSessionUsage(
  sessionId: string,
  refreshKey?: unknown,
): SessionUsageState {
  const [summary, setSummary] = useState<SessionUsageSummary>()
  const [refreshTick, setRefreshTick] = useState(0)

  const refresh = useCallback(() => {
    setRefreshTick((tick) => tick + 1)
  }, [])

  useEffect(() => {
    setSummary(undefined)
  }, [sessionId])

  useEffect(() => {
    if (!sessionId) return

    const controller = new AbortController()
    getSessionUsage(sessionId, controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) {
          setSummary(next)
        }
      })
      .catch((error) => {
        if (controller.signal.aborted) return
        console.error("Failed to load session usage:", error)
      })

    return () => {
      controller.abort()
    }
  }, [sessionId, refreshKey, refreshTick])

  return { summary, refresh }
}
