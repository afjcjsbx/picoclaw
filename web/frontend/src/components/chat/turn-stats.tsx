import { IconChartBar } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  cacheHitRate,
  formatCompactTokenCount,
  formatDuration,
  formatTokenCount,
  formatTokensPerSecond,
  tokensPerSecond,
} from "@/features/chat/usage"
import type { TokenUsage } from "@/store/chat"

interface TurnStatsProps {
  usage: TokenUsage
  modelName?: string
  isDefaultModel?: boolean
}

function formatTurnRate(value: number): string {
  return value >= 10 ? String(Math.round(value)) : formatTokensPerSecond(value)
}

export function TurnStats({
  usage,
  modelName,
  isDefaultModel = false,
}: TurnStatsProps) {
  const { t } = useTranslation()
  const responseRate = tokensPerSecond(usage)
  const wholeTurnRate = tokensPerSecond(usage, usage.elapsed_ms ?? 0)
  const cacheRate = cacheHitRate(usage)
  const hasTokenBreakdown =
    usage.input_tokens !== undefined || usage.output_tokens !== undefined
  const rows: Array<readonly [string, ReactNode]> = [
    [
      t("chat.turnStats.model"),
      modelName ? (
        <span className="flex max-w-52 items-center justify-end gap-1.5 text-right">
          <span className="min-w-0 break-all">{modelName}</span>
          {isDefaultModel && (
            <span className="shrink-0 text-zinc-500">
              {t("chat.turnStats.defaultModel", {
                defaultValue: "(default)",
              })}
            </span>
          )}
        </span>
      ) : (
        "—"
      ),
    ],
    [
      t("chat.turnStats.response"),
      responseRate === undefined
        ? "—"
        : t("chat.turnStats.responseRate", {
            rate: formatTurnRate(responseRate),
            defaultValue: "~{{rate}} tok/s",
          }),
    ],
    [
      t("chat.turnStats.wholeTurn"),
      wholeTurnRate === undefined
        ? "—"
        : t("chat.turnStats.wholeTurnRate", {
            rate: formatTurnRate(wholeTurnRate),
            defaultValue: "~{{rate}} tok/s",
          }),
    ],
    [
      t("chat.turnStats.elapsed"),
      usage.elapsed_ms === undefined ? "—" : formatDuration(usage.elapsed_ms),
    ],
    [
      t("chat.turnStats.modelTime"),
      usage.duration_ms === undefined ? "—" : formatDuration(usage.duration_ms),
    ],
    [
      t("chat.turnStats.toolTime"),
      usage.tool_duration_ms === undefined
        ? "—"
        : formatDuration(usage.tool_duration_ms),
    ],
    [
      t("chat.turnStats.averageTtft"),
      usage.average_ttft_ms === undefined
        ? "—"
        : formatDuration(usage.average_ttft_ms),
    ],
    [
      t("chat.turnStats.steps"),
      usage.requests === undefined ? "—" : formatTokenCount(usage.requests),
    ],
    [
      t("chat.turnStats.tokens"),
      hasTokenBreakdown
        ? `${formatCompactTokenCount(usage.input_tokens ?? 0)} ↑ · ${formatCompactTokenCount(usage.output_tokens ?? 0)} ↓`
        : "—",
    ],
    [
      t("chat.turnStats.cache"),
      cacheRate === undefined ? (
        "—"
      ) : (
        <span className="text-emerald-400">
          {Math.round(Math.min(100, Math.max(0, cacheRate)))}%
        </span>
      ),
    ],
  ]

  return (
    <TooltipProvider delayDuration={200} skipDelayDuration={0}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-6"
            aria-label={t("chat.turnStats.open")}
          >
            <IconChartBar className="size-3.5" aria-hidden="true" />
          </Button>
        </TooltipTrigger>
        <TooltipContent
          side="top"
          align="end"
          arrowClassName="bg-zinc-900 fill-zinc-900"
          className="w-80 max-w-[calc(100vw-2rem)] border border-zinc-700 bg-zinc-900 p-4 text-left text-zinc-100 shadow-xl"
        >
          <p className="mb-3 text-sm font-medium">
            {t("chat.turnStats.title")}
          </p>
          <dl className="grid grid-cols-[1fr_auto] items-center gap-x-6 gap-y-1.5 text-xs">
            {rows.map(([label, value]) => (
              <div key={label} className="contents">
                <dt className="text-zinc-400">{label}</dt>
                <dd className="text-right tabular-nums">{value}</dd>
              </div>
            ))}
          </dl>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}
