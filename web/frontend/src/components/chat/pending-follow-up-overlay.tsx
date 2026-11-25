import {
  IconArrowUp,
  IconClock,
  IconPaperclip,
  IconX,
} from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import type { PendingFollowUp } from "@/features/chat/follow-up"

interface PendingFollowUpOverlayProps {
  followUps: PendingFollowUp[]
  canSteer: boolean
  onSteer: (id: string) => void
  onRemove: (id: string) => void
}

function attachmentLabel(
  followUp: PendingFollowUp,
  index: number,
  t: (key: string) => string,
): string {
  const attachment = followUp.attachments[index]
  if (attachment.filename) {
    return attachment.filename
  }
  if (attachment.type === "image") {
    return t("chat.uploadedImage")
  }
  if (attachment.type === "audio") {
    return t("chat.uploadedAudio")
  }
  return t("chat.uploadedFile")
}

function followUpSummary(
  followUp: PendingFollowUp,
  t: (key: string) => string,
): string {
  const content = followUp.content.trim()
  if (content) {
    return content
  }
  return followUp.attachments
    .map((_, index) => attachmentLabel(followUp, index, t))
    .join(", ")
}

function attachmentTitle(
  followUp: PendingFollowUp,
  t: (key: string) => string,
): string {
  return followUp.attachments
    .map((_, index) => attachmentLabel(followUp, index, t))
    .join(", ")
}

export function PendingFollowUpOverlay({
  followUps,
  canSteer,
  onSteer,
  onRemove,
}: PendingFollowUpOverlayProps) {
  const { t } = useTranslation()

  if (followUps.length === 0) {
    return null
  }

  return (
    <div
      className="border-border/50 bg-background/40 divide-border/40 -mx-3 -mt-3 mb-2 flex flex-col divide-y overflow-hidden rounded-t-3xl border-b"
      aria-live="polite"
      title={t("chat.queuedFollowUpHint")}
    >
      {followUps.map((followUp) => {
        const summary = followUpSummary(followUp, t)
        return (
          <div
            key={followUp.id}
            className="flex min-w-0 items-center gap-1.5 px-3 py-1.5"
          >
            <IconClock
              className="text-muted-foreground size-3.5 shrink-0"
              aria-hidden="true"
            />
            <span
              className="text-foreground/90 min-w-0 flex-1 truncate text-xs"
              title={followUp.content || attachmentTitle(followUp, t)}
            >
              {summary}
            </span>
            {followUp.attachments.length > 0 && (
              <span
                className="text-muted-foreground inline-flex shrink-0 items-center gap-0.5 text-[10px] tabular-nums"
                title={attachmentTitle(followUp, t)}
              >
                <IconPaperclip className="size-3" aria-hidden="true" />
                {followUp.attachments.length}
              </span>
            )}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onSteer(followUp.id)}
              disabled={!canSteer}
              className="h-6 shrink-0 gap-1 rounded-full px-2 text-[11px] font-medium text-violet-600 hover:bg-violet-500/10 hover:text-violet-700 dark:text-violet-400 dark:hover:text-violet-300"
              aria-label={t("chat.steerNow")}
              title={t("chat.steerNow")}
            >
              <IconArrowUp className="size-3" />
              {t("chat.steerNow")}
            </Button>
            <button
              type="button"
              onClick={() => onRemove(followUp.id)}
              className="text-muted-foreground hover:text-foreground inline-flex size-6 shrink-0 items-center justify-center rounded-full"
              aria-label={t("chat.removeQueuedMessage")}
              title={t("chat.removeQueuedMessage")}
            >
              <IconX className="size-3.5" />
            </button>
          </div>
        )
      })}
    </div>
  )
}
