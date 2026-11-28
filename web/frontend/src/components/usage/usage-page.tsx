import { IconChartBar, IconDatabase, IconRefresh } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { type ComponentType, useState } from "react"
import { useTranslation } from "react-i18next"

import type { DailyRow, ModelTotals, SessionRow, Totals } from "@/api/usage"
import { getUsage } from "@/api/usage"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  compactTokens,
  fillDays,
  fmtInt,
  formatCost,
  sharePercent,
  tokenCategories,
} from "@/features/usage/usage"
import { cn } from "@/lib/utils"

// ─── category color palette (uses existing chart CSS vars) ───────────────────
const CAT_COLORS = {
  freshInput: "var(--chart-3)",
  cacheRead: "var(--chart-2)",
  cacheWrite: "var(--chart-4)",
  answer: "var(--chart-1)",
  reasoning: "var(--chart-5)",
} as const

const CHART_H = 128

// ─── small helpers ────────────────────────────────────────────────────────────

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
          <p className="text-foreground/90 font-tabular-nums text-lg font-semibold tracking-tight tabular-nums">
            {value}
          </p>
        </div>
      </CardContent>
    </Card>
  )
}

function RangeSelector({
  value,
  onChange,
}: {
  value: number
  onChange: (days: number) => void
}) {
  const { t } = useTranslation()
  const options = [
    { days: 7, label: t("pages.agent.usage.range_7") },
    { days: 30, label: t("pages.agent.usage.range_30") },
    { days: 90, label: t("pages.agent.usage.range_90") },
  ]
  return (
    <div className="border-border flex shrink-0 overflow-hidden rounded-lg border">
      {options.map(({ days, label }) => (
        <button
          key={days}
          type="button"
          onClick={() => onChange(days)}
          className={cn(
            "px-3 py-1.5 text-sm transition-colors",
            days !== options[0].days && "border-border border-l",
            value === days
              ? "bg-primary text-primary-foreground font-medium"
              : "text-muted-foreground hover:bg-accent hover:text-foreground",
          )}
        >
          {label}
        </button>
      ))}
    </div>
  )
}

// ─── token categories stacked bar ────────────────────────────────────────────

function TokenCategoriesCard({ totals }: { totals: Totals }) {
  const { t } = useTranslation()
  const cats = tokenCategories(totals)
  const inputTotal = totals.input_tokens ?? 0
  const outputTotal = totals.output_tokens ?? 0

  const inputCategories = [
    {
      key: "freshInput",
      label: t("pages.agent.usage.categories.fresh_input"),
      value: cats.freshInput,
      color: CAT_COLORS.freshInput,
      share: sharePercent(cats.freshInput, inputTotal),
    },
    {
      key: "cacheRead",
      label: t("pages.agent.usage.categories.cache_read"),
      value: cats.cacheRead,
      color: CAT_COLORS.cacheRead,
      share: sharePercent(cats.cacheRead, inputTotal),
    },
    {
      key: "cacheWrite",
      label: t("pages.agent.usage.categories.cache_write"),
      value: cats.cacheWrite,
      color: CAT_COLORS.cacheWrite,
      share: sharePercent(cats.cacheWrite, inputTotal),
    },
  ].filter((c) => c.value > 0)

  const outputCategories = [
    {
      key: "answer",
      label: t("pages.agent.usage.categories.answer"),
      value: cats.answer,
      color: CAT_COLORS.answer,
      share: sharePercent(cats.answer, outputTotal),
    },
    {
      key: "reasoning",
      label: t("pages.agent.usage.categories.reasoning"),
      value: cats.reasoning,
      color: CAT_COLORS.reasoning,
      share: sharePercent(cats.reasoning, outputTotal),
    },
  ].filter((c) => c.value > 0)

  const allCats = [...inputCategories, ...outputCategories]
  if (allCats.length === 0) return null

  const grandTotal = (totals.total_tokens ?? 0) || 1

  const cacheHitRate =
    inputTotal > 0 ? sharePercent(cats.cacheRead, inputTotal) : null

  return (
    <Card className="shadow-none">
      <CardContent className="space-y-4 py-5">
        <h3 className="font-medium">
          {t("pages.agent.usage.categories.title")}
        </h3>

        {/* horizontal stacked bar */}
        <div
          className="flex h-5 overflow-hidden rounded"
          role="img"
          aria-label={t("pages.agent.usage.categories.title")}
        >
          {allCats.map((cat) => (
            <div
              key={cat.key}
              style={{
                flex: `${cat.value} 0 0`,
                backgroundColor: cat.color,
              }}
              title={`${cat.label}: ${fmtInt(cat.value)}`}
            />
          ))}
        </div>

        {/* legend */}
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {allCats.map((cat) => (
            <div key={cat.key} className="flex items-center gap-2 text-sm">
              <span
                className="size-3 shrink-0 rounded-sm"
                style={{ backgroundColor: cat.color }}
                aria-hidden="true"
              />
              <span className="text-muted-foreground min-w-0 truncate">
                {cat.label}
              </span>
              <span className="text-foreground/80 ml-auto shrink-0 tabular-nums">
                {compactTokens(cat.value)}
              </span>
              <span className="text-muted-foreground w-14 shrink-0 text-right text-xs tabular-nums">
                {sharePercent(cat.value, grandTotal)}
              </span>
            </div>
          ))}
        </div>

        {cacheHitRate && (
          <p className="text-muted-foreground text-sm">
            {t("pages.agent.usage.categories.cache_hit_rate")}{" "}
            <span className="text-foreground/80 font-medium tabular-nums">
              {cacheHitRate}
            </span>
          </p>
        )}
      </CardContent>
    </Card>
  )
}

