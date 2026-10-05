import { type RefObject, useEffect, useLayoutEffect, useRef } from "react"
import { useTranslation } from "react-i18next"

import { AnsiLogLine } from "@/components/logs/ansi-log-line"
import { ScrollArea } from "@/components/ui/scroll-area"

const AUTO_SCROLL_THRESHOLD_PX = 24

function isNearBottom(viewport: HTMLDivElement) {
  const distanceToBottom =
    viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight

  return distanceToBottom <= AUTO_SCROLL_THRESHOLD_PX
}

type LogsPanelProps = {
  logs: string[]
  wrapColumns: number
  contentRef: RefObject<HTMLDivElement | null>
  measureRef: RefObject<HTMLSpanElement | null>
  hasOlder: boolean
  loadingOlder: boolean
  logStart: number
  onLoadOlder: () => Promise<boolean>
}

export function LogsPanel({
  logs,
  wrapColumns,
  contentRef,
  measureRef,
  hasOlder,
  loadingOlder,
  logStart,
  onLoadOlder,
}: LogsPanelProps) {
  const { t } = useTranslation()
  const scrollAreaRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const shouldStickToBottomRef = useRef(true)
  const pendingPrependRef = useRef<{ anchor: HTMLElement; top: number } | null>(
    null,
  )
  const requestingOlderRef = useRef(false)

  useEffect(() => {
    const scrollArea = scrollAreaRef.current
    const viewport = scrollArea?.querySelector<HTMLDivElement>(
      '[data-slot="scroll-area-viewport"]',
    )

    if (!viewport) {
      return
    }

    viewportRef.current = viewport

    const updateStickToBottom = () => {
      shouldStickToBottomRef.current = isNearBottom(viewport)
    }
    const loadOlderAtTop = () => {
      if (
        viewport.scrollTop > 32 ||
        !hasOlder ||
        loadingOlder ||
        requestingOlderRef.current
      ) {
        return
      }

      requestingOlderRef.current = true
      shouldStickToBottomRef.current = false
      const viewportTop = viewport.getBoundingClientRect().top
      const anchor = Array.from(
        contentRef.current?.querySelectorAll<HTMLElement>(
          "[data-log-offset]",
        ) ?? [],
      ).find((line) => line.getBoundingClientRect().bottom > viewportTop)
      pendingPrependRef.current = anchor
        ? { anchor, top: anchor.getBoundingClientRect().top }
        : null
      void onLoadOlder().then((loaded) => {
        requestingOlderRef.current = false
        if (!loaded) {
          pendingPrependRef.current = null
        }
      })
    }

    updateStickToBottom()
    viewport.addEventListener("scroll", updateStickToBottom)
    viewport.addEventListener("scroll", loadOlderAtTop)

    return () => {
      viewport.removeEventListener("scroll", updateStickToBottom)
      viewport.removeEventListener("scroll", loadOlderAtTop)
      if (viewportRef.current === viewport) {
        viewportRef.current = null
      }
    }
  }, [contentRef, hasOlder, loadingOlder, onLoadOlder])

  useLayoutEffect(() => {
    const viewport = viewportRef.current
    const pending = pendingPrependRef.current
    if (!viewport || !pending || loadingOlder) {
      return
    }

    if (pending.anchor.isConnected) {
      viewport.scrollTop +=
        pending.anchor.getBoundingClientRect().top - pending.top
    }
    pendingPrependRef.current = null
  }, [loadingOlder, logStart])

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport) {
      return
    }

    // Clearing logs or switching runs can replace the buffer with much shorter
    // content, so a previously stale "not sticky" state needs to be rechecked.
    if (!shouldStickToBottomRef.current) {
      shouldStickToBottomRef.current = isNearBottom(viewport)
    }

    if (shouldStickToBottomRef.current) {
      viewport.scrollTop = viewport.scrollHeight
    }
  }, [logs])

  return (
    <div className="relative flex-1 overflow-hidden rounded-lg border border-zinc-800 bg-zinc-950 text-zinc-100">
      <ScrollArea ref={scrollAreaRef} className="h-full">
        <div
          ref={contentRef}
          className="relative p-4 font-mono text-sm leading-relaxed"
        >
          <span
            ref={measureRef}
            aria-hidden
            className="pointer-events-none invisible absolute font-mono text-sm"
          >
            0
          </span>
          {logs.length === 0 ? (
            <div className="text-zinc-500 italic">{t("pages.logs.empty")}</div>
          ) : (
            logs.map((log, index) => (
              <AnsiLogLine
                key={logStart + index}
                line={log}
                logOffset={logStart + index}
                wrapColumns={wrapColumns}
              />
            ))
          )}
        </div>
      </ScrollArea>
    </div>
  )
}
