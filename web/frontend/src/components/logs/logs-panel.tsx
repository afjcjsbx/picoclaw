import { IconPlayerPlay } from "@tabler/icons-react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { type RefObject, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { AnsiLogLine } from "@/components/logs/ansi-log-line"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"

const AUTO_SCROLL_THRESHOLD_PX = 24
const ESTIMATED_LINE_HEIGHT_PX = 23
const OVERSCAN_ROWS = 12

function isNearBottom(viewport: HTMLDivElement) {
  const distanceToBottom =
    viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight

  return distanceToBottom <= AUTO_SCROLL_THRESHOLD_PX
}

type LogsPanelProps = {
  logs: string[]
  emptyMessage: string
  wrapColumns: number
  contentRef: RefObject<HTMLDivElement | null>
  measureRef: RefObject<HTMLSpanElement | null>
  paused: boolean
  newLines: number
  resumeToken: number
  onResume: () => void
}

export function LogsPanel({
  logs,
  emptyMessage,
  wrapColumns,
  contentRef,
  measureRef,
  paused,
  newLines,
  resumeToken,
  onResume,
}: LogsPanelProps) {
  const { t } = useTranslation()
  const scrollAreaRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<HTMLDivElement | null>(null)
  const shouldStickToBottomRef = useRef(true)
  const lastResumeTokenRef = useRef(resumeToken)
  const [, setViewportVersion] = useState(0)

  // TanStack Virtual owns its measurement/memoization; the compiler cannot model it.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: logs.length,
    getScrollElement: () => viewportRef.current,
    estimateSize: () => ESTIMATED_LINE_HEIGHT_PX,
    overscan: OVERSCAN_ROWS,
  })

  useEffect(() => {
    const scrollArea = scrollAreaRef.current
    const viewport = scrollArea?.querySelector<HTMLDivElement>(
      '[data-slot="scroll-area-viewport"]',
    )

    if (!viewport) {
      return
    }

    viewportRef.current = viewport
    // Re-render once so the virtualizer can attach to the scroll element.
    setViewportVersion((version) => version + 1)

    const updateStickToBottom = () => {
      shouldStickToBottomRef.current = isNearBottom(viewport)
    }

    updateStickToBottom()
    viewport.addEventListener("scroll", updateStickToBottom)

    return () => {
      viewport.removeEventListener("scroll", updateStickToBottom)
      if (viewportRef.current === viewport) {
        viewportRef.current = null
      }
    }
  }, [])

  useEffect(() => {
    virtualizer.measure()
  }, [wrapColumns, virtualizer])

  useEffect(() => {
    if (paused) {
      return
    }

    const frame = requestAnimationFrame(() => {
      const viewport = viewportRef.current
      if (!viewport) {
        return
      }

      // Clearing logs or switching runs can replace the buffer with much shorter
      // content, so a previously stale "not sticky" state needs to be rechecked.
      if (!shouldStickToBottomRef.current) {
        shouldStickToBottomRef.current = isNearBottom(viewport)
      }

      if (!shouldStickToBottomRef.current) {
        return
      }

      const lastIndex = logs.length - 1
      if (lastIndex >= 0) {
        virtualizer.scrollToIndex(lastIndex, { align: "end" })
      }
      viewport.scrollTop = viewport.scrollHeight
    })

    return () => cancelAnimationFrame(frame)
  }, [logs, paused, virtualizer, wrapColumns])

  useEffect(() => {
    if (resumeToken === lastResumeTokenRef.current) {
      return
    }

    lastResumeTokenRef.current = resumeToken
    shouldStickToBottomRef.current = true

    const frame = requestAnimationFrame(() => {
      const viewport = viewportRef.current
      if (!viewport) {
        return
      }

      const lastIndex = logs.length - 1
      if (lastIndex >= 0) {
        virtualizer.scrollToIndex(lastIndex, { align: "end" })
      }
      viewport.scrollTop = viewport.scrollHeight
    })

    return () => cancelAnimationFrame(frame)
  }, [logs.length, resumeToken, virtualizer])

  const virtualItems = virtualizer.getVirtualItems()

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
            <div className="text-zinc-500 italic">{emptyMessage}</div>
          ) : (
            <div
              className="relative w-full"
              style={{ height: `${virtualizer.getTotalSize()}px` }}
            >
              {virtualItems.map((virtualItem) => (
                <div
                  key={virtualItem.key}
                  data-index={virtualItem.index}
                  ref={virtualizer.measureElement}
                  className="absolute top-0 left-0 w-full"
                  style={{
                    transform: `translateY(${virtualItem.start}px)`,
                  }}
                >
                  <AnsiLogLine
                    line={logs[virtualItem.index]}
                    wrapColumns={wrapColumns}
                  />
                </div>
              ))}
            </div>
          )}
        </div>
      </ScrollArea>

      {paused && (
        <div className="pointer-events-none absolute inset-x-0 bottom-4 flex justify-center">
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="pointer-events-auto shadow-lg"
            onClick={onResume}
          >
            <IconPlayerPlay className="size-4" />
            {newLines > 0
              ? `${t("pages.logs.new_lines", { total: newLines })} · ${t(
                  "pages.logs.resume_tail",
                )}`
              : t("pages.logs.resume_tail")}
          </Button>
        </div>
      )}
    </div>
  )
}
