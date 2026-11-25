import {
  IconAlertTriangle,
  IconClock,
  IconGitBranch,
  IconRefresh,
  IconRobot,
  IconUsers,
} from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import type { SubagentActiveTask } from "@/api/subagents"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

interface SubagentActiveCardProps {
  tasks: SubagentActiveTask[]
  runtimeAvailable: boolean
  isLoading: boolean
  hasError: boolean
  onRefresh: () => void
}

export function SubagentActiveCard({
  tasks,
  runtimeAvailable,
  isLoading,
  hasError,
  onRefresh,
}: SubagentActiveCardProps) {
  const { t } = useTranslation()
  const now = useNow()

  return (
    <Card className="shadow-none">
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2">
            <IconUsers className="text-muted-foreground size-4" />
            {t("pages.agent.subagents.live.title", "Active subagents")}
          </CardTitle>
          <CardDescription>
            {t(
              "pages.agent.subagents.live.description",
              "Live sub-turn tasks spawned by the gateway.",
            )}
          </CardDescription>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Badge variant={tasks.length > 0 ? "default" : "outline"}>
            {t("pages.agent.subagents.live.count", {
              count: tasks.length,
              defaultValue: "{{count}} running",
            })}
          </Badge>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={t("pages.agent.subagents.live.refresh", "Refresh")}
            title={t("pages.agent.subagents.live.refresh", "Refresh")}
            onClick={onRefresh}
            disabled={isLoading}
          >
            <IconRefresh
              className={cn("size-4", isLoading && "animate-spin")}
            />
          </Button>
        </div>
      </CardHeader>

      <CardContent>
        {hasError ? (
          <StatusPanel
            icon={IconAlertTriangle}
            title={t(
              "pages.agent.subagents.live.load_error",
              "Failed to load live subagent status.",
            )}
            description={t(
              "pages.agent.subagents.live.load_error_hint",
              "The gateway may be restarting. Retrying automatically.",
            )}
            tone="error"
          />
        ) : isLoading ? (
          <div className="space-y-3">
            {[1, 2].map((item) => (
              <Skeleton key={item} className="h-24 rounded-xl" />
            ))}
          </div>
        ) : !runtimeAvailable ? (
          <StatusPanel
            icon={IconRobot}
            title={t(
              "pages.agent.subagents.live.runtime_unavailable",
              "Gateway runtime unavailable",
            )}
            description={t(
              "pages.agent.subagents.live.runtime_unavailable_hint",
              "Start the gateway to see live subagent tasks.",
            )}
          />
        ) : tasks.length === 0 ? (
          <StatusPanel
            icon={IconClock}
            title={t("pages.agent.subagents.live.empty", "No active subagents")}
            description={t(
              "pages.agent.subagents.live.empty_hint",
              "Spawned tasks appear here while they are running.",
            )}
          />
        ) : (
          <div className="space-y-3">
            {tasks.map((task) => (
              <ActiveTaskRow key={task.turn_id} task={task} now={now} />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

function ActiveTaskRow({
  task,
  now,
}: {
  task: SubagentActiveTask
  now: number
}) {
  const { t } = useTranslation()
  const elapsed = formatElapsed(task.started_at, now)

  return (
    <article className="border-border/50 bg-muted/20 rounded-xl border p-4">
      <div className="flex items-start gap-3">
        <div className="bg-background text-muted-foreground ring-foreground/10 flex size-9 shrink-0 items-center justify-center rounded-lg ring-1">
          <IconRobot className="size-4.5" />
        </div>
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-foreground/90 text-sm font-medium">
              {task.agent_name || task.agent_id}
            </span>
            <Badge variant="outline" className="font-mono text-[10px]">
              {task.agent_id}
            </Badge>
            <Badge variant="secondary" className="text-[10px] capitalize">
              {task.phase || "running"}
            </Badge>
            <Badge variant="outline" className="text-[10px]">
              {t("pages.agent.subagents.live.depth", {
                count: task.depth,
                defaultValue: "Depth {{count}}",
              })}
            </Badge>
          </div>

          <p className="text-foreground/80 line-clamp-2 text-[13px]">
            {task.task ||
              t(
                "pages.agent.subagents.live.task_missing",
                "Task details unavailable.",
              )}
          </p>

          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px]">
            <span className="inline-flex items-center gap-1">
              <IconGitBranch className="size-3.5" />
              {t("pages.agent.subagents.live.iteration", {
                count: task.iteration,
                defaultValue: "Iteration {{count}}",
              })}
            </span>
            {elapsed && (
              <span className="inline-flex items-center gap-1">
                <IconClock className="size-3.5" />
                {t("pages.agent.subagents.live.elapsed", {
                  duration: elapsed,
                  defaultValue: "{{duration}} elapsed",
                })}
              </span>
            )}
            {(task.channel || task.chat_id) && (
              <span className="inline-flex items-center gap-1">
                <IconUsers className="size-3.5" />
                {[task.channel, task.chat_id].filter(Boolean).join(" / ")}
              </span>
            )}
            {task.parent_turn_id && (
              <span className="font-mono">
                {t("pages.agent.subagents.live.parent", {
                  id: task.parent_turn_id,
                  defaultValue: "Parent {{id}}",
                })}
              </span>
            )}
          </div>
        </div>
      </div>
    </article>
  )
}

function StatusPanel({
  icon: Icon,
  title,
  description,
  tone = "muted",
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  description: string
  tone?: "muted" | "error"
}) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-xl border border-dashed px-4 py-5",
        tone === "error"
          ? "border-destructive/30 bg-destructive/5"
          : "border-border/60 bg-muted/20",
      )}
    >
      <div
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-lg",
          tone === "error"
            ? "bg-destructive/10 text-destructive"
            : "bg-muted text-muted-foreground",
        )}
      >
        <Icon className="size-4.5" />
      </div>
      <div className="space-y-1">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-muted-foreground text-[12px]">{description}</p>
      </div>
    </div>
  )
}

function useNow(): number {
  const [now, setNow] = React.useState(() => Date.now())
  React.useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  return now
}

function formatElapsed(startedAt: string | undefined, now: number): string {
  if (!startedAt) return ""
  const started = Date.parse(startedAt)
  if (Number.isNaN(started)) return ""
  const seconds = Math.max(0, Math.floor((now - started) / 1000))
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  if (minutes < 60) return `${minutes}m ${remainingSeconds}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${minutes % 60}m`
}
