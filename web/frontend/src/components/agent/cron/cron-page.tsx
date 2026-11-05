import {
  IconBolt,
  IconClock,
  IconHistory,
  IconListDetails,
  IconLoader2,
  IconPlayerPause,
  IconPlayerPlay,
  IconRefresh,
  IconTrash,
} from "@tabler/icons-react"
import dayjs from "dayjs"
import { type ComponentType, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import type { CronJob, CronRunRecord } from "@/api/cron"
import { PageHeader } from "@/components/page-header"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
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

function statusVariant(
  status?: string,
): "secondary" | "destructive" | "outline" {
  switch (status) {
    case "error":
      return "destructive"
    case "ok":
      return "secondary"
    default:
      return "outline"
  }
}

function errorDescription(err: unknown): string | undefined {
  return err instanceof Error ? err.message : undefined
}

function StatCard({
  icon: Icon,
  label,
  value,
}: {
  icon: ComponentType<{ className?: string }>
  label: string
  value: string | number
}) {
  return (
    <Card className="shadow-none">
      <CardContent className="flex items-center gap-3 py-4">
        <div className="bg-muted/60 text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl">
          <Icon className="size-4.5" />
        </div>
        <div className="min-w-0">
          <p className="text-muted-foreground truncate text-[12px]">{label}</p>
          <p className="text-foreground/90 text-lg font-semibold tracking-tight">
            {value}
          </p>
        </div>
      </CardContent>
    </Card>
  )
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className="text-right break-words">{value}</span>
    </div>
  )
}

function RunRow({ run }: { run: CronRunRecord }) {
  const { t } = useTranslation()
  const labelKey = statusLabelKey(run.status)
  return (
    <div className="space-y-0.5">
      <div className="flex items-center gap-2 text-xs" title={run.error}>
        <span
          aria-hidden="true"
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            statusColor(run.status),
          )}
        />
        <span className="text-muted-foreground shrink-0">
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
      {run.error && (
        <p
          className="text-destructive ml-3.5 truncate text-[10px]"
          title={run.error}
        >
          {run.error}
        </p>
      )}
    </div>
  )
}

