import {
  IconBolt,
  IconChevronRight,
  IconLoader2,
  IconPlayerPause,
  IconPlayerPlay,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react"
import dayjs from "dayjs"
import * as React from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import type { CronJob, CronRunRecord } from "@/api/cron"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
} from "@/components/ui/sidebar"
import {
  describeCronSchedule,
  formatDuration,
  recentRuns,
} from "@/features/cron/schedule"
import { useCronJobs } from "@/hooks/use-cron-jobs"
import { cn } from "@/lib/utils"

function statusColor(status?: string) {
  switch (status) {
    case "ok":
      return "bg-emerald-500"
    case "error":
      return "bg-destructive"
    case "skipped":
      return "bg-amber-500"
    default:
      return "bg-muted-foreground/40"
  }
}

function statusLabelKey(status: string): string | null {
  switch (status) {
    case "ok":
      return "cron.statusOk"
    case "error":
      return "cron.statusError"
    case "skipped":
      return "cron.statusSkipped"
    default:
      return null
  }
}

function errorDescription(err: unknown): string | undefined {
  return err instanceof Error ? err.message : undefined
}

function JobActionButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string
  disabled?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
      className="text-muted-foreground hover:bg-sidebar-accent hover:text-foreground focus-visible:ring-sidebar-ring rounded p-1 transition-colors focus-visible:ring-2 focus-visible:outline-hidden disabled:pointer-events-none disabled:opacity-40"
    >
      {children}
    </button>
  )
}

function RunHistoryRow({ run }: { run: CronRunRecord }) {
  const { t } = useTranslation()
  const labelKey = statusLabelKey(run.status)
  return (
    <div
      className="flex items-center gap-1.5 text-[10px]"
      title={run.error || undefined}
    >
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          statusColor(run.status),
        )}
      />
      <span className="text-muted-foreground truncate">
        {dayjs(run.runAtMs).fromNow()}
      </span>
      <span className="text-muted-foreground/80 ml-auto shrink-0">
        {labelKey ? t(labelKey) : run.status}
      </span>
      {run.durationMs > 0 && (
        <span className="text-muted-foreground/60 shrink-0">
          {formatDuration(run.durationMs)}
        </span>
      )}
    </div>
  )
}

