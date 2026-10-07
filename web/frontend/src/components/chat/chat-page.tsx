import {
  IconActivity,
  IconArrowBackUp,
  IconChevronDown,
  IconGitFork,
  IconLayoutColumns,
  IconLayoutDashboard,
  IconLayoutGrid,
  IconLayoutRows,
  IconLayoutSidebarRight,
  IconPlus,
  IconX,
} from "@tabler/icons-react"
import type { TFunction } from "i18next"
import { useAtom, useAtomValue } from "jotai"
import {
  type ChangeEvent,
  Children,
  type ClipboardEvent,
  type DragEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { forkSession, getSessionHistory } from "@/api/sessions"
import { AssistantMessage } from "@/components/chat/assistant-message"
import {
  ChatComposer,
  type ChatInputDisabledReason,
} from "@/components/chat/chat-composer"
import { ChatEmptyState } from "@/components/chat/chat-empty-state"
import { ModelSelector } from "@/components/chat/model-selector"
import { TypingIndicator } from "@/components/chat/typing-indicator"
import { UserMessage } from "@/components/chat/user-message"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { loadSessionMessages } from "@/features/chat/history"
import {
  CHAT_IMAGE_ACCEPT,
  buildChatImageAttachments,
  getTransferredFiles,
  hasFileTransfer,
} from "@/features/chat/image-input"
import {
  mergeSplitConversations,
  removeSplitConversation,
} from "@/features/chat/split-groups"
import { useChatModels } from "@/hooks/use-chat-models"
import { useGateway } from "@/hooks/use-gateway"
import { usePicoChat } from "@/hooks/use-pico-chat"
import type { AssistantDetailVisibility } from "@/store/chat"
import type {
  ChatAttachment,
  ChatMessage,
  ConnectionState,
  SplitLayout,
  SplitLayoutSizes,
  SplitSessionState,
} from "@/store/chat"
import {
  assistantDetailVisibilityAtom,
  sessionTitlesAtom,
  shouldShowAssistantMessage,
  splitConversationsAtom,
  splitLayoutSizesAtom,
  splitLayoutsAtom,
  splitSessionStatesAtom,
} from "@/store/chat"
import type { GatewayState } from "@/store/gateway"

const SPLIT_LAYOUTS: Array<{
  value: SplitLayout
  label: string
  icon: typeof IconLayoutColumns
}> = [
  { value: "columns", label: "Columns", icon: IconLayoutColumns },
  { value: "rows", label: "Rows", icon: IconLayoutRows },
  { value: "grid", label: "Grid", icon: IconLayoutGrid },
  { value: "bsp", label: "BSP", icon: IconLayoutDashboard },
  {
    value: "main-stack",
    label: "Main and stack",
    icon: IconLayoutSidebarRight,
  },
]

function AssistantActivityGroup({
  label,
  status,
  children,
}: {
  label: string
  status?: ReactNode
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  const hasDetails = Children.count(children) > 0

  return (
    <Collapsible
      open={open}
      onOpenChange={setOpen}
      className="group/activity w-full"
    >
      {hasDetails ? (
        <CollapsibleTrigger className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex w-full min-w-0 items-center gap-1 rounded-sm text-left focus-visible:ring-2 focus-visible:outline-none">
          {status ?? (
            <span className="flex min-w-0 flex-1 items-center gap-1.5 py-0.5 text-[12px] leading-4">
              <IconActivity aria-hidden="true" className="size-3.5 shrink-0" />
              <span className="truncate">{label}</span>
            </span>
          )}
          <IconChevronDown className="size-3.5 shrink-0 transition-transform group-data-[state=open]/activity:rotate-180" />
        </CollapsibleTrigger>
      ) : (
        status
      )}
      {hasDetails && (
        <CollapsibleContent>
          <div className="mt-0.5 space-y-0.75">{children}</div>
        </CollapsibleContent>
      )}
    </Collapsible>
  )
}

function splitGroupKey(sessions: string[]) {
  return [...sessions].sort().join("\u0000")
}

type SplitAxis = "columns" | "rows"

interface SplitDivider {
  axis: SplitAxis
  index: number
  style: React.CSSProperties
}

function getDefaultSplitSizes(
  layout: SplitLayout,
  count: number,
): SplitLayoutSizes {
  if (layout === "rows") {
    return {
      columns: [1],
      rows: Array.from({ length: count }, () => 1 / count),
    }
  }

  if (layout === "grid") {
    const columns = Math.ceil(Math.sqrt(count))
    const rows = Math.ceil(count / columns)
    return {
      columns: Array.from({ length: columns }, () => 1 / columns),
      rows: Array.from({ length: rows }, () => 1 / rows),
    }
  }

  if (layout === "main-stack") {
    return {
      columns: [1.65 / 2.65, 1 / 2.65],
      rows: Array.from({ length: count - 1 }, () => 1 / (count - 1)),
    }
  }

  if (layout === "bsp" && count === 3) {
    return { columns: [0.5, 0.5], rows: [0.5, 0.5] }
  }

  if (layout === "bsp" && count >= 4) {
    return { columns: [0.5, 0.25, 0.25], rows: [0.5, 0.5] }
  }

  return {
    columns: Array.from({ length: count }, () => 1 / count),
    rows: [1],
  }
}

function normalizeSplitSizes(sizes: number[] | undefined, defaults: number[]) {
  if (
    !sizes ||
    sizes.length !== defaults.length ||
    sizes.some((size) => !Number.isFinite(size) || size <= 0)
  ) {
    return defaults
  }
  const total = sizes.reduce((sum, size) => sum + size, 0)
  return sizes.map((size) => size / total)
}

function getSplitLayoutGeometry(
  layout: SplitLayout,
  count: number,
  savedSizes?: SplitLayoutSizes,
) {
  const defaults = getDefaultSplitSizes(layout, count)
  const sizes = {
    columns: normalizeSplitSizes(savedSizes?.columns, defaults.columns),
    rows: normalizeSplitSizes(savedSizes?.rows, defaults.rows),
  }
  const gridStyle: React.CSSProperties = {
    gridTemplateColumns: sizes.columns
      .map((size) => `minmax(0, ${size}fr)`)
      .join(" "),
    gridTemplateRows: sizes.rows
      .map((size) => `minmax(0, ${size}fr)`)
      .join(" "),
  }
  const paneStyles: Array<React.CSSProperties | undefined> = []
  const dividers: SplitDivider[] = []
  const columnOffset = (index: number) =>
    `${sizes.columns.slice(0, index).reduce((sum, size) => sum + size, 0) * 100}%`
  const rowOffset = (index: number) =>
    `${sizes.rows.slice(0, index).reduce((sum, size) => sum + size, 0) * 100}%`
  const addColumnDividers = (first = 1, last = sizes.columns.length - 1) => {
    for (let index = first; index <= last; index += 1) {
      dividers.push({
        axis: "columns",
        index: index - 1,
        style: { left: columnOffset(index), top: 0, bottom: 0 },
      })
    }
  }
  const addRowDividers = (left = "0%") => {
    for (let index = 1; index < sizes.rows.length; index += 1) {
      dividers.push({
        axis: "rows",
        index: index - 1,
        style: { top: rowOffset(index), left, right: 0 },
      })
    }
  }

  if (layout === "rows") {
    addRowDividers()
  } else if (layout === "grid") {
    addColumnDividers()
    addRowDividers()
  } else if (layout === "main-stack") {
    addColumnDividers()
    addRowDividers(columnOffset(1))
  } else if (layout === "bsp" && count === 3) {
    addColumnDividers()
    addRowDividers(columnOffset(1))
  } else if (layout === "bsp" && count >= 4) {
    addColumnDividers(1, 1)
    dividers.push({
      axis: "columns",
      index: 1,
      style: { left: columnOffset(2), top: rowOffset(1), bottom: 0 },
    })
    dividers.push({
      axis: "rows",
      index: 0,
      style: { top: rowOffset(1), left: columnOffset(1), right: 0 },
    })
  } else {
    addColumnDividers()
  }

  if (layout === "main-stack") {
    paneStyles.push({ gridColumn: "1", gridRow: `1 / ${count}` })
    for (let index = 0; index < count - 1; index += 1) {
      paneStyles.push({ gridColumn: "2", gridRow: index + 1 })
    }
  } else if (layout === "bsp" && count === 3) {
    paneStyles.push(
      { gridColumn: "1", gridRow: "1 / 3" },
      { gridColumn: "2", gridRow: "1" },
      { gridColumn: "2", gridRow: "2" },
    )
  } else if (layout === "bsp" && count >= 4) {
    paneStyles.push(
      { gridColumn: "1", gridRow: "1 / 3" },
      { gridColumn: "2 / 4", gridRow: "1" },
      { gridColumn: "2", gridRow: "2" },
      { gridColumn: "3", gridRow: "2" },
    )
  }

  return { gridStyle, paneStyles, dividers, sizes }
}

const MIN_SPLIT_PANE_SIZE = 160

function resizeSplitSizes(
  sizes: SplitLayoutSizes,
  axis: SplitAxis,
  index: number,
  delta: number,
  extent: number,
): SplitLayoutSizes {
  const tracks = [...sizes[axis]]
  const pairSize = tracks[index] + tracks[index + 1]
  const minimum = Math.min(
    MIN_SPLIT_PANE_SIZE / Math.max(extent, 1),
    pairSize / 2,
  )
  tracks[index] = Math.max(
    minimum,
    Math.min(pairSize - minimum, tracks[index] + delta),
  )
  tracks[index + 1] = pairSize - tracks[index]
  return { ...sizes, [axis]: tracks }
}

interface SplitResizeHandleProps extends SplitDivider {
  sizes: SplitLayoutSizes
  onResize: (sizes: SplitLayoutSizes) => void
  label: string
}

function SplitResizeHandle({
  axis,
  index,
  style,
  sizes,
  onResize,
  label,
}: SplitResizeHandleProps) {
  const dragRef = useRef<{
    pointerId: number
    position: number
    extent: number
    sizes: SplitLayoutSizes
  } | null>(null)
  const orientation = axis === "columns" ? "vertical" : "horizontal"
  const currentSize = sizes[axis][index]

  const getExtent = (element: HTMLElement) => {
    const rect = element.parentElement?.getBoundingClientRect()
    return rect ? (axis === "columns" ? rect.width : rect.height) : 0
  }
  const getPosition = (event: React.PointerEvent) =>
    axis === "columns" ? event.clientX : event.clientY

  return (
    <div
      role="separator"
      aria-label={label}
      aria-orientation={orientation}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(currentSize * 100)}
      tabIndex={0}
      style={style}
      onPointerDown={(event) => {
        if (event.button !== 0) return
        const extent = getExtent(event.currentTarget)
        if (!extent) return
        event.preventDefault()
        event.currentTarget.setPointerCapture(event.pointerId)
        dragRef.current = {
          pointerId: event.pointerId,
          position: getPosition(event),
          extent,
          sizes,
        }
      }}
      onPointerMove={(event) => {
        const drag = dragRef.current
        if (!drag || drag.pointerId !== event.pointerId) return
        const delta = (getPosition(event) - drag.position) / drag.extent
        onResize(resizeSplitSizes(drag.sizes, axis, index, delta, drag.extent))
      }}
      onPointerUp={(event) => {
        dragRef.current = null
        if (event.currentTarget.hasPointerCapture(event.pointerId)) {
          event.currentTarget.releasePointerCapture(event.pointerId)
        }
      }}
      onPointerCancel={() => {
        dragRef.current = null
      }}
      onLostPointerCapture={() => {
        dragRef.current = null
      }}
      onKeyDown={(event) => {
        const extent = getExtent(event.currentTarget)
        const pairSize = currentSize + sizes[axis][index + 1]
        const minimum = Math.min(
          MIN_SPLIT_PANE_SIZE / Math.max(extent, 1),
          pairSize / 2,
        )
        let delta: number
        if (event.key === "Home") {
          delta = minimum - currentSize
        } else if (event.key === "End") {
          delta = pairSize - minimum - currentSize
        } else if (
          (axis === "columns" && event.key === "ArrowLeft") ||
          (axis === "rows" && event.key === "ArrowUp")
        ) {
          delta = -24 / Math.max(extent, 1)
        } else if (
          (axis === "columns" && event.key === "ArrowRight") ||
          (axis === "rows" && event.key === "ArrowDown")
        ) {
          delta = 24 / Math.max(extent, 1)
        } else {
          return
        }
        event.preventDefault()
        onResize(resizeSplitSizes(sizes, axis, index, delta, extent))
      }}
      className={`group focus-visible:ring-ring absolute z-20 flex touch-none items-center justify-center outline-none select-none focus-visible:ring-2 ${axis === "columns" ? "top-0 bottom-0 w-2 -translate-x-1/2 cursor-col-resize" : "right-0 left-0 h-2 -translate-y-1/2 cursor-row-resize"}`}
    >
      <span
        aria-hidden="true"
        className={`${axis === "columns" ? "h-full w-px" : "h-px w-full"} bg-border/70 group-hover:bg-primary group-focus-visible:bg-primary transition-colors`}
      />
    </div>
  )
}

function resolveChatInputDisabledReason({
  hasDefaultModel,
  connectionState,
  gatewayState,
}: {
  hasDefaultModel: boolean
  connectionState: ConnectionState
  gatewayState: GatewayState
}): ChatInputDisabledReason | null {
  if (gatewayState === "unknown") {
    return "gatewayUnknown"
  }

  if (gatewayState === "starting") {
    return "gatewayStarting"
  }

  if (gatewayState === "restarting") {
    return "gatewayRestarting"
  }

  if (gatewayState === "stopping") {
    return "gatewayStopping"
  }

  if (gatewayState === "stopped") {
    return "gatewayStopped"
  }

  if (gatewayState === "error") {
    return "gatewayError"
  }

  if (connectionState === "connecting") {
    return "websocketConnecting"
  }

  if (connectionState === "error") {
    return "websocketError"
  }

  if (connectionState === "disconnected") {
    return "websocketDisconnected"
  }

  if (!hasDefaultModel) {
    return "noDefaultModel"
  }

  return null
}

interface SplitConversationViewProps {
  sessions: string[]
  layout: SplitLayout
  activeSessionId: string
  activeHistoryStart: number
  hasHydratedActiveSession: boolean
  sessionTitles: Record<string, string>
  splitSessionStates: Record<string, SplitSessionState>
  messages: ChatMessage[]
  isTyping: boolean
  renderMessages: (
    messages: ChatMessage[],
    sessionId: string,
    historyStart: number,
    isTyping: boolean,
  ) => React.ReactNode
  onActivate: (sessionId: string) => void
  onRemove: (sessionId: string) => void
  onScroll: (event: React.UIEvent<HTMLDivElement>) => void
  activeEmptyState: React.ReactNode
  t: TFunction
}

function SplitConversationView({
  sessions,
  layout,
  activeSessionId,
  activeHistoryStart,
  hasHydratedActiveSession,
  sessionTitles,
  splitSessionStates,
  messages,
  isTyping,
  renderMessages,
  onActivate,
  onRemove,
  onScroll,
  activeEmptyState,
  t,
}: SplitConversationViewProps) {
  const [storedSizes, setStoredSizes] = useAtom(splitLayoutSizesAtom)
  const sizeKey = `${splitGroupKey(sessions)}\u0001${layout}`
  const { gridStyle, paneStyles, dividers, sizes } = getSplitLayoutGeometry(
    layout,
    sessions.length,
    storedSizes[sizeKey],
  )
  const [history, setHistory] = useState<Record<string, ChatMessage[] | null>>(
    {},
  )
  const activeSessionRef = useRef(activeSessionId)
  const hydratedSessionRef = useRef(hasHydratedActiveSession)
  const sessionSnapshotsRef = useRef(new Set<string>())
  const sessionsKey = JSON.stringify(sessions)

  useLayoutEffect(() => {
    activeSessionRef.current = activeSessionId
    hydratedSessionRef.current = hasHydratedActiveSession
  }, [activeSessionId, hasHydratedActiveSession])

  useEffect(() => {
    let cancelled = false
    for (const sessionId of JSON.parse(sessionsKey) as string[]) {
      void loadSessionMessages(sessionId)
        .then((page) => {
          if (
            cancelled ||
            sessionSnapshotsRef.current.has(sessionId) ||
            (sessionId === activeSessionRef.current &&
              hydratedSessionRef.current)
          ) {
            return
          }
          setHistory((current) => ({ ...current, [sessionId]: page.messages }))
        })
        .catch((error) => {
          console.error("Failed to load split conversation:", error)
          if (
            cancelled ||
            sessionSnapshotsRef.current.has(sessionId) ||
            (sessionId === activeSessionRef.current &&
              hydratedSessionRef.current)
          ) {
            return
          }
          setHistory((current) => ({ ...current, [sessionId]: null }))
        })
    }
    return () => {
      cancelled = true
    }
  }, [sessionsKey])

  useEffect(() => {
    if (!hasHydratedActiveSession) return
    sessionSnapshotsRef.current.add(activeSessionId)
    setHistory((current) => ({ ...current, [activeSessionId]: messages }))
  }, [activeSessionId, hasHydratedActiveSession, messages])

  return (
    <div
      className="relative grid h-full min-h-0 min-w-0 gap-0 overflow-hidden"
      style={gridStyle}
    >
      {sessions.map((sessionId, index) => {
        const isActive = sessionId === activeSessionId
        const paneMessages =
          isActive && hasHydratedActiveSession
            ? messages
            : (splitSessionStates[sessionId]?.messages ??
              history[sessionId] ??
              (isActive ? messages : undefined))
        const title =
          sessionTitles[sessionId] ||
          t("chat.splitConversation", { number: index + 1 })

        return (
          <SplitConversationPane
            key={sessionId}
            sessionId={sessionId}
            index={index}
            title={title}
            style={paneStyles[index]}
            messages={paneMessages ?? []}
            historyStart={isActive ? activeHistoryStart : 0}
            isLoaded={
              (isActive && hasHydratedActiveSession) ||
              sessionId in splitSessionStates ||
              sessionId in history ||
              (isActive && messages.length > 0)
            }
            isActive={isActive}
            isTyping={
              isActive
                ? isTyping
                : (splitSessionStates[sessionId]?.isTyping ?? false)
            }
            renderMessages={renderMessages}
            onActivate={onActivate}
            onRemove={onRemove}
            onScroll={onScroll}
            emptyContent={isActive ? activeEmptyState : null}
            loadError={paneMessages === null}
            t={t}
          />
        )
      })}
      {dividers.map((divider) => (
        <SplitResizeHandle
          key={`${divider.axis}-${divider.index}`}
          {...divider}
          sizes={sizes}
          onResize={(nextSizes) =>
            setStoredSizes((current) => ({ ...current, [sizeKey]: nextSizes }))
          }
          label={t("chat.resizeSplitPane", {
            defaultValue: "Resize conversation panes",
          })}
        />
      ))}
    </div>
  )
}

interface SplitConversationPaneProps {
  sessionId: string
  index: number
  title: string
  style?: React.CSSProperties
  messages: ChatMessage[]
  historyStart: number
  isLoaded: boolean
  isActive: boolean
  isTyping: boolean
  renderMessages: SplitConversationViewProps["renderMessages"]
  onActivate: SplitConversationViewProps["onActivate"]
  onRemove: SplitConversationViewProps["onRemove"]
  onScroll: SplitConversationViewProps["onScroll"]
  emptyContent: React.ReactNode
  loadError: boolean
  t: SplitConversationViewProps["t"]
}

function SplitConversationPane({
  sessionId,
  index,
  title,
  style,
  messages,
  historyStart,
  isLoaded,
  isActive,
  isTyping,
  renderMessages,
  onActivate,
  onRemove,
  onScroll,
  emptyContent,
  loadError,
  t,
}: SplitConversationPaneProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickToBottomRef = useRef(true)

  useEffect(() => {
    if (isLoaded && stickToBottomRef.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [isLoaded, messages, isTyping])

  return (
    <section
      aria-label={`${title}, ${index + 1}`}
      tabIndex={0}
      style={style}
      onClick={(event) => {
        const target = event.target as HTMLElement
        if (target.closest("[data-pane-close]")) return
        if (!isActive) onActivate(sessionId)
      }}
      onFocusCapture={(event) => {
        if (
          !isActive &&
          !(event.target as HTMLElement).closest("[data-pane-close]")
        ) {
          onActivate(sessionId)
        }
      }}
      className="bg-background relative flex min-h-0 min-w-0 flex-col overflow-hidden"
    >
      <button
        type="button"
        data-pane-close
        aria-label={t("chat.removeSplitPane")}
        title={t("chat.removeSplitPane")}
        onClick={() => onRemove(sessionId)}
        className="text-muted-foreground hover:text-foreground bg-background/85 absolute top-1 right-1 z-30 rounded p-1"
      >
        <IconX className="size-4" />
      </button>
      <div
        ref={scrollRef}
        data-session-scroll={sessionId}
        onScroll={onScroll}
        onScrollCapture={(event) => {
          const element = event.currentTarget
          stickToBottomRef.current =
            element.scrollHeight - element.scrollTop <=
            element.clientHeight + 10
        }}
        className="chat-scroll-fade min-h-0 flex-1 overflow-y-auto px-3 pt-4"
      >
        {!isLoaded ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            {t("chat.loadingMore")}
          </div>
        ) : loadError ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            {t("chat.historyLoadFailed")}
          </div>
        ) : (
          <div className="mx-auto flex w-full max-w-[49.5rem] flex-col gap-6">
            {messages.length === 0 && !isTyping && emptyContent}
            {renderMessages(messages, sessionId, historyStart, isTyping)}
          </div>
        )}
      </div>
    </section>
  )
}

export function ChatPage() {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dragDepthRef = useRef(0)
  const [isAtBottom, setIsAtBottom] = useState(true)
  const [hasScrolled, setHasScrolled] = useState(false)
  const [input, setInput] = useState("")
  const [attachments, setAttachments] = useState<ChatAttachment[]>([])
  const [isDragActive, setIsDragActive] = useState(false)
  const [forkedFrom, setForkedFrom] = useState<string>()
  const [forkIndex, setForkIndex] = useState(0)
  const pendingReturnIndexRef = useRef<number | null>(null)
  const [assistantDetailVisibility, setAssistantDetailVisibility] = useAtom(
    assistantDetailVisibilityAtom,
  )
  const [splitGroups, setSplitGroups] = useAtom(splitConversationsAtom)
  const [splitLayouts, setSplitLayouts] = useAtom(splitLayoutsAtom)
  const sessionTitles = useAtomValue(sessionTitlesAtom)
  const splitSessionStates = useAtomValue(splitSessionStatesAtom)

  const assistantDetailVisibilityOptions: Array<{
    value: AssistantDetailVisibility
    label: string
  }> = [
    { value: "none", label: t("chat.assistantDetailVisibility.none") },
    { value: "thought", label: t("chat.assistantDetailVisibility.thought") },
    {
      value: "tool_calls",
      label: t("chat.assistantDetailVisibility.toolCalls"),
    },
    { value: "all", label: t("chat.assistantDetailVisibility.all") },
  ]

  const {
    messages,
    connectionState,
    isTyping,
    activeSessionId,
    historyStart,
    hasHydratedActiveSession,
    hasMoreHistory,
    contextUsage,
    sendMessage,
    switchSession,
    loadOlderHistory,
    newChat,
  } = usePicoChat()

  const activeSplitGroup = splitGroups.find((group) =>
    group.includes(activeSessionId),
  )
  const activeSplitLayout = activeSplitGroup
    ? (splitLayouts[splitGroupKey(activeSplitGroup)] ?? "columns")
    : "columns"
  const ActiveSplitLayoutIcon =
    SPLIT_LAYOUTS.find((option) => option.value === activeSplitLayout)?.icon ??
    IconLayoutColumns
  const getActiveScroller = useCallback(() => {
    const root = scrollRef.current
    if (!root) return null
    if (root.dataset.sessionScroll === activeSessionId) return root
    return (
      Array.from(
        root.querySelectorAll<HTMLDivElement>("[data-session-scroll]"),
      ).find((element) => element.dataset.sessionScroll === activeSessionId) ??
      null
    )
  }, [activeSessionId])

  const { state: gwState } = useGateway()
  const isGatewayRunning = gwState === "running"

  const {
    defaultModelName,
    hasAvailableModels,
    apiKeyModels,
    oauthModels,
    localModels,
    settingDefault,
    handleSetDefault,
  } = useChatModels({ isConnected: isGatewayRunning })
  const hasDefaultModel = Boolean(defaultModelName)
  const inputDisabledReason = resolveChatInputDisabledReason({
    hasDefaultModel,
    connectionState,
    gatewayState: gwState,
  })
  const canInput = inputDisabledReason === null

  useEffect(() => {
    let cancelled = false
    setForkedFrom(undefined)
    setForkIndex(0)
    void getSessionHistory(activeSessionId)
      .then((detail) => {
        if (!cancelled) {
          setForkedFrom(detail.forked_from)
          setForkIndex(detail.fork_index ?? 0)
        }
      })
      .catch(() => {
        if (!cancelled) setForkedFrom(undefined)
      })
    return () => {
      cancelled = true
    }
  }, [activeSessionId])

  useEffect(() => {
    const index = pendingReturnIndexRef.current
    if (index === null) return
    if (index < historyStart && hasMoreHistory) {
      void loadOlderHistory(activeSessionId)
      return
    }
    pendingReturnIndexRef.current = null
    requestAnimationFrame(() => {
      getActiveScroller()
        ?.querySelector(`[data-chat-index="${index}"]`)
        ?.scrollIntoView({ block: "center" })
    })
  }, [
    activeSessionId,
    hasMoreHistory,
    historyStart,
    loadOlderHistory,
    messages,
    getActiveScroller,
  ])

  const handleFork = async (sessionId: string, messageIndex: number) => {
    try {
      const fork = await forkSession(sessionId, messageIndex)
      await switchSession(fork.id)
    } catch (error) {
      console.error("Failed to fork conversation:", error)
      toast.error(t("chat.forkFailed"))
    }
  }

  const renderMessages = (
    renderedMessages: ChatMessage[],
    sessionId: string,
    start: number,
    isTyping: boolean,
  ) => {
    const messagesToRender: {
      message: ChatMessage
      offset: number
      toolName?: string
    }[] = []

    renderedMessages.forEach((message, offset) => {
      const firstToolName =
        message.kind === "tool_calls"
          ? message.toolCalls?.[0]?.function?.name?.trim()
          : undefined
      const toolName =
        firstToolName &&
        message.toolCalls?.every(
          (call) => call.function?.name?.trim() === firstToolName,
        )
          ? firstToolName
          : undefined
      const previous = messagesToRender.at(-1)

      if (toolName && previous?.toolName === toolName) {
        previous.message = {
          ...previous.message,
          toolCalls: [
            ...(previous.message.toolCalls ?? []),
            ...(message.toolCalls ?? []),
          ],
        }
      } else {
        messagesToRender.push({ message, offset, toolName })
      }
    })

    const visibleMessages = messagesToRender.filter(({ message }) =>
      shouldShowAssistantMessage(assistantDetailVisibility, message.kind),
    )
    const lastUserIndex = visibleMessages.reduce(
      (lastIndex, { message }, index) =>
        message.role === "user" ? index : lastIndex,
      -1,
    )
    const turnKey =
      visibleMessages[lastUserIndex]?.message.id ?? `session-${sessionId}`
    const renderItems: Array<
      | {
          type: "message"
          item: (typeof visibleMessages)[number]
        }
      | {
          type: "activity"
          items: Array<(typeof visibleMessages)[number]>
          label: string
          startIndex: number
        }
    > = []

    visibleMessages.forEach((item, index) => {
      const kind = item.message.kind
      const isActivity =
        item.message.role === "assistant" &&
        ["thought", "tool_calls", "tool_feedback"].includes(kind ?? "")
      const previous = renderItems.at(-1)

      if (isActivity && previous?.type === "activity") {
        previous.items.push(item)
      } else if (isActivity) {
        renderItems.push({
          type: "activity",
          items: [item],
          label: "",
          startIndex: index,
        })
      } else {
        renderItems.push({ type: "message", item })
      }
    })
    renderItems.forEach((item) => {
      if (item.type !== "activity") return
      const kinds = new Set(item.items.map(({ message }) => message.kind))
      item.label = [
        kinds.has("thought") ? t("chat.reasoningLabel") : "",
        kinds.has("tool_calls") || kinds.has("tool_feedback")
          ? t("chat.toolCallsLabel")
          : "",
      ]
        .filter(Boolean)
        .join(" · ")
    })

    let statusActivityIndex = renderItems.reduce(
      (lastIndex, item, index) =>
        item.type === "activity" && item.startIndex > lastUserIndex
          ? index
          : lastIndex,
      -1,
    )
    if (statusActivityIndex < 0 && (lastUserIndex >= 0 || isTyping)) {
      renderItems.push({
        type: "activity",
        items: [],
        label: "",
        startIndex: visibleMessages.length,
      })
      statusActivityIndex = renderItems.length - 1
    }

    const renderMessage = (
      { message: msg, offset: messageOffset }: (typeof visibleMessages)[number],
      activityChild = false,
    ) => {
      const storedIndex = msg.id.match(/^hist-(\d+)$/)
      const messageIndex = storedIndex
        ? Number(storedIndex[1])
        : start + messageOffset
      const canFork =
        msg.role === "assistant" && (!msg.kind || msg.kind === "normal")

      return (
        <div
          key={msg.id}
          data-chat-index={messageIndex}
          className={`group flex w-full flex-col ${activityChild ? "gap-0" : "gap-1"}`}
        >
          {msg.role === "assistant" ? (
            <>
              <AssistantMessage
                content={msg.content}
                attachments={msg.attachments}
                kind={msg.kind}
                onFork={
                  canFork
                    ? () => void handleFork(sessionId, messageIndex)
                    : undefined
                }
                toolCalls={msg.toolCalls}
                timestamp={msg.timestamp}
              />
            </>
          ) : (
            <UserMessage
              content={msg.content}
              attachments={msg.attachments}
              timestamp={msg.timestamp}
            />
          )}
        </div>
      )
    }

    return renderItems.map((item, index) => {
      if (item.type === "message") return renderMessage(item.item)

      const isStatusGroup = index === statusActivityIndex
      const firstMessageId = item.items[0]?.message.id ?? turnKey
      return (
        <AssistantActivityGroup
          key={
            isStatusGroup ? `activity-${turnKey}` : `activity-${firstMessageId}`
          }
          label={item.label}
          status={
            isStatusGroup ? (
              <TypingIndicator
                key={sessionId}
                isTyping={isTyping}
                fallbackLabel={item.label}
              />
            ) : undefined
          }
        >
          {item.items.map((activityItem) => renderMessage(activityItem, true))}
        </AssistantActivityGroup>
      )
    })
  }

  const syncScrollState = (element: HTMLDivElement) => {
    const { clientHeight, scrollHeight, scrollTop } = element
    setHasScrolled(scrollTop > 0)
    setIsAtBottom(scrollHeight - scrollTop <= clientHeight + 10)
  }

  const handleScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const element = e.currentTarget
    if (element.dataset.sessionScroll !== activeSessionId) return
    syncScrollState(element)
    if (element.scrollTop > 16 || !hasMoreHistory) {
      return
    }

    const previousHeight = element.scrollHeight
    void loadOlderHistory(activeSessionId).then((loaded) => {
      if (!loaded) return
      requestAnimationFrame(() => {
        if (getActiveScroller() !== element) return
        element.scrollTop += element.scrollHeight - previousHeight
        syncScrollState(element)
      })
    })
  }

  useEffect(() => {
    const activeScroller = getActiveScroller()
    if (activeScroller) {
      if (isAtBottom) {
        activeScroller.scrollTop = activeScroller.scrollHeight
      }
      syncScrollState(activeScroller)
    }
  }, [messages, isTyping, isAtBottom, getActiveScroller])

  const handleSend = () => {
    if ((!input.trim() && attachments.length === 0) || !canInput) return
    if (
      sendMessage({
        content: input,
        attachments,
      })
    ) {
      setInput("")
      setAttachments([])
    }
  }

  const handleAddImages = () => {
    if (!canInput) return
    fileInputRef.current?.click()
  }

  const handleRemoveAttachment = (index: number) => {
    setAttachments((prev) => prev.filter((_, itemIndex) => itemIndex !== index))
  }

  const appendImageFiles = async (files: readonly File[]) => {
    if (!canInput || files.length === 0) {
      return
    }

    const nextAttachments = await buildChatImageAttachments(files, t)
    if (nextAttachments.length === 0) {
      return
    }

    setAttachments((prev) => [...prev, ...nextAttachments])
  }

  const handleImageSelection = async (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    event.target.value = ""

    if (files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const resetDragState = () => {
    dragDepthRef.current = 0
    setIsDragActive(false)
  }

  const handleComposerPaste = async (
    event: ClipboardEvent<HTMLTextAreaElement>,
  ) => {
    const files = getTransferredFiles(event.clipboardData)
    if (files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const handleComposerDragEnter = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    if (!canInput) {
      return
    }
    dragDepthRef.current += 1
    setIsDragActive(true)
  }

  const handleComposerDragLeave = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    if (!canInput) {
      resetDragState()
      return
    }
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1)
    if (dragDepthRef.current === 0) {
      setIsDragActive(false)
    }
  }

  const handleComposerDragOver = (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    event.dataTransfer.dropEffect = canInput ? "copy" : "none"
  }

  const handleComposerDrop = async (event: DragEvent<HTMLDivElement>) => {
    if (!hasFileTransfer(event.dataTransfer)) {
      return
    }

    event.preventDefault()
    const files = getTransferredFiles(event.dataTransfer)
    resetDragState()

    if (!canInput || files.length === 0) {
      return
    }

    await appendImageFiles(files)
  }

  const canSubmit =
    canInput && (Boolean(input.trim()) || attachments.length > 0)

  const handleRemoveSplitPane = (sessionId: string) => {
    const remaining = activeSplitGroup?.filter((id) => id !== sessionId)
    const removePane = () =>
      setSplitGroups((groups) => removeSplitConversation(groups, sessionId))

    if (sessionId === activeSessionId && remaining?.[0]) {
      setIsAtBottom(true)
      void switchSession(remaining[0]).finally(removePane)
      return
    }
    removePane()
  }

  const handleAddSplitPane = async (
    layout: SplitLayout = activeSplitLayout,
  ) => {
    if (activeSplitGroup && activeSplitGroup.length >= 4) return

    const newSessionId = await newChat(true)
    if (!newSessionId) return

    const nextGroup = [...(activeSplitGroup ?? [activeSessionId]), newSessionId]
    setSplitGroups((groups) =>
      mergeSplitConversations(groups, newSessionId, activeSessionId),
    )
    setSplitLayouts((layouts) => ({
      ...layouts,
      [splitGroupKey(nextGroup)]: layout,
    }))
  }

  const handleSplitLayoutChange = (value: string) => {
    const layout = value as SplitLayout
    if (!activeSplitGroup) {
      void handleAddSplitPane(layout)
      return
    }
    setSplitLayouts((layouts) => ({
      ...layouts,
      [splitGroupKey(activeSplitGroup)]: layout,
    }))
  }

  const activeEmptyState = (
    <ChatEmptyState
      hasAvailableModels={hasAvailableModels}
      defaultModelName={defaultModelName}
      isConnected={isGatewayRunning}
    />
  )

  return (
    <div className="chat-font flex h-full flex-col bg-[var(--conversation-background)]">
      <PageHeader
        title={t("navigation.chat")}
        className={`transition-shadow ${
          hasScrolled ? "shadow-xs" : "shadow-none"
        }`}
      >
        <div className="flex items-center gap-0.5">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={t("chat.paneLayout")}
                title={t("chat.paneLayout")}
                className="text-muted-foreground hover:text-foreground h-8 w-8 rounded-full"
              >
                <ActiveSplitLayoutIcon className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>{t("chat.paneLayout")}</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuRadioGroup
                value={activeSplitGroup ? activeSplitLayout : ""}
                onValueChange={handleSplitLayoutChange}
              >
                {SPLIT_LAYOUTS.map(({ value, label, icon: LayoutIcon }) => (
                  <DropdownMenuRadioItem key={value} value={value}>
                    <LayoutIcon className="size-4" />
                    {t(`chat.splitLayouts.${value}`, { defaultValue: label })}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            disabled={activeSplitGroup?.length === 4}
            aria-label={t("chat.addSplitPane")}
            title={t("chat.addSplitPane")}
            onClick={() => void handleAddSplitPane()}
            className="text-muted-foreground hover:text-foreground h-8 w-8 rounded-full"
          >
            <IconPlus className="size-4" />
          </Button>
        </div>
        <div className="border-border/60 hidden items-center gap-2 rounded-lg border px-3 py-1.5 sm:flex">
          <span className="text-muted-foreground text-sm">
            {t("chat.showAssistantDetails")}
          </span>
          <Select
            value={assistantDetailVisibility}
            onValueChange={(value) =>
              setAssistantDetailVisibility(value as AssistantDetailVisibility)
            }
          >
            <SelectTrigger
              size="sm"
              aria-label={t("chat.showAssistantDetails")}
              className="text-muted-foreground hover:text-foreground focus-visible:border-input h-8 min-w-[104px] bg-transparent shadow-none focus-visible:ring-0"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="end">
              {assistantDetailVisibilityOptions.map((option) => (
                <SelectItem key={option.value} value={option.value}>
                  {option.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </PageHeader>

      {forkedFrom && (
        <div className="border-border flex items-center justify-between gap-3 border-b px-4 py-2 text-sm">
          <span className="text-muted-foreground flex items-center gap-2">
            <IconGitFork className="size-4" />
            {t("chat.forkedConversation")}
          </span>
          <Button
            variant="ghost"
            size="sm"
            className="gap-2"
            onClick={() => {
              pendingReturnIndexRef.current = forkIndex
              setIsAtBottom(false)
              void switchSession(forkedFrom)
            }}
          >
            <IconArrowBackUp className="size-4" />
            {t("chat.backToOriginal")}
          </Button>
        </div>
      )}

      {activeSplitGroup ? (
        <div ref={scrollRef} className="min-h-0 flex-1 overflow-hidden">
          <SplitConversationView
            sessions={activeSplitGroup}
            layout={activeSplitLayout}
            activeSessionId={activeSessionId}
            activeHistoryStart={historyStart}
            hasHydratedActiveSession={hasHydratedActiveSession}
            sessionTitles={sessionTitles}
            splitSessionStates={splitSessionStates}
            messages={messages}
            isTyping={isTyping}
            renderMessages={renderMessages}
            onActivate={(sessionId) => {
              setIsAtBottom(true)
              void switchSession(sessionId)
            }}
            onRemove={handleRemoveSplitPane}
            onScroll={handleScroll}
            activeEmptyState={activeEmptyState}
            t={t}
          />
        </div>
      ) : (
        <div
          ref={scrollRef}
          data-session-scroll={activeSessionId}
          onScroll={handleScroll}
          className="chat-scroll-fade min-h-0 flex-1 [scrollbar-gutter:stable] overflow-y-auto px-4 pt-6 md:px-8 lg:px-24 xl:px-48"
        >
          <div className="mx-auto flex w-full max-w-[49.5rem] flex-col gap-8">
            {messages.length === 0 && !isTyping && activeEmptyState}
            {renderMessages(messages, activeSessionId, historyStart, isTyping)}
          </div>
        </div>
      )}

      <input
        ref={fileInputRef}
        type="file"
        accept={CHAT_IMAGE_ACCEPT}
        multiple
        className="hidden"
        onChange={handleImageSelection}
      />

      <ChatComposer
        input={input}
        attachments={attachments}
        onInputChange={setInput}
        onAddImages={handleAddImages}
        onPaste={handleComposerPaste}
        onDragEnter={handleComposerDragEnter}
        onDragLeave={handleComposerDragLeave}
        onDragOver={handleComposerDragOver}
        onDrop={handleComposerDrop}
        onRemoveAttachment={handleRemoveAttachment}
        onSend={handleSend}
        modelSelector={
          hasAvailableModels ? (
            <ModelSelector
              defaultModelName={defaultModelName}
              apiKeyModels={apiKeyModels}
              oauthModels={oauthModels}
              localModels={localModels}
              disabled={settingDefault}
              onValueChange={handleSetDefault}
            />
          ) : null
        }
        onContextDetail={() => {
          if (sendMessage({ content: "/context", attachments: [] })) {
            setInput("")
          }
        }}
        inputDisabledReason={inputDisabledReason}
        canSend={canSubmit}
        isDragActive={isDragActive}
        contextUsage={contextUsage}
      />
    </div>
  )
}
