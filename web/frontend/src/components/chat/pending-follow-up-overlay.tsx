import { IconArrowUp, IconClock, IconX } from "@tabler/icons-react"
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
    <div className="mb-2 flex w-full flex-col gap-1.5" aria-live="polite">
      {followUps.map((followUp) => (
        <div
          key={followUp.id}
          className="bg-background/95 border-border/60 flex items-start gap-2 rounded-2xl border px-3 py-2 shadow-sm backdrop-blur"
        >
          <IconClock
            className="text-muted-foreground mt-0.5 size-3.5 shrink-0"
            aria-hidden="true"
          />
          <div className="min-w-0 flex-1">
            <div className="text-muted-foreground text-[11px] font-medium">
              {t("chat.queuedFollowUp")}
            </div>
            {followUp.content && (
              <div className="text-foreground mt-0.5 max-h-20 overflow-y-auto text-sm break-words whitespace-pre-wrap">
                {followUp.content}
              </div>
            )}
            {followUp.attachments.length > 0 && (
              <div className="mt-1 flex flex-wrap gap-1">
                {followUp.attachments.map((attachment, index) => (
                  <span
                    key={`${attachment.url}-${index}`}
                    className="bg-muted text-muted-foreground max-w-52 truncate rounded px-1.5 py-0.5 text-[11px]"
                  >
                    {attachmentLabel(followUp, index, t)}
                  </span>
                ))}
              </div>
            )}
            <div className="text-muted-foreground/80 mt-0.5 text-[10px]">
              {t("chat.queuedFollowUpHint")}
            </div>
          </div>
          <Button
            type="button"
            size="sm"
            onClick={() => onSteer(followUp.id)}
            disabled={!canSteer}
            className="h-7 shrink-0 gap-1 rounded-full bg-violet-500 px-2.5 text-xs text-white hover:bg-violet-600"
            aria-label={t("chat.steerNow")}
            title={t("chat.steerNow")}
          >
            <IconArrowUp className="size-3.5" />
            {t("chat.steerNow")}
          </Button>
          <button
            type="button"
            onClick={() => onRemove(followUp.id)}
            className="text-muted-foreground hover:text-foreground inline-flex size-7 shrink-0 items-center justify-center rounded-full"
            aria-label={t("chat.removeQueuedMessage")}
            title={t("chat.removeQueuedMessage")}
          >
            <IconX className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  )
}
