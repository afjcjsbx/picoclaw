import { useAtomValue } from "jotai"
import { useCallback, useEffect, useRef, useState } from "react"

import { clearGatewayLogs, getGatewayLogs } from "@/api/gateway"
import { gatewayAtom } from "@/store/gateway"

export function useGatewayLogs() {
  const [logs, setLogs] = useState<string[]>([])
  const [clearing, setClearing] = useState(false)
  const [hasOlder, setHasOlder] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [logStart, setLogStart] = useState(0)
  const logOffsetRef = useRef(0)
  const logStartRef = useRef(0)
  const logRunIdRef = useRef(-1)
  const syncTokenRef = useRef(0)
  const hasOlderRef = useRef(false)
  const loadingOlderRef = useRef(false)

  const gateway = useAtomValue(gatewayAtom)

  const clearLogs = async () => {
    setClearing(true)
    try {
      const data = await clearGatewayLogs()
      syncTokenRef.current += 1
      setLogs([])
      logOffsetRef.current = data.log_total ?? 0
      logStartRef.current = 0
      setLogStart(0)
      hasOlderRef.current = false
      setHasOlder(false)
      if (data.log_run_id !== undefined) {
        logRunIdRef.current = data.log_run_id
      }
    } catch {
      // Ignore clear failures silently to avoid noisy transient errors.
    } finally {
      setClearing(false)
    }
  }

  const loadOlder = useCallback(async () => {
    if (!hasOlderRef.current || loadingOlderRef.current) {
      return false
    }

    loadingOlderRef.current = true
    setLoadingOlder(true)
    try {
      const runID = logRunIdRef.current
      const data = await getGatewayLogs({
        log_before: logStartRef.current,
        log_run_id: runID,
      })
      if (data.log_run_id !== undefined && data.log_run_id !== runID) {
        logRunIdRef.current = data.log_run_id
        logStartRef.current = data.log_start ?? 0
        setLogStart(logStartRef.current)
        logOffsetRef.current = data.log_total ?? 0
        hasOlderRef.current = data.log_has_older ?? false
        setHasOlder(hasOlderRef.current)
        setLogs(data.logs ?? [])
        return false
      }

      const olderLogs = data.logs ?? []
      if (olderLogs.length === 0) {
        hasOlderRef.current = false
        setHasOlder(false)
        return false
      }

      setLogs((current) => [...olderLogs, ...current])
      logStartRef.current =
        data.log_start ?? Math.max(0, logStartRef.current - olderLogs.length)
      setLogStart(logStartRef.current)
      hasOlderRef.current = data.log_has_older ?? logStartRef.current > 0
      setHasOlder(hasOlderRef.current)
      return true
    } catch {
      return false
    } finally {
      loadingOlderRef.current = false
      setLoadingOlder(false)
    }
  }, [])

  useEffect(() => {
    let mounted = true
    let timeout: ReturnType<typeof setTimeout>

    const fetchLogs = async () => {
      if (
        !mounted ||
        !["running", "starting", "restarting", "stopping"].includes(
          gateway.status,
        )
      ) {
        if (mounted) {
          timeout = setTimeout(fetchLogs, 1000)
        }
        return
      }

      try {
        const requestToken = syncTokenRef.current
        const requestOffset = logOffsetRef.current
        const requestRunId = logRunIdRef.current
        const data = await getGatewayLogs({
          log_offset: requestOffset,
          log_run_id: requestRunId,
        })

        if (!mounted || requestToken !== syncTokenRef.current) {
          return
        }

        if (data.log_run_id !== undefined && data.log_run_id !== requestRunId) {
          logRunIdRef.current = data.log_run_id
          logStartRef.current = data.log_start ?? 0
          setLogStart(logStartRef.current)
          hasOlderRef.current = data.log_has_older ?? false
          setHasOlder(hasOlderRef.current)
          logOffsetRef.current = data.log_total ?? 0
          if (data.logs) {
            setLogs(data.logs)
          }
        } else if (data.logs && data.logs.length > 0) {
          const nextLogs = data.logs
          setLogs((prev) => [...prev, ...nextLogs])
          logOffsetRef.current =
            data.log_total ?? logOffsetRef.current + nextLogs.length
        }
        hasOlderRef.current = data.log_has_older ?? hasOlderRef.current
        setHasOlder(hasOlderRef.current)
      } catch {
        // Ignore simple fetch errors during polling.
      } finally {
        if (mounted) {
          timeout = setTimeout(fetchLogs, 1000)
        }
      }
    }

    fetchLogs()

    return () => {
      mounted = false
      clearTimeout(timeout)
    }
  }, [gateway.status])

  return {
    clearLogs,
    clearing,
    hasOlder,
    loadOlder,
    loadingOlder,
    logStart,
    logs,
  }
}
