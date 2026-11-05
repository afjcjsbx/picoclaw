import { IconActivity } from "@tabler/icons-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

interface TypingIndicatorProps {
  isTyping: boolean
  fallbackLabel?: string
}

function formatDuration(milliseconds: number): string {
  const seconds =
    milliseconds > 0 && milliseconds < 1000
      ? 1
      : Math.max(0, Math.round(milliseconds / 1000))
  if (seconds < 60) return `${seconds}s`

  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  return remainingSeconds ? `${minutes}m ${remainingSeconds}s` : `${minutes}m`
}

export function TypingIndicator({
  isTyping,
  fallbackLabel,
}: TypingIndicatorProps) {
  const { t } = useTranslation()
  const [timerActive, setTimerActive] = useState(false)
  const [startedAt, setStartedAt] = useState<number | null>(null)
  const [now, setNow] = useState<number | null>(null)
  const [finishedAt, setFinishedAt] = useState<number | null>(null)

  useEffect(() => {
    if (isTyping && !timerActive) {
      const start = Date.now()
      setStartedAt(start)
      setNow(start)
      setFinishedAt(null)
    } else if (!isTyping && timerActive) {
      setFinishedAt(Date.now())
    }
    setTimerActive(isTyping)
  }, [isTyping, timerActive])

  useEffect(() => {
    if (!isTyping) return

    const interval = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(interval)
  }, [isTyping])

  const starting = isTyping && !timerActive
  if (!isTyping && startedAt === null) {
    if (!fallbackLabel) return null
    return (
      <span className="text-muted-foreground/65 inline-flex min-w-0 flex-1 items-center gap-1.5 py-0.5 text-[12px] leading-4">
        <IconActivity
          aria-hidden="true"
          stroke={1.5}
          className="size-3.5 shrink-0"
        />
        <span className="min-w-0 truncate">{fallbackLabel}</span>
      </span>
    )
  }

  const end = isTyping ? now : (finishedAt ?? now)
  const durationMs =
    starting || startedAt === null || end === null
      ? 0
      : Math.max(0, end - startedAt)
  const duration = formatDuration(durationMs)
  const label = isTyping
    ? t("chat.activityWorkingFor", {
        duration,
        defaultValue: "Working for {{duration}}",
      })
    : durationMs <= 0
      ? t("chat.activityWorked", { defaultValue: "Worked" })
      : t("chat.activityWorkedFor", {
          duration,
          defaultValue: "Worked for {{duration}}",
        })

  return (
    <span
      role="status"
      aria-live={isTyping ? "polite" : undefined}
      className="text-muted-foreground/65 inline-flex min-w-0 flex-1 items-center gap-1.5 py-0.5 text-[12px] leading-4"
    >
      <IconActivity
        aria-hidden="true"
        stroke={1.5}
        className={`size-3.5 shrink-0 ${isTyping ? "animate-pulse text-violet-400" : "text-muted-foreground/65"}`}
      />
      <span className="min-w-0 truncate">{label}</span>
    </span>
  )
}