// ─── daily bar chart ──────────────────────────────────────────────────────────

function DailyChart({
  days,
  filledDays,
}: {
  days: number
  filledDays: DailyRow[]
}) {
  const { t } = useTranslation()
  const maxTotal = Math.max(...filledDays.map((d) => d.total_tokens ?? 0), 1)

  return (
    <div
      role="img"
      aria-label={t("pages.agent.usage.chart.aria_label", { days })}
    >
      <div
        className="flex items-end gap-[1px] overflow-hidden rounded"
        style={{ height: CHART_H }}
      >
        {filledDays.map((day) => {
          const cats = tokenCategories(day)
          const total = day.total_tokens ?? 0
          const barH = (total / maxTotal) * CHART_H

          if (barH < 1) {
            return (
              <div
                key={day.date}
                className="bg-muted/30 min-w-0 flex-1"
                style={{ height: 2 }}
                title={`${day.date}: 0`}
              />
            )
          }

          const segs = [
            { v: cats.reasoning, c: CAT_COLORS.reasoning },
            { v: cats.answer, c: CAT_COLORS.answer },
            { v: cats.cacheWrite, c: CAT_COLORS.cacheWrite },
            { v: cats.cacheRead, c: CAT_COLORS.cacheRead },
            { v: cats.freshInput, c: CAT_COLORS.freshInput },
          ].filter((s) => s.v > 0)

          const catSum =
            cats.freshInput +
            cats.cacheRead +
            cats.cacheWrite +
            cats.answer +
            cats.reasoning

          return (
            <div
              key={day.date}
              className="flex min-w-0 flex-1 flex-col"
              style={{ height: barH }}
              title={`${day.date}: ${fmtInt(total)} tokens`}
            >
              {segs.map((seg) => (
                <div
                  key={seg.c}
                  style={{
                    flex: `${catSum > 0 ? seg.v : 1} 0 0`,
                    backgroundColor: seg.c,
                  }}
                />
              ))}
            </div>
          )
        })}
      </div>

      {/* x-axis: first / last labels */}
      {filledDays.length >= 2 && (
        <div className="text-muted-foreground mt-1 flex justify-between text-[10px]">
          <span>{filledDays[0]?.date}</span>
          <span>{filledDays[filledDays.length - 1]?.date}</span>
        </div>
      )}

      {/* accessible text summary */}
      <p className="sr-only">
        {t("pages.agent.usage.chart.aria_label", { days })}.{" "}
        {filledDays
          .filter((d) => (d.total_tokens ?? 0) > 0)
          .map((d) => `${d.date}: ${fmtInt(d.total_tokens)} tokens`)
          .join("; ")}
      </p>
    </div>
  )
}

