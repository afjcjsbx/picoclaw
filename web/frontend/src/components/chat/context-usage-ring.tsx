import { IconArrowRight } from "@tabler/icons-react"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import type { ConversationUsageStats } from "@/features/chat/usage"
import {
  formatCompactTokenCount,
  formatContextTokenCount,
  formatConversationCostUsd,
} from "@/features/chat/usage"
import type { ContextUsage } from "@/store/chat"

interface ContextUsageRingProps {
  usage: ContextUsage
  conversationUsage?: ConversationUsageStats
  onOpen?: () => void
  onDetailClick?: () => void
}

export function ContextUsageRing({
  usage,
  conversationUsage,
  onOpen,
  onDetailClick,
}: ContextUsageRingProps) {
  const { t } = useTranslation()
  const [intent, setIntent] = useState(false) // user wants open
  const [visible, setVisible] = useState(false) // DOM mounted
  const [animated, setAnimated] = useState(false) // CSS target state
  const [cooldown, setCooldown] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const timerRef = useRef<ReturnType<typeof setTimeout>>(null)
  const hoverIntent = useRef<ReturnType<typeof setTimeout>>(null)
  const closeTimer = useRef<ReturnType<typeof setTimeout>>(null)

  useEffect(() => {
    if (intent) {
      // Mount first, animate in on next frame
      if (closeTimer.current) clearTimeout(closeTimer.current)
      setVisible(true)
      requestAnimationFrame(() => {
        requestAnimationFrame(() => setAnimated(true))
      })
    } else if (visible) {
      // Animate out, then unmount
      setAnimated(false)
      closeTimer.current = setTimeout(() => setVisible(false), 150)
    }
  }, [intent, visible])

  useEffect(() => {
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
      if (hoverIntent.current) clearTimeout(hoverIntent.current)
      if (closeTimer.current) clearTimeout(closeTimer.current)
    }
  }, [])

  const percent = Math.min(
    100,
    usage.total_tokens > 0
      ? Math.round((usage.used_tokens / usage.total_tokens) * 100)
      : usage.used_percent,
  )
  const radius = 8
  const circumference = 2 * Math.PI * radius
  const offset = circumference - (percent / 100) * circumference

  const handleDetail = () => {
    if (cooldown || !onDetailClick) return
    setCooldown(true)
    onDetailClick()
    setIntent(false)
    timerRef.current = setTimeout(() => setCooldown(false), 1000)
  }

  // Desktop: hover to open, mouse leave to close (with small delay)
  const handleMouseEnter = () => {
    if (hoverIntent.current) clearTimeout(hoverIntent.current)
    onOpen?.()
    setIntent(true)
  }

  const handleMouseLeave = () => {
    hoverIntent.current = setTimeout(() => setIntent(false), 150)
  }

  // Mobile: tap to toggle (preventDefault suppresses synthetic mouseenter)
  const handleTouchStart = (e: React.TouchEvent) => {
    e.preventDefault()
    if (!intent) onOpen?.()
    setIntent((v) => !v)
  }

  const cachePercent = conversationUsage?.cacheHitPercent
  const cacheLabel =
    cachePercent === undefined
      ? "—"
      : `${Math.round(Math.min(100, Math.max(0, cachePercent)))}%`
  const tokensLabel = conversationUsage
    ? `${formatCompactTokenCount(conversationUsage.inputTokens)} ↑ · ${formatCompactTokenCount(conversationUsage.outputTokens)} ↓`
    : "—"

  return (
    <div
      ref={containerRef}
      className="relative"
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
    >
      <button
        type="button"
        onTouchStart={handleTouchStart}
        aria-label={`${t("chat.contextWindow")}: ${percent}% ${t("chat.contextUsed")}`}
        title={`${t("chat.contextWindow")}: ${percent}% ${t("chat.contextUsed")}`}
        className="relative flex h-6 w-6 cursor-pointer items-center justify-center transition-opacity hover:opacity-70"
      >
        <svg
          className="h-6 w-6 -rotate-90"
          viewBox="0 0 20 20"
          aria-hidden="true"
        >
          <circle
            cx="10"
            cy="10"
            r={radius}
            fill="none"
            className="stroke-muted-foreground/25"
            strokeWidth="2"
          />
          <circle
            cx="10"
            cy="10"
            r={radius}
            fill="none"
            className="stroke-muted-foreground"
            strokeWidth="2"
            strokeLinecap="round"
            strokeDasharray={circumference}
            strokeDashoffset={offset}
          />
        </svg>
      </button>

      {visible && (
        <div
          className={`bg-popover text-popover-foreground absolute right-0 bottom-full z-50 mb-3 w-[272px] max-w-[calc(100vw-2rem)] rounded-2xl border p-4 shadow-xl transition-all duration-150 ${
            animated
              ? "scale-100 opacity-100"
              : "pointer-events-none scale-95 opacity-0"
          }`}
        >
          <div className="text-base font-medium">{t("chat.contextWindow")}</div>
          <div className="mt-1 text-lg font-semibold tabular-nums">
            {percent}% {t("chat.contextUsed")}
          </div>
          <div className="text-muted-foreground mt-1 text-sm tabular-nums">
            {formatContextTokenCount(usage.used_tokens)} /{" "}
            {formatContextTokenCount(usage.total_tokens)}{" "}
            {t("chat.contextTokensWord")}
          </div>

          <dl className="mt-4 space-y-2 text-sm">
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">
                {t("chat.contextTokens")}
              </dt>
              <dd className="tabular-nums">{tokensLabel}</dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">
                {t("chat.contextCacheLastReply")}
              </dt>
              <dd className="text-emerald-600 tabular-nums dark:text-emerald-400">
                {cacheLabel}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-4">
              <dt className="text-muted-foreground">{t("chat.contextCost")}</dt>
              <dd className="tabular-nums">
                {formatConversationCostUsd(conversationUsage?.costUsd)}
              </dd>
            </div>
          </dl>

          {onDetailClick && (
            <button
              type="button"
              onClick={handleDetail}
              disabled={cooldown}
              className="text-muted-foreground hover:text-foreground mt-3 inline-flex items-center gap-1 text-xs font-medium transition-colors disabled:opacity-40"
            >
              {t("chat.contextDetail")}
              <IconArrowRight className="h-3 w-3" />
            </button>
          )}
        </div>
      )}
    </div>
  )
}
