import {
  IconCheck,
  IconClockHour4,
  IconTerminal2,
  IconX,
} from "@tabler/icons-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import type { ChatApproval } from "@/store/chat"

interface ApprovalMessageProps {
  approval: ChatApproval
  onApprove: () => boolean
  onDeny: () => boolean
}

function formatRemaining(ms: number): string {
  const totalSeconds = Math.max(0, Math.ceil(ms / 1000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${String(seconds).padStart(2, "0")}`
}

export function ApprovalMessage({
  approval,
  onApprove,
  onDeny,
}: ApprovalMessageProps) {
  const { t } = useTranslation()
  const [decision, setDecision] = useState<"approve" | "deny" | null>(null)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (approval.expiresAt === undefined) return
    const tick = () => setNow(Date.now())
    tick()
    const timer = window.setInterval(tick, 1000)
    return () => window.clearInterval(timer)
  }, [approval.expiresAt])

  const expired = approval.expiresAt !== undefined && now >= approval.expiresAt
  const disabled = decision !== null || expired

  const respond = (action: "approve" | "deny") => {
    if (disabled) return
    const sent = action === "approve" ? onApprove() : onDeny()
    if (!sent) {
      toast.error(t("chat.approval.sendFailed"))
      return
    }
    setDecision(action)
  }

  const status = (() => {
    if (decision === "approve") return t("chat.approval.approved")
    if (decision === "deny") return t("chat.approval.denied")
    if (expired) return t("chat.approval.expired")
    if (approval.expiresAt !== undefined) {
      return t("chat.approval.expiresIn", {
        time: formatRemaining(approval.expiresAt - now),
      })
    }
    return ""
  })()

  return (
    <div className="rounded-2xl border border-amber-500/30 bg-amber-500/5 p-4 shadow-sm">
      <div className="flex items-start gap-3">
        <span className="mt-0.5 inline-flex size-8 shrink-0 items-center justify-center rounded-full bg-amber-500/10 text-amber-600 dark:text-amber-400">
          <IconTerminal2 className="size-4" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h3 className="text-sm font-semibold">
                {t("chat.approval.title")}
              </h3>
              <p className="text-muted-foreground mt-0.5 text-xs">
                {t("chat.approval.description")}
              </p>
            </div>
            {status ? (
              <span className="text-muted-foreground inline-flex shrink-0 items-center gap-1 text-xs">
                <IconClockHour4 className="size-3.5" aria-hidden="true" />
                {status}
              </span>
            ) : null}
          </div>

          {approval.reason ? (
            <p className="text-muted-foreground mt-3 text-xs">
              <span className="text-foreground/80 font-medium">
                {t("chat.approval.reason")}:
              </span>{" "}
              {approval.reason}
            </p>
          ) : null}

          {approval.command ? (
            <div className="mt-3">
              <p className="text-muted-foreground mb-1 text-xs font-medium">
                {t("chat.approval.command")}
              </p>
              <pre className="border-border/60 bg-background/70 max-h-48 overflow-auto rounded-lg border px-3 py-2 text-xs break-all whitespace-pre-wrap">
                <code>{approval.command}</code>
              </pre>
            </div>
          ) : null}

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button
              type="button"
              size="sm"
              onClick={() => respond("approve")}
              disabled={disabled}
              className="gap-1.5 bg-violet-500 text-white hover:bg-violet-600"
            >
              <IconCheck className="size-3.5" aria-hidden="true" />
              {t("chat.approval.approve")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => respond("deny")}
              disabled={disabled}
              className="gap-1.5"
            >
              <IconX className="size-3.5" aria-hidden="true" />
              {t("chat.approval.deny")}
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}
