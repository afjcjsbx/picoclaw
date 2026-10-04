import { IconCircleFilled, IconLoader2 } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { MCPConnectionStatus } from "@/api/mcp"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

const statusClasses: Record<MCPConnectionStatus, string> = {
  connected:
    "border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  error: "border-destructive/20 bg-destructive/10 text-destructive",
  disconnected:
    "border-border bg-muted/60 text-muted-foreground dark:bg-muted/40",
  connecting:
    "border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  disabled: "border-border bg-muted/40 text-muted-foreground",
}

export function MCPStatusBadge({ status }: { status: MCPConnectionStatus }) {
  const { t } = useTranslation()
  return (
    <Badge
      variant="outline"
      className={cn("capitalize", statusClasses[status])}
    >
      {status === "connecting" ? (
        <IconLoader2 className="animate-spin" />
      ) : (
        <IconCircleFilled />
      )}
      {t(`pages.agent.mcp.status.${status}`, status)}
    </Badge>
  )
}