function CronJobRow({
  job,
  busy,
  expanded,
  onToggleExpand,
  onRun,
  onToggle,
  onDelete,
}: {
  job: CronJob
  busy: boolean
  expanded: boolean
  onToggleExpand: () => void
  onRun: () => void
  onToggle: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation()
  const schedule = describeCronSchedule(job.schedule, (ms) =>
    dayjs(ms).format("LLL"),
  )
  const scheduleText = t(schedule.key, schedule.values)
  const runs = recentRuns(job, 5)
  const lastStatusKey = job.state.lastStatus
    ? statusLabelKey(job.state.lastStatus)
    : null

  let timing: string
  if (!job.enabled) {
    timing = t("cron.paused")
  } else if (job.state.nextRunAtMs) {
    timing = t("cron.nextRun", {
      time: dayjs(job.state.nextRunAtMs).fromNow(),
    })
  } else if (job.state.lastRunAtMs) {
    timing = t("cron.lastRun", {
      time: dayjs(job.state.lastRunAtMs).fromNow(),
    })
  } else {
    timing = t("cron.neverRun")
  }

  return (
    <div className="border-sidebar-border/60 bg-background/60 rounded-lg border p-2">
      <div className="flex items-start gap-1.5">
        {busy ? (
          <IconLoader2 className="text-muted-foreground mt-0.5 size-3 shrink-0 animate-spin" />
        ) : (
          <span
            aria-hidden="true"
            title={lastStatusKey ? t(lastStatusKey) : undefined}
            className={cn(
              "mt-1 size-1.5 shrink-0 rounded-full",
              statusColor(job.enabled ? job.state.lastStatus : undefined),
            )}
          />
        )}
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium" title={job.name}>
            {job.name}
          </div>
          <div
            className="text-muted-foreground truncate text-[10px]"
            title={scheduleText}
          >
            {scheduleText}
          </div>
        </div>
      </div>
      <div className="mt-1 flex items-center justify-between gap-1">
        <button
          type="button"
          aria-expanded={expanded}
          aria-label={t("cron.history")}
          title={t("cron.history")}
          onClick={onToggleExpand}
          className="text-muted-foreground hover:text-foreground focus-visible:ring-sidebar-ring flex min-w-0 items-center gap-1 rounded text-[10px] outline-none focus-visible:ring-2"
        >
          <IconChevronRight
            className={cn(
              "size-3 shrink-0 transition-transform",
              expanded && "rotate-90",
            )}
          />
          <span className="truncate">{timing}</span>
        </button>
        <div className="flex shrink-0 items-center">
          <JobActionButton
            label={t("cron.runNow")}
            disabled={busy}
            onClick={onRun}
          >
            <IconBolt className="size-3.5" />
          </JobActionButton>
          <JobActionButton
            label={job.enabled ? t("cron.pause") : t("cron.resume")}
            disabled={busy}
            onClick={onToggle}
          >
            {job.enabled ? (
              <IconPlayerPause className="size-3.5" />
            ) : (
              <IconPlayerPlay className="size-3.5" />
            )}
          </JobActionButton>
          <JobActionButton
            label={t("cron.delete")}
            disabled={busy}
            onClick={onDelete}
          >
            <IconTrash className="size-3.5" />
          </JobActionButton>
        </div>
      </div>
      {expanded && (
        <div className="border-sidebar-border/60 mt-1.5 space-y-1 border-t pt-1.5">
          {runs.length === 0 ? (
            <div className="text-muted-foreground text-[10px]">
              {t("cron.noHistory")}
            </div>
          ) : (
            runs.map((run, index) => (
              <RunHistoryRow key={`${run.runAtMs}-${index}`} run={run} />
            ))
          )}
        </div>
      )}
    </div>
  )
}

export function SidebarCronCard() {
  const { t } = useTranslation()
  const {
    jobs,
    isLoading,
    loadError,
    busyIds,
    refresh,
    toggleJob,
    triggerJob,
    removeJob,
  } = useCronJobs({ enabled: true })
  const [expandedIds, setExpandedIds] = React.useState<Set<string>>(
    () => new Set(),
  )
  const [pendingDelete, setPendingDelete] = React.useState<CronJob | null>(null)
  const [deleting, setDeleting] = React.useState(false)
  const loadErrorText = loadError?.includes("gateway is not running")
    ? t("cron.gatewayUnavailable")
    : t("cron.loadFailed")

  const handleToggleExpand = (id: string) => {
    setExpandedIds((current) => {
      const next = new Set(current)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  const handleRun = (job: CronJob) => {
    void triggerJob(job).catch((err) => {
      toast.error(t("cron.runFailed"), {
        description: errorDescription(err),
      })
    })
  }

  const handleToggle = (job: CronJob) => {
    void toggleJob(job).catch((err) => {
      toast.error(t("cron.toggleFailed"), {
        description: errorDescription(err),
      })
    })
  }

  const handleDeleteConfirm = async () => {
    if (!pendingDelete || deleting) return
    setDeleting(true)
    try {
      await removeJob(pendingDelete)
      setPendingDelete(null)
    } catch (err) {
      toast.error(t("cron.deleteFailed"), {
        description: errorDescription(err),
      })
    } finally {
      setDeleting(false)
    }
  }

  return (
    <>
      <SidebarGroup className="px-2 py-0">
        <div className="flex items-center justify-between px-2 pb-1">
          <SidebarGroupLabel className="px-0">
            {t("cron.title")}
          </SidebarGroupLabel>
          <button
            type="button"
            aria-label={t("cron.refresh")}
            title={t("cron.refresh")}
            onClick={() => void refresh()}
            className="text-muted-foreground hover:text-foreground focus-visible:ring-sidebar-ring rounded p-0.5 focus-visible:ring-2 focus-visible:outline-hidden"
          >
            <IconRefresh
              className={cn("size-3.5", isLoading && "animate-spin")}
            />
          </button>
        </div>
        <SidebarGroupContent>
          <div className="border-sidebar-border/70 bg-sidebar-accent/30 rounded-xl border p-1.5">
            {isLoading && jobs.length === 0 ? (
              <div className="text-muted-foreground flex items-center gap-1.5 px-1 py-1.5 text-[10px]">
                <IconLoader2 className="size-3 animate-spin" />
                {t("cron.loading")}
              </div>
            ) : loadError && jobs.length === 0 ? (
              <div className="text-muted-foreground px-1 py-1.5 text-[10px]">
                {loadErrorText}
              </div>
            ) : jobs.length === 0 ? (
              <div className="text-muted-foreground px-1 py-1.5 text-[10px]">
                {t("cron.empty")}
              </div>
            ) : (
              <div className="max-h-[min(30vh,18rem)] space-y-1.5 overflow-y-auto pr-0.5">
                {jobs.map((job) => (
                  <CronJobRow
                    key={job.id}
                    job={job}
                    busy={busyIds.has(job.id)}
                    expanded={expandedIds.has(job.id)}
                    onToggleExpand={() => handleToggleExpand(job.id)}
                    onRun={() => handleRun(job)}
                    onToggle={() => handleToggle(job)}
                    onDelete={() => setPendingDelete(job)}
                  />
                ))}
              </div>
            )}
            {loadError && jobs.length > 0 && (
              <div className="text-destructive px-1 pt-1 text-[10px]">
                {loadErrorText}
              </div>
            )}
          </div>
        </SidebarGroupContent>
      </SidebarGroup>
      <AlertDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open && !deleting) setPendingDelete(null)
        }}
      >
        <AlertDialogContent size="sm">
          <AlertDialogHeader>
            <AlertDialogTitle>{t("cron.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("cron.deleteConfirm", { name: pendingDelete?.name ?? "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={deleting}
              onClick={(event) => {
                event.preventDefault()
                void handleDeleteConfirm()
              }}
            >
              {deleting && <IconLoader2 className="size-4 animate-spin" />}
              {t("cron.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
