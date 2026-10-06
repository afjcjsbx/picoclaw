import { IconArrowBackUp, IconGitFork, IconX } from "@tabler/icons-react"
import type { TFunction } from "i18next"
import { useAtom, useAtomValue } from "jotai"
import {
  type ChangeEvent,
  type ClipboardEvent,
  type DragEvent,
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
import { removeSplitConversation } from "@/features/chat/split-groups"
import { useChatModels } from "@/hooks/use-chat-models"
import { useGateway } from "@/hooks/use-gateway"
import { usePicoChat } from "@/hooks/use-pico-chat"
import type { AssistantDetailVisibility } from "@/store/chat"
import type {
  ChatAttachment,
  ChatMessage,
  ConnectionState,
  SplitSessionState,
} from "@/store/chat"
import {
  assistantDetailVisibilityAtom,
  sessionTitlesAtom,
  shouldShowAssistantMessage,
  splitConversationsAtom,
  splitSessionStatesAtom,
} from "@/store/chat"
import type { GatewayState } from "@/store/gateway"

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
  ) => React.ReactNode
  onActivate: (sessionId: string) => void
  onRemove: (sessionId: string) => void
  onScroll: (event: React.UIEvent<HTMLDivElement>) => void
  activeEmptyState: React.ReactNode
  t: TFunction
}

function SplitConversationView({
  sessions,
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
      className={`grid h-full min-h-0 gap-2 p-2 ${sessions.length === 3 ? "grid-cols-2 grid-rows-2 [&>*:nth-child(3)]:col-span-2" : sessions.length === 4 ? "grid-cols-2 grid-rows-2" : "grid-cols-1 sm:grid-cols-2"}`}
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
    </div>
  )
}

interface SplitConversationPaneProps {
  sessionId: string
  index: number
  title: string
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
      className={`bg-background flex min-h-0 min-w-0 flex-col overflow-hidden rounded-xl border ${isActive ? "border-primary/50" : "border-border/70"}`}
    >
      <header className="border-border/60 flex min-w-0 items-center gap-2 border-b px-3 py-2">
        <button
          type="button"
          aria-pressed={isActive}
          onClick={() => onActivate(sessionId)}
          className={`min-w-0 flex-1 truncate text-left text-sm ${isActive ? "font-medium" : "text-muted-foreground"}`}
          title={title}
        >
          {title}
        </button>
        <button
          type="button"
          aria-label={t("chat.removeSplitPane")}
          title={t("chat.removeSplitPane")}
          onClick={() => onRemove(sessionId)}
          className="text-muted-foreground hover:text-foreground rounded p-1"
        >
          <IconX className="size-4" />
        </button>
      </header>
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
        className="min-h-0 flex-1 overflow-y-auto px-3 py-4"
      >
        {!isLoaded ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            {t("chat.loadingMore")}
          </div>
        ) : loadError ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            {t("chat.historyLoadFailed")}
          </div>
        ) : messages.length === 0 ? (
          <>
            {!isTyping && emptyContent}
            {isTyping && <TypingIndicator />}
          </>
        ) : (
          <div className="mx-auto flex w-full max-w-250 flex-col gap-6 pb-4">
            {renderMessages(messages, sessionId, historyStart)}
            {isTyping && <TypingIndicator />}
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
  } = usePicoChat()

  const activeSplitGroup = splitGroups.find((group) =>
    group.includes(activeSessionId),
  )
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
  ) =>
    renderedMessages.map((msg, messageOffset) => {
      if (!shouldShowAssistantMessage(assistantDetailVisibility, msg.kind)) {
        return null
      }

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
          className="group flex w-full flex-col gap-1"
        >
          {msg.role === "assistant" ? (
            <>
              <AssistantMessage
                content={msg.content}
                attachments={msg.attachments}
                kind={msg.kind}
                modelName={msg.modelName}
                toolCalls={msg.toolCalls}
                timestamp={msg.timestamp}
              />
              {canFork && (
                <div className="flex justify-end">
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={t("chat.forkAtMessage")}
                    title={t("chat.forkAtMessage")}
                    className="size-8 opacity-50 hover:opacity-100 sm:opacity-0 sm:group-hover:opacity-100"
                    onClick={() => void handleFork(sessionId, messageIndex)}
                  >
                    <IconGitFork className="size-4" />
                  </Button>
                </div>
              )}
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
    })

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

  const activeEmptyState = (
    <ChatEmptyState
      hasAvailableModels={hasAvailableModels}
      defaultModelName={defaultModelName}
      isConnected={isGatewayRunning}
    />
  )

  return (
    <div className="flex h-full flex-col bg-[var(--conversation-background)]">
      <PageHeader
        title={t("navigation.chat")}
        className={`transition-shadow ${
          hasScrolled ? "shadow-xs" : "shadow-none"
        }`}
        titleExtra={
          hasAvailableModels && (
            <ModelSelector
              defaultModelName={defaultModelName}
              apiKeyModels={apiKeyModels}
              oauthModels={oauthModels}
              localModels={localModels}
              disabled={settingDefault}
              onValueChange={handleSetDefault}
            />
          )
        }
      >
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
          className="min-h-0 flex-1 [scrollbar-gutter:stable] overflow-y-auto px-4 py-6 md:px-8 lg:px-24 xl:px-48"
        >
          <div className="mx-auto flex w-full max-w-250 flex-col gap-8 pb-8">
            {messages.length === 0 && !isTyping && activeEmptyState}
            {renderMessages(messages, activeSessionId, historyStart)}
            {isTyping && <TypingIndicator />}
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
