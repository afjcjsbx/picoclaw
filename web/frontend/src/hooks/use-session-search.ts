import { useEffect, useRef, useState } from "react"

import { type SessionSearchResult, searchSessions } from "@/api/sessions"

const SEARCH_DEBOUNCE_MS = 250
const SEARCH_MIN_LENGTH = 2
const SEARCH_LIMIT = 20

/**
 * Debounced full-text conversation search. Aborts stale requests so typing
 * quickly never stacks work on the backend, and keeps the query server-side so
 * long histories are never loaded into the browser just to be filtered.
 */
export function useSessionSearch() {
  const [query, setQuery] = useState("")
  const [results, setResults] = useState<SessionSearchResult[]>([])
  const [isSearching, setIsSearching] = useState(false)
  const [error, setError] = useState(false)
  const [truncated, setTruncated] = useState(false)
  const [hasMore, setHasMore] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  const trimmedQuery = query.trim()
  const isActive = trimmedQuery.length >= SEARCH_MIN_LENGTH

  useEffect(() => {
    abortRef.current?.abort()
    abortRef.current = null

    if (!isActive) {
      setResults([])
      setIsSearching(false)
      setError(false)
      setTruncated(false)
      setHasMore(false)
      return
    }

    setIsSearching(true)
    const handle = window.setTimeout(() => {
      const controller = new AbortController()
      abortRef.current = controller

      searchSessions(trimmedQuery, SEARCH_LIMIT, 0, controller.signal)
        .then((response) => {
          if (controller.signal.aborted) return
          setResults(response.results)
          setHasMore(response.has_more)
          setTruncated(response.truncated)
          setError(false)
        })
        .catch((searchError: unknown) => {
          if (
            controller.signal.aborted ||
            (searchError instanceof DOMException &&
              searchError.name === "AbortError")
          ) {
            return
          }
          setResults([])
          setError(true)
        })
        .finally(() => {
          if (!controller.signal.aborted) {
            setIsSearching(false)
          }
        })
    }, SEARCH_DEBOUNCE_MS)

    return () => {
      window.clearTimeout(handle)
    }
  }, [isActive, trimmedQuery])

  useEffect(
    () => () => {
      abortRef.current?.abort()
    },
    [],
  )

  return {
    query,
    setQuery,
    results,
    isSearching,
    error,
    truncated,
    hasMore,
    isActive,
    clear: () => setQuery(""),
  }
}