// ─── generic data table ───────────────────────────────────────────────────────

function UsageTable({
  rows,
  showModel = false,
  showLabel = false,
}: {
  rows: (Totals & { model?: string; label?: string })[]
  showModel?: boolean
  showLabel?: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm tabular-nums">
        <thead>
          <tr className="text-muted-foreground border-border border-b text-left text-xs">
            {(showModel || showLabel) && (
              <th className="pr-4 pb-2 font-medium">
                {showModel
                  ? t("pages.agent.usage.table.model")
                  : t("pages.agent.usage.table.session")}
              </th>
            )}
            <th className="pr-4 pb-2 text-right font-medium">
              {t("pages.agent.usage.table.calls")}
            </th>
            <th className="pr-4 pb-2 text-right font-medium">
              {t("pages.agent.usage.table.input")}
            </th>
            <th className="pr-4 pb-2 text-right font-medium">
              {t("pages.agent.usage.table.output")}
            </th>
            <th className="pr-4 pb-2 text-right font-medium">
              {t("pages.agent.usage.table.cache_read")}
            </th>
            <th className="pr-4 pb-2 text-right font-medium">
              {t("pages.agent.usage.table.total")}
            </th>
            <th className="pb-2 text-right font-medium">
              {t("pages.agent.usage.table.cost")}
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr
              key={(showModel ? row.model : row.label) ?? i}
              className="border-border/50 border-b last:border-0"
            >
              {(showModel || showLabel) && (
                <td className="py-2 pr-4">
                  <span
                    className="block max-w-[180px] truncate text-xs"
                    title={showModel ? row.model : row.label}
                  >
                    {showModel ? row.model : row.label}
                  </span>
                </td>
              )}
              <td className="py-2 pr-4 text-right text-xs">
                {fmtInt(row.requests)}
              </td>
              <td className="py-2 pr-4 text-right text-xs">
                {compactTokens(row.input_tokens ?? 0)}
              </td>
              <td className="py-2 pr-4 text-right text-xs">
                {compactTokens(row.output_tokens ?? 0)}
              </td>
              <td className="py-2 pr-4 text-right text-xs">
                {row.cache_read_tokens
                  ? compactTokens(row.cache_read_tokens)
                  : "—"}
              </td>
              <td className="py-2 pr-4 text-right text-xs font-medium">
                {compactTokens(row.total_tokens ?? 0)}
              </td>
              <td className="py-2 text-right text-xs">
                {formatCost(row.cost_usd)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ─── loading skeleton ─────────────────────────────────────────────────────────

function LoadingState() {
  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
        {Array.from({ length: 5 }).map((_, i) => (
          <Card key={i} className="shadow-none">
            <CardContent className="py-4">
              <Skeleton className="mb-2 h-4 w-24" />
              <Skeleton className="h-6 w-16" />
            </CardContent>
          </Card>
        ))}
      </div>
      <Card className="shadow-none">
        <CardContent className="py-5">
          <Skeleton className="h-32 w-full" />
        </CardContent>
      </Card>
    </div>
  )
}

// ─── main page ────────────────────────────────────────────────────────────────

export function UsagePage() {
  const { t } = useTranslation()
  const [days, setDays] = useState(30)
  const [showAllSessions, setShowAllSessions] = useState(false)

  const { data, isLoading, isFetching, error, refetch } = useQuery({
    queryKey: ["usage", days],
    queryFn: () => getUsage(days),
    staleTime: 60_000,
  })

  const period = data?.period
  const filledDays =
    period && period.daily.length >= 0
      ? fillDays(period.daily, period.since)
      : []

  const sessions: SessionRow[] = data?.sessions ?? []
  const models: ModelTotals[] = data?.models ?? []
  const visibleSessions = showAllSessions ? sessions : sessions.slice(0, 20)

  // Determine if the tracked_since note should be shown
  let trackedSinceNote: string | null = null
  if (period) {
    if (!period.tracked_since) {
      trackedSinceNote = t("pages.agent.usage.no_history")
    } else if (period.tracked_since > period.since) {
      const d = period.tracked_since.slice(0, 10)
      trackedSinceNote = t("pages.agent.usage.history_since", { date: d })
    }
  }

  return (
    <div className="bg-background flex h-full flex-col">
      <PageHeader title={t("navigation.usage", "Usage")}>
        <RangeSelector value={days} onChange={setDays} />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={isFetching}
          onClick={() => void refetch()}
        >
          <IconRefresh className={cn("size-4", isFetching && "animate-spin")} />
          {t("pages.agent.usage.refresh")}
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-auto px-6 py-6 pb-20">
        <div className="animate-in fade-in mx-auto w-full max-w-6xl space-y-8 pt-2 duration-300">
          <div className="max-w-2xl space-y-2">
            <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
              {t("pages.agent.usage.title")}
            </h1>
            <p className="text-muted-foreground/80 text-[14px] leading-relaxed">
              {t("pages.agent.usage.description")}
            </p>
          </div>

          {/* error state */}
          {error && !data && (
            <Card className="shadow-none">
              <CardContent className="flex flex-col items-center gap-4 py-14 text-center">
                <p className="text-destructive font-medium">
                  {error instanceof Error
                    ? error.message
                    : t("pages.agent.usage.load_error")}
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void refetch()}
                >
                  {t("pages.agent.usage.retry")}
                </Button>
              </CardContent>
            </Card>
          )}

          {/* loading state */}
          {isLoading && !data && <LoadingState />}

          {/* data */}
          {data && (
            <>
              {/* empty state */}
              {(data.totals.total_tokens ?? 0) === 0 && (
                <Card className="shadow-none">
                  <CardContent className="flex flex-col items-center gap-3 py-16 text-center">
                    <div className="bg-muted/60 text-muted-foreground flex size-12 items-center justify-center rounded-2xl">
                      <IconChartBar className="size-6" />
                    </div>
                    <p className="font-medium">
                      {t("pages.agent.usage.empty")}
                    </p>
                  </CardContent>
                </Card>
              )}

              {(data.totals.total_tokens ?? 0) > 0 && (
                <>
                  {/* all-time stat cards */}
                  <section aria-label={t("pages.agent.usage.section_alltime")}>
                    <h2 className="text-muted-foreground mb-3 text-sm font-semibold tracking-wide uppercase">
                      {t("pages.agent.usage.section_alltime")}
                    </h2>
                    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
                      <StatCard
                        icon={IconChartBar}
                        label={t("pages.agent.usage.stats.total_tokens")}
                        value={compactTokens(data.totals.total_tokens ?? 0)}
                      />
                      <StatCard
                        icon={IconDatabase}
                        label={t("pages.agent.usage.stats.input")}
                        value={compactTokens(data.totals.input_tokens ?? 0)}
                      />
                      <StatCard
                        icon={IconChartBar}
                        label={t("pages.agent.usage.stats.output")}
                        value={compactTokens(data.totals.output_tokens ?? 0)}
                      />
                      <StatCard
                        icon={IconChartBar}
                        label={t("pages.agent.usage.stats.cost")}
                        value={formatCost(data.totals.cost_usd)}
                      />
                      <StatCard
                        icon={IconChartBar}
                        label={t("pages.agent.usage.stats.llm_calls")}
                        value={fmtInt(data.totals.requests)}
                      />
                    </div>
                  </section>

                  {/* token categories card */}
                  <TokenCategoriesCard totals={data.totals} />

                  {/* period section */}
                  {period && (
                    <section
                      aria-label={t("pages.agent.usage.section_period", {
                        days,
                      })}
                    >
                      <h2 className="text-muted-foreground mb-3 text-sm font-semibold tracking-wide uppercase">
                        {t("pages.agent.usage.section_period", { days })}
                      </h2>

                      {trackedSinceNote && (
                        <p className="text-muted-foreground mb-3 text-xs">
                          {trackedSinceNote}
                        </p>
                      )}

                      {filledDays.length > 0 && (
                        <Card className="shadow-none">
                          <CardContent className="space-y-4 py-5">
                            <DailyChart days={days} filledDays={filledDays} />

                            {/* period totals inline */}
                            <div className="border-border/50 grid grid-cols-2 gap-x-6 gap-y-1 border-t pt-4 text-sm sm:grid-cols-4">
                              <div>
                                <span className="text-muted-foreground text-xs">
                                  {t("pages.agent.usage.stats.total_tokens")}
                                </span>
                                <p className="font-medium tabular-nums">
                                  {compactTokens(
                                    period.totals.total_tokens ?? 0,
                                  )}
                                </p>
                              </div>
                              <div>
                                <span className="text-muted-foreground text-xs">
                                  {t("pages.agent.usage.stats.input")}
                                </span>
                                <p className="font-medium tabular-nums">
                                  {compactTokens(
                                    period.totals.input_tokens ?? 0,
                                  )}
                                </p>
                              </div>
                              <div>
                                <span className="text-muted-foreground text-xs">
                                  {t("pages.agent.usage.stats.output")}
                                </span>
                                <p className="font-medium tabular-nums">
                                  {compactTokens(
                                    period.totals.output_tokens ?? 0,
                                  )}
                                </p>
                              </div>
                              <div>
                                <span className="text-muted-foreground text-xs">
                                  {t("pages.agent.usage.stats.cost")}
                                </span>
                                <p className="font-medium tabular-nums">
                                  {formatCost(period.totals.cost_usd)}
                                </p>
                              </div>
                            </div>
                          </CardContent>
                        </Card>
                      )}

                      {/* period per-model table */}
                      {period.models.length > 0 && (
                        <Card className="mt-4 shadow-none">
                          <CardContent className="py-5">
                            <UsageTable rows={period.models} showModel />
                          </CardContent>
                        </Card>
                      )}
                    </section>
                  )}

                  {/* all-time by model */}
                  {models.length > 0 && (
                    <section aria-label={t("pages.agent.usage.section_models")}>
                      <h2 className="text-muted-foreground mb-3 text-sm font-semibold tracking-wide uppercase">
                        {t("pages.agent.usage.section_models")}
                      </h2>
                      <Card className="shadow-none">
                        <CardContent className="py-5">
                          <UsageTable rows={models} showModel />
                        </CardContent>
                      </Card>
                    </section>
                  )}

                  {/* sessions table */}
                  {sessions.length > 0 && (
                    <section
                      aria-label={t("pages.agent.usage.section_sessions")}
                    >
                      <h2 className="text-muted-foreground mb-3 text-sm font-semibold tracking-wide uppercase">
                        {t("pages.agent.usage.section_sessions")}
                      </h2>
                      <Card className="shadow-none">
                        <CardContent className="py-5">
                          <UsageTable
                            rows={visibleSessions.map((s) => ({
                              ...s.usage,
                              label: s.label || s.key,
                            }))}
                            showLabel
                          />

                          {sessions.length > 20 && (
                            <div className="mt-4 flex justify-center">
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => setShowAllSessions((v) => !v)}
                              >
                                {showAllSessions
                                  ? t("pages.agent.usage.table.show_less")
                                  : t("pages.agent.usage.table.show_all", {
                                      count: sessions.length,
                                    })}
                              </Button>
                            </div>
                          )}
                        </CardContent>
                      </Card>
                    </section>
                  )}
                </>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
