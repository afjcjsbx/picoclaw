import {
  IconAdjustments,
  IconArrowsLeftRight,
  IconEraser,
  IconGauge,
  IconHelpCircle,
  IconListDetails,
  IconMessageQuestion,
  IconPlayerPlay,
  IconPlayerStop,
  IconPlugConnected,
  IconRotate,
  IconSparkles,
  IconTerminal2,
  IconUsers,
  IconVolume,
  type TablerIcon,
} from "@tabler/icons-react"
import { useLayoutEffect, useRef } from "react"
import { useTranslation } from "react-i18next"

import type { SlashPaletteItem } from "@/features/chat/slash-commands"
import { cn } from "@/lib/utils"

const PALETTE_MAX_HEIGHT_PX = 288
const PALETTE_CHROME_PX = 12

const SLASH_COMMAND_ICONS: Record<string, TablerIcon> = {
  "/start": IconPlayerPlay,
  "/help": IconHelpCircle,
  "/stop": IconPlayerStop,
  "/show": IconAdjustments,
  "/list": IconListDetails,
  "/use": IconSparkles,
  "/btw": IconMessageQuestion,
  "/switch": IconArrowsLeftRight,
  "/check": IconPlugConnected,
  "/clear": IconEraser,
  "/context": IconGauge,
  "/subagents": IconUsers,
  "/reload": IconRotate,
  "/voice": IconVolume,
}

interface SlashCommandPaletteProps {
  items: SlashPaletteItem[]
  selectedIndex: number
  onHover: (index: number) => void
  onChoose: (item: SlashPaletteItem) => void
}

export function SlashCommandPalette({
  items,
  selectedIndex,
  onHover,
  onChoose,
}: SlashCommandPaletteProps) {
  const { t } = useTranslation()
  const listRef = useRef<HTMLDivElement>(null)

  useLayoutEffect(() => {
    const option = listRef.current?.querySelector<HTMLElement>(
      `[data-palette-index="${selectedIndex}"]`,
    )
    if (typeof option?.scrollIntoView === "function") {
      option.scrollIntoView({ block: "nearest" })
    }
  }, [selectedIndex])

  return (
    <div
      role="listbox"
      aria-label={t("chat.slash.ariaLabel")}
      style={{ maxHeight: PALETTE_MAX_HEIGHT_PX }}
      className={cn(
        "rounded-floating bg-popover text-popover-foreground absolute bottom-full left-1/2 z-30 mb-2 w-[calc(100%-0.5rem)] -translate-x-1/2 overflow-hidden p-1.5 shadow-[0_8px_24px_rgba(15,23,42,0.10)] dark:shadow-[0_12px_28px_rgba(0,0,0,0.32)]",
      )}
    >
      <div
        ref={listRef}
        className="overflow-y-auto pr-0.5"
        style={{ maxHeight: PALETTE_MAX_HEIGHT_PX - PALETTE_CHROME_PX }}
      >
        {items.map((item, index) => {
          const Icon = SLASH_COMMAND_ICONS[item.command] ?? IconTerminal2
          const selected = index === selectedIndex
          return (
            <button
              key={item.command}
              type="button"
              role="option"
              data-palette-index={index}
              aria-selected={selected}
              onMouseEnter={() => onHover(index)}
              onMouseDown={(event) => {
                // Keep focus in the textarea so the caret stays where the user was typing.
                event.preventDefault()
                onChoose(item)
              }}
              className={cn(
                "rounded-control flex min-h-[44px] w-full items-center gap-3 px-3 py-2 text-left text-[13px] transition-colors outline-none select-none",
                selected
                  ? "bg-foreground/[0.065] text-foreground dark:bg-white/[0.09]"
                  : "text-foreground/90 hover:bg-foreground/[0.045] dark:hover:bg-white/[0.065]",
              )}
            >
              <span
                className={cn(
                  "text-muted-foreground flex h-7 w-7 shrink-0 items-center justify-center transition-colors",
                  selected && "text-foreground",
                )}
              >
                <Icon className="h-4 w-4" />
              </span>
              <span className="flex min-w-0 flex-1 flex-col gap-0.5 sm:flex-row sm:items-baseline sm:gap-2">
                <span className="text-foreground min-w-0 truncate text-[13.5px] font-semibold tracking-normal">
                  {item.title}
                </span>
                <span className="text-muted-foreground min-w-0 truncate text-[13px]">
                  {item.description}
                </span>
              </span>
              <span className="ml-2 flex max-w-[42%] shrink-0 items-center gap-1.5 sm:max-w-none">
                {item.recent ? (
                  <span className="bg-foreground/[0.055] text-muted-foreground hidden rounded-full px-2 py-1 text-[11px] font-medium sm:inline-flex">
                    {t("chat.slash.recent")}
                  </span>
                ) : null}
                <span className="text-muted-foreground/60 font-mono text-[12px]">
                  {item.argHint
                    ? `${item.command} ${item.argHint}`
                    : item.command}
                </span>
              </span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
