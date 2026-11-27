import {
  type MouseEvent,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react"

import { cn } from "@/lib/utils"

type MessageRole = "user" | "assistant"

interface MessageMarker {
  key: string
  index: number
  role: MessageRole
  preview: string
  top: number
  height: number
}

export interface ConversationNavigatorProps {
  scrollRef: RefObject<HTMLDivElement | null>
  label: string
  messageLabel?: (role: MessageRole, index: number, preview: string) => string
}

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(maximum, Math.max(minimum, value))
}

function messagePreview(element: HTMLElement) {
  const text = (
    element.dataset.chatPreview ||
    element.innerText ||
    element.textContent ||
    ""
  )
    .replace(/\s+/g, " ")
    .trim()
  return text.slice(0, 80)
}

function markersEqual(left: MessageMarker[], right: MessageMarker[]) {
  return (
    left.length === right.length &&
    left.every((marker, index) => {
      const next = right[index]
      return (
        marker.key === next.key &&
        marker.index === next.index &&
        marker.role === next.role &&
        marker.preview === next.preview &&
        marker.top === next.top &&
        marker.height === next.height
      )
    })
  )
}

export function ConversationNavigator({
  scrollRef,
  label,
  messageLabel,
}: ConversationNavigatorProps) {
  const [markers, setMarkers] = useState<MessageMarker[]>([])
  const [activeKey, setActiveKey] = useState<string | null>(null)
  const [visible, setVisible] = useState(false)
  const markersRef = useRef<MessageMarker[]>([])
  const selectedKeyRef = useRef<string | null>(null)

  const updateActiveMarker = useCallback(() => {
    const selectedKey = selectedKeyRef.current
    if (selectedKey) {
      setActiveKey((current) =>
        current === selectedKey ? current : selectedKey,
      )
      return
    }

    const scroller = scrollRef.current
    if (!scroller || markersRef.current.length === 0) {
      setActiveKey(null)
      return
    }

    const viewportCenter = scroller.scrollTop + scroller.clientHeight / 2
    const closest = markersRef.current.reduce((current, marker) =>
      Math.abs(marker.top - viewportCenter) <
      Math.abs(current.top - viewportCenter)
        ? marker
        : current,
    )
    setActiveKey((current) => (current === closest.key ? current : closest.key))
  }, [scrollRef])

  const clearMessageSelection = useCallback(() => {
    selectedKeyRef.current = null
    updateActiveMarker()
  }, [updateActiveMarker])

  const measureMarkers = useCallback(() => {
    const scroller = scrollRef.current
    if (!scroller) return

    const scrollerRect = scroller.getBoundingClientRect()
    const nextMarkers = Array.from(
      scroller.querySelectorAll<HTMLElement>("[data-chat-index]"),
    )
      .flatMap<MessageMarker>((element) => {
        const role = element.dataset.chatRole
        const kind = element.dataset.chatKind
        const index = Number(element.dataset.chatIndex)
        if (
          !Number.isInteger(index) ||
          (role !== "user" && role !== "assistant") ||
          (role === "assistant" && kind !== "normal")
        ) {
          return []
        }

        const rect = element.getBoundingClientRect()
        const top = rect.top - scrollerRect.top + scroller.scrollTop
        return [
          {
            key: `${role}-${index}`,
            index,
            role,
            preview: messagePreview(element),
            top,
            height: rect.height,
          },
        ]
      })
      .sort((left, right) => left.top - right.top)

    if (
      selectedKeyRef.current &&
      !nextMarkers.some((marker) => marker.key === selectedKeyRef.current)
    ) {
      selectedKeyRef.current = null
    }
    markersRef.current = nextMarkers
    setMarkers((current) =>
      markersEqual(current, nextMarkers) ? current : nextMarkers,
    )
    setVisible(
      nextMarkers.length > 0 && scroller.scrollHeight > scroller.clientHeight,
    )
    updateActiveMarker()
  }, [scrollRef, updateActiveMarker])

  useLayoutEffect(() => {
    measureMarkers()
  })

  useEffect(() => {
    const scroller = scrollRef.current
    if (!scroller) return

    const observer = new ResizeObserver(measureMarkers)
    observer.observe(scroller)
    if (scroller.firstElementChild) {
      observer.observe(scroller.firstElementChild)
    }
    scroller.addEventListener("scroll", updateActiveMarker, { passive: true })
    scroller.addEventListener("wheel", clearMessageSelection, { passive: true })
    scroller.addEventListener("touchmove", clearMessageSelection, {
      passive: true,
    })
    scroller.addEventListener("pointerdown", clearMessageSelection)
    scroller.addEventListener("keydown", clearMessageSelection)

    return () => {
      observer.disconnect()
      scroller.removeEventListener("scroll", updateActiveMarker)
      scroller.removeEventListener("wheel", clearMessageSelection)
      scroller.removeEventListener("touchmove", clearMessageSelection)
      scroller.removeEventListener("pointerdown", clearMessageSelection)
      scroller.removeEventListener("keydown", clearMessageSelection)
    }
  }, [clearMessageSelection, measureMarkers, scrollRef, updateActiveMarker])

  const jumpToMarker = (marker: MessageMarker) => {
    const scroller = scrollRef.current
    if (!scroller) return
    selectedKeyRef.current = marker.key
    setActiveKey(marker.key)
    const maxScroll = Math.max(0, scroller.scrollHeight - scroller.clientHeight)
    scroller.scrollTop = clamp(
      marker.top - scroller.clientHeight / 2 + marker.height / 2,
      0,
      maxScroll,
    )
  }

  const handleTrackClick = (event: MouseEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest("[data-message-marker]")) return
    if (markersRef.current.length === 0) return

    const buttons = Array.from(
      event.currentTarget.querySelectorAll<HTMLButtonElement>(
        "[data-message-marker]",
      ),
    )
    const closestIndex = buttons.reduce((closest, button, index) => {
      const rect = button.getBoundingClientRect()
      const distance = Math.abs(event.clientY - (rect.top + rect.height / 2))
      const closestRect = buttons[closest].getBoundingClientRect()
      const closestDistance = Math.abs(
        event.clientY - (closestRect.top + closestRect.height / 2),
      )
      return distance < closestDistance ? index : closest
    }, 0)
    jumpToMarker(markersRef.current[closestIndex])
  }

  return (
    <div
      aria-hidden={!visible}
      className={cn(
        "pointer-events-none absolute inset-y-0 left-0 z-20 flex w-5 justify-center transition-opacity",
        visible ? "opacity-100" : "invisible opacity-0",
      )}
    >
      <nav
        aria-label={label}
        aria-hidden={!visible}
        className="pointer-events-none flex justify-center self-stretch"
      >
        <div
          data-conversation-track
          onClick={handleTrackClick}
          className="pointer-events-auto relative my-3 flex w-4 shrink-0 flex-col items-center justify-center gap-0 self-stretch"
        >
          {markers.map((marker, index) => {
            const markerLabel =
              messageLabel?.(marker.role, marker.index, marker.preview) ??
              `${label} ${marker.index + 1}: ${marker.preview}`
            const isActive = marker.key === activeKey

            return (
              <button
                key={marker.key}
                type="button"
                data-message-marker
                data-marker-index={index}
                data-marker-count={markers.length}
                aria-label={markerLabel}
                aria-current={isActive ? "true" : undefined}
                title={markerLabel}
                tabIndex={visible ? 0 : -1}
                onClick={() => jumpToMarker(marker)}
                className={cn(
                  "group/marker focus-visible:ring-ring relative flex h-2 min-h-0 w-5 items-center justify-center rounded-sm outline-none focus-visible:ring-2",
                  isActive ? "z-10" : "z-0",
                )}
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    "h-0.5 rounded-none transition-all",
                    isActive
                      ? "bg-foreground w-5"
                      : "bg-muted-foreground/55 w-3.5",
                  )}
                />
              </button>
            )
          })}
        </div>
      </nav>
    </div>
  )
}
