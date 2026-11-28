import { IconScissors } from "@tabler/icons-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

type CompactionIndicatorPhase = "running" | "completed"

interface CompactionIndicatorProps {
  phase?: CompactionIndicatorPhase
}

// COMPACTION_INDICATOR_DELAY_MS keeps no-op or very short compactions from
// flashing a divider in the transcript. The backend emits a running/idle pair
// even when there is nothing to summarize, so a brief "Compacting..." frame
// would otherwise appear on every turn. The completed marker renders
// immediately because it is a deliberate, persisted transcript event.
const COMPACTION_INDICATOR_DELAY_MS = 150

export function CompactionIndicator({
  phase = "running",
}: CompactionIndicatorProps) {
  const { t } = useTranslation()
  const [visible, setVisible] = useState(phase === "completed")

  useEffect(() => {
    if (phase === "completed") {
      setVisible(true)
      return
    }

    setVisible(false)
    const timer = window.setTimeout(
      () => setVisible(true),
      COMPACTION_INDICATOR_DELAY_MS,
    )
    return () => window.clearTimeout(timer)
  }, [phase])

  if (!visible) {
    return null
  }

  const label =
    phase === "completed"
      ? t("chat.contextCompacted", { defaultValue: "Context compacted" })
      : t("chat.compacting", { defaultValue: "Compacting..." })

  return (
    <div
      role="status"
      aria-live="polite"
      className="text-muted-foreground flex w-full items-center gap-3 py-1 text-sm"
    >
      <span aria-hidden="true" className="bg-border h-px flex-1" />
      <span className="flex shrink-0 items-center gap-2.5">
        {phase === "completed" ? (
          <IconScissors
            aria-hidden="true"
            stroke={1.5}
            className="size-4 shrink-0"
          />
        ) : (
          <span
            aria-hidden="true"
            className="border-muted-foreground/30 border-t-muted-foreground size-4 shrink-0 animate-spin rounded-full border-2"
          />
        )}
        <span>{label}</span>
      </span>
      <span aria-hidden="true" className="bg-border h-px flex-1" />
    </div>
  )
}