function CronJobCard({
  job,
  busy,
  onRun,
  onToggle,
  onDelete,
}: {
  job: CronJob
  busy: boolean
  onRun: () => void
  onToggle: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation()
  const schedule = describeCronSchedule(job.schedule, (ms) =>
    dayjs(ms).format("LLL"),
  )
  const scheduleText = t(schedule.key, schedule.values)
  const runs = recentRuns(job, 10)
  const lastStatusKey = job.state.lastStatus
    ? statusLabelKey(job.state.lastStatus)
    : null
  const nextRun = job.state.nextRunAtMs
    ? dayjs(job.state.nextRunAtMs).fromNow()
    : t("pages.agent.cron.not_scheduled")
  const lastRun = job.state.lastRunAtMs
    ? dayjs(job.state.lastRunAtMs).fromNow()
    : t("cron.neverRun")
  const notSet = t("pages.agent.cron.not_set")

  return (
    <Card className="shadow-none">
      <CardContent className="space-y-4 py-5">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
          <div className="flex min-w-0 items-start gap-3">
            <div className="bg-muted/60 text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-xl">
              <IconClock className="size-5" />
            </div>
            <div className="min-w-0 space-y-1">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="truncate text-base font-semibold tracking-tight">
                  {job.name}
                </h2>
                <Badge variant={job.enabled ? "secondary" : "outline"}>
                  {job.enabled
                    ? t("pages.agent.cron.active")
                    : t("cron.paused")}
                </Badge>
                {lastStatusKey && (
                  <Badge variant={statusVariant(job.state.lastStatus)}>
                    {t(lastStatusKey)}
                  </Badge>
                )}
              </div>
              <p className="text-muted-foreground text-[13px]">
                {scheduleText}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={onRun}
            >
              {busy ? (
                <IconLoader2 className="size-4 animate-spin" />
              ) : (
                <IconBolt className="size-4" />
              )}
              {t("cron.runNow")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={onToggle}
            >
              {job.enabled ? (
                <IconPlayerPause className="size-4" />
              ) : (
                <IconPlayerPlay className="size-4" />
              )}
              {job.enabled ? t("cron.pause") : t("cron.resume")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive"
              disabled={busy}
              onClick={onDelete}
            >
              <IconTrash className="size-4" />
              {t("cron.delete")}
            </Button>
          </div>
        </div>

        <div className="grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
          <div className="space-y-2 text-[13px]">
            <DetailRow label={t("pages.agent.cron.next_run")} value={nextRun} />
            <DetailRow label={t("pages.agent.cron.last_run")} value={lastRun} />
            <DetailRow
              label={t("pages.agent.cron.channel")}
              value={job.payload.channel || notSet}
            />
            {job.payload.to && (
              <DetailRow
                label={t("pages.agent.cron.target")}
                value={job.payload.to}
              />
            )}
            {job.payload.command ? (
              <div className="bg-muted/40 mt-3 rounded-lg p-2.5">
                <p className="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase">
                  {t("pages.agent.cron.command")}
                </p>
                <code className="block text-xs break-words whitespace-pre-wrap">
                  {job.payload.command}
                </code>
              </div>
            ) : job.payload.message ? (
              <div className="mt-3">
                <p className="text-muted-foreground mb-1 text-[11px] font-medium tracking-wide uppercase">
                  {t("pages.agent.cron.message")}
                </p>
                <p className="text-muted-foreground line-clamp-3 text-xs leading-relaxed">
                  {job.payload.message}
                </p>
              </div>
            ) : null}
          </div>

          <div className="border-border/60 rounded-xl border">
            <div className="border-border/50 flex items-center justify-between border-b px-3 py-2">
              <h3 className="flex items-center gap-1.5 text-sm font-medium">
                <IconHistory className="size-4" />
                {t("pages.agent.cron.history")}
              </h3>
              <span className="text-muted-foreground text-xs">
                {runs.length}
              </span>
            </div>
            <div className="space-y-1.5 px-3 py-2">
              {runs.length === 0 ? (
                <p className="text-muted-foreground text-xs">
                  {t("cron.noHistory")}
                </p>
              ) : (
                runs.map((run, index) => (
                  <RunRow key={`${run.runAtMs}-${index}`} run={run} />
                ))
              )}
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function CronLoadingState() {
  return (
    <div className="space-y-4">
      {Array.from({ length: 3 }).map((_, index) => (
        <Card key={index} className="shadow-none">
          <CardContent className="space-y-3 py-5">
            <Skeleton className="h-5 w-48" />
            <Skeleton className="h-4 w-72" />
            <Skeleton className="h-20 w-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

export function CronPage() {
  const { t } = useTranslation()
  const {
    jobs,
    isLoading,
    isFetching,
    loadError,
    busyIds,
    refresh,
    toggleJob,
    triggerJob,
    removeJob,
  } = useCronJobs()
  const [pendingDelete, setPendingDelete] = useState<CronJob | null>(null)
  const [deleting, setDeleting] = useState(false)

  const activeCount = jobs.filter((job) => job.enabled).length
  const pausedCount = jobs.length - activeCount
  const runCount = jobs.reduce(
    (total, job) =>
      total + (job.state.runHistory?.length ?? (job.state.lastRunAtMs ? 1 : 0)),
    0,
  )
  const loadErrorText = loadError?.includes("gateway is not running")
    ? t("cron.gatewayUnavailable")
    : loadError || t("pages.agent.cron.load_error")

  const handleRun = (job: CronJob) => {
    void triggerJob(job)
      .then(() => {
        toast.success(t("pages.agent.cron.run_success", { name: job.name }))
      })
      .catch((err) => {
        toast.error(t("pages.agent.cron.run_error"), {
          description: errorDescription(err),
        })
      })
  }

  const handleToggle = (job: CronJob) => {
    void toggleJob(job).catch((err) => {
      toast.error(t("pages.agent.cron.toggle_error"), {
        description: errorDescription(err),
      })
    })
  }

  const handleDeleteConfirm = async () => {
    if (!pendingDelete || deleting) return
    setDeleting(true)
    try {
      await removeJob(pendingDelete)
      toast.success(t("pages.agent.cron.delete_success"))
      setPendingDelete(null)
    } catch (err) {
      toast.error(t("pages.agent.cron.delete_error"), {
        description: errorDescription(err),
      })
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="bg-background flex h-full flex-col">
      <PageHeader title={t("navigation.cron", "Cron")}>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={isFetching}
          onClick={() => void refresh()}
        >
          <IconRefresh className={cn("size-4", isFetching && "animate-spin")} />
          {t("pages.agent.cron.refresh")}
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-auto px-6 py-6 pb-20">
        <div className="animate-in fade-in mx-auto w-full max-w-6xl space-y-8 pt-2 duration-300">
          <div className="max-w-2xl space-y-2">
            <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
              {t("pages.agent.cron.title")}
            </h1>
            <p className="text-muted-foreground/80 text-[14px] leading-relaxed">
              {t("pages.agent.cron.description")}
            </p>
          </div>

          {loadError && jobs.length === 0 ? (
            <Card className="shadow-none">
              <CardContent className="flex flex-col items-center gap-4 py-14 text-center">
                <p className="text-destructive font-medium">{loadErrorText}</p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void refresh()}
                >
                  {t("pages.agent.cron.retry")}
                </Button>
              </CardContent>
            </Card>
          ) : isLoading && jobs.length === 0 ? (
            <CronLoadingState />
          ) : (
            <>
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
                <StatCard
                  icon={IconListDetails}
                  label={t("pages.agent.cron.stats.total")}
                  value={jobs.length}
                />
                <StatCard
                  icon={IconPlayerPlay}
                  label={t("pages.agent.cron.stats.active")}
                  value={activeCount}
                />
                <StatCard
                  icon={IconPlayerPause}
                  label={t("pages.agent.cron.stats.paused")}
                  value={pausedCount}
                />
                <StatCard
                  icon={IconHistory}
                  label={t("pages.agent.cron.stats.runs")}
                  value={runCount}
                />
              </div>

              {jobs.length === 0 ? (
                <Card className="shadow-none">
                  <CardContent className="flex flex-col items-center gap-3 py-16 text-center">
                    <div className="bg-muted/60 text-muted-foreground flex size-12 items-center justify-center rounded-2xl">
                      <IconClock className="size-6" />
                    </div>
                    <p className="font-medium">{t("pages.agent.cron.empty")}</p>
                    <p className="text-muted-foreground max-w-md text-sm">
                      {t("pages.agent.cron.empty_hint")}
                    </p>
                  </CardContent>
                </Card>
              ) : (
                <div className="grid gap-5">
                  {jobs.map((job) => (
                    <CronJobCard
                      key={job.id}
                      job={job}
                      busy={busyIds.has(job.id)}
                      onRun={() => handleRun(job)}
                      onToggle={() => handleToggle(job)}
                      onDelete={() => setPendingDelete(job)}
                    />
                  ))}
                </div>
              )}

              {loadError && jobs.length > 0 && (
                <p className="text-destructive text-sm">{loadErrorText}</p>
              )}
            </>
          )}
        </div>
      </div>

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
    </div>
  )
}
