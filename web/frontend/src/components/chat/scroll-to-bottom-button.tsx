import { IconChevronDown } from "@tabler/icons-react"
import {
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useState,
} from "react"

import { cn } from "@/lib/utils"

const BOTTOM_THRESHOLD_PX = 80

export interface ScrollToBottomButtonProps {
  scrollRef: RefObject<HTMLDivElement | null>
  label: string
  className?: string
}

export function ScrollToBottomButton({
  scrollRef,
  label,
  className,
}: ScrollToBottomButtonProps) {
  const [visible, setVisible] = useState(false)

  const updateVisibility = useCallback(() => {
    const scroller = scrollRef.current
    if (!scroller) {
      setVisible(false)
      return
    }

    const distanceFromBottom =
      scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight
    setVisible(distanceFromBottom > BOTTOM_THRESHOLD_PX)
  }, [scrollRef])

  useLayoutEffect(() => {
    updateVisibility()
  })

  useEffect(() => {
    const scroller = scrollRef.current
    if (!scroller) return

    scroller.addEventListener("scroll", updateVisibility, { passive: true })
    const observer = new ResizeObserver(updateVisibility)
    observer.observe(scroller)
    if (scroller.firstElementChild) {
      observer.observe(scroller.firstElementChild)
    }

    return () => {
      observer.disconnect()
      scroller.removeEventListener("scroll", updateVisibility)
    }
  }, [scrollRef, updateVisibility])

  const scrollToBottom = () => {
    const scroller = scrollRef.current
    if (!scroller) return

    const reduceMotion =
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches

    if (!reduceMotion && typeof scroller.scrollTo === "function") {
      scroller.scrollTo({ top: scroller.scrollHeight, behavior: "smooth" })
      return
    }

    scroller.scrollTop = scroller.scrollHeight
    updateVisibility()
  }

  if (!visible) return null

  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={scrollToBottom}
      className={cn(
        "bg-background/90 text-muted-foreground hover:text-foreground border-border focus-visible:ring-ring absolute bottom-4 left-1/2 z-30 flex size-9 -translate-x-1/2 items-center justify-center rounded-full border shadow-lg backdrop-blur transition-colors outline-none focus-visible:ring-2",
        className,
      )}
    >
      <IconChevronDown className="size-5" />
    </button>
  )
}
