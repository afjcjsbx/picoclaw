import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { type SessionSummary, deleteSession, getSessions } from "@/api/sessions"

const LIMIT = 20

interface UseSessionHistoryOptions {
  activeSessionId: string
  onDeletedActiveSession: () => void
}

export function useSessionHistory({
  activeSessionId,
  onDeletedActiveSession,
}: UseSessionHistoryOptions) {
  const { t } = useTranslation()
  const observerRef = useRef<HTMLDivElement>(null)
  const offsetRef = useRef(0)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [hasMore, setHasMore] = useState(true)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [loadError, setLoadError] = useState(false)

  const loadSessions = useCallback(async (reset = true) => {
    try {
      const currentOffset = reset ? 0 : offsetRef.current
      if (reset) {
        setLoadError(false)
        setHasMore(true)
        offsetRef.current = 0
      }
      setIsLoadingMore(true)

      const data = await getSessions(currentOffset, LIMIT)
      setLoadError(false)

      if (data.length < LIMIT) {
        setHasMore(false)
      }

      if (reset) {
        setSessions(data)
      } else {
        setSessions((prev) => {
          const existingIds = new Set(prev.map((s) => s.id))
          const newItems = data.filter((s) => !existingIds.has(s.id))
          return [...prev, ...newItems]
        })
      }

      offsetRef.current = currentOffset + data.length
    } catch (err) {
      console.error("Failed to fetch session history:", err)
      setLoadError(true)
      if (!reset) {
        setHasMore(false)
      }
    } finally {
      setIsLoadingMore(false)
    }
  }, [])

  useEffect(() => {
    if (!observerRef.current || !hasMore || isLoadingMore || loadError) return

    const observer = new IntersectionObserver(
      (entries) => {
        if (
          entries[0].isIntersecting &&
          hasMore &&
          !isLoadingMore &&
          !loadError
        ) {
          setIsLoadingMore(true)
          void loadSessions(false)
        }
      },
      { threshold: 0.1 },
    )

    observer.observe(observerRef.current)
    return () => observer.disconnect()
  }, [hasMore, isLoadingMore, loadError, loadSessions])

  const handleDeleteSession = useCallback(
    async (id: string): Promise<boolean> => {
      try {
        const deletedLoadedSession = sessions.some(
          (session) => session.id === id,
        )
        await deleteSession(id)
        setSessions((prev) => prev.filter((s) => s.id !== id))
        if (deletedLoadedSession) {
          offsetRef.current = Math.max(offsetRef.current - 1, 0)
        }
        if (id === activeSessionId) {
          onDeletedActiveSession()
        }
        return true
      } catch (err) {
        console.error("Failed to delete session:", err)
        return false
      }
    },
    [activeSessionId, onDeletedActiveSession, sessions],
  )

  return {
    sessions,
    hasMore,
    isLoadingMore,
    loadError,
    loadErrorMessage: t("chat.historyLoadFailed"),
    observerRef,
    loadSessions,
    handleDeleteSession,
  }
}
