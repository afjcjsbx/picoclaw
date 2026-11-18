import { atom, getDefaultStore } from "jotai"
import { atomWithStorage } from "jotai/utils"

import {
  getInitialActiveSessionId,
  writeStoredSessionId,
} from "@/features/chat/state"

export interface ChatAttachment {
  type: "image" | "audio" | "video" | "file"
  url: string
  filename?: string
  contentType?: string
}

export interface ChatToolCallFunction {
  name?: string
  arguments?: string
}

export interface ChatToolCallExtraContent {
  toolFeedbackExplanation?: string
}

export interface ChatToolCall {
  id?: string
  type?: string
  function?: ChatToolCallFunction
  extraContent?: ChatToolCallExtraContent
}

export type AssistantMessageKind =
  | "normal"
  | "thought"
  | "tool_calls"
  | "tool_feedback"

export interface ChatMessage {
  id: string
  role: "user" | "assistant"
  content: string
  timestamp: number | string
  kind?: AssistantMessageKind
  modelName?: string
  attachments?: ChatAttachment[]
  toolCalls?: ChatToolCall[]
  usage?: TokenUsage
}

export interface TokenUsage {
  input_tokens?: number
  output_tokens?: number
  total_tokens: number
  cache_read_tokens?: number
  cache_creation_tokens?: number
  /** Number of LLM calls aggregated into the turn totals. */
  requests?: number
  /** Prompt size of the last LLM call (the context currently in the window). */
  context_tokens?: number
  /** Absolute turn cost in USD when the backend reports it. */
  cost_usd?: number
}

export interface ContextUsage {
  used_tokens: number
  total_tokens: number
  history_tokens?: number
  compress_at_tokens: number
  summarize_at_tokens?: number
  used_percent: number
}

export type ConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "error"

export interface ChatStoreState {
  messages: ChatMessage[]
  connectionState: ConnectionState
  isTyping: boolean
  activeSessionId: string
  hasHydratedActiveSession: boolean
  historyStart: number
  hasMoreHistory: boolean
  contextUsage?: ContextUsage
}

export interface SplitSessionState {
  messages: ChatMessage[]
  isTyping: boolean
  contextUsage?: ContextUsage
  historyStart: number
  hasMoreHistory: boolean
}

export const splitConversationsAtom = atomWithStorage<string[][]>(
  "picoclaw:split-conversations",
  [],
  undefined,
  { getOnInit: true },
)
export type SplitLayout = "columns" | "rows" | "grid" | "bsp" | "main-stack"
export interface SplitLayoutSizes {
  columns: number[]
  rows: number[]
}
export const splitLayoutsAtom = atomWithStorage<Record<string, SplitLayout>>(
  "picoclaw:split-layouts",
  {},
  undefined,
  { getOnInit: true },
)
export const splitLayoutSizesAtom = atomWithStorage<
  Record<string, SplitLayoutSizes>
>("picoclaw:split-layout-sizes", {}, undefined, { getOnInit: true })
export const splitSessionStatesAtom = atom<Record<string, SplitSessionState>>(
  {},
)
export const sessionTitlesAtom = atom<Record<string, string>>({})
export const unreadSessionIdsAtom = atom<Set<string>>(new Set<string>())
export const sessionRunningAtom = atom<Record<string, boolean>>({})

export interface ComposerInsertion {
  text: string
}

// Set by the command palette to drop a slash command into the composer. The
// chat page consumes the request and clears it once the input is updated.
export const composerInsertionAtom = atom<ComposerInsertion | null>(null)

// Bumped whenever the chat composer should take focus, e.g. after starting a
// new conversation.
export const composerFocusSignalAtom = atom(0)

export interface PendingScrollTarget {
  sessionId: string
  messageIndex: number
}

// Set when a conversation search result is opened so the chat view can load
// back to that message and scroll it into view.
export const pendingScrollTargetAtom = atom<PendingScrollTarget | null>(null)

type ChatStorePatch = Partial<ChatStoreState>

const DEFAULT_CHAT_STATE: ChatStoreState = {
  messages: [],
  connectionState: "disconnected",
  isTyping: false,
  activeSessionId: getInitialActiveSessionId(),
  hasHydratedActiveSession: false,
  historyStart: 0,
  hasMoreHistory: false,
}

export const chatAtom = atom<ChatStoreState>(DEFAULT_CHAT_STATE)

const store = getDefaultStore()

export function markSessionUnread(sessionId: string) {
  if (!sessionId || getChatState().activeSessionId === sessionId) {
    return
  }

  store.set(unreadSessionIdsAtom, (current) => {
    if (current.has(sessionId)) return current
    const next = new Set(current)
    next.add(sessionId)
    return next
  })
}

export function markSessionRead(sessionId: string) {
  store.set(unreadSessionIdsAtom, (current) => {
    if (!current.has(sessionId)) return current
    const next = new Set(current)
    next.delete(sessionId)
    return next
  })
}

export function setSessionRunning(sessionId: string, isRunning: boolean) {
  if (!sessionId) return

  store.set(sessionRunningAtom, (current) => {
    if (Boolean(current[sessionId]) === isRunning) return current
    const next = { ...current }
    if (isRunning) {
      next[sessionId] = true
    } else {
      delete next[sessionId]
    }
    return next
  })
}

export function clearSessionRunning(keepSessionId?: string) {
  store.set(sessionRunningAtom, (current) => {
    const keys = Object.keys(current)
    if (keys.length === 0) return current
    if (!keepSessionId || !current[keepSessionId]) return {}
    if (keys.length === 1) return current
    return { [keepSessionId]: true }
  })
}

export function updateSplitSessionState(
  sessionId: string,
  patch:
    | Partial<SplitSessionState>
    | ((prev: SplitSessionState) => Partial<SplitSessionState>),
) {
  store.set(splitSessionStatesAtom, (states) => {
    const prev = states[sessionId] ?? {
      messages: [],
      isTyping: false,
      historyStart: 0,
      hasMoreHistory: false,
    }
    const nextPatch = typeof patch === "function" ? patch(prev) : patch
    return { ...states, [sessionId]: { ...prev, ...nextPatch } }
  })
}

export function initializeSplitSessionState(
  sessionId: string,
  history: { messages: ChatMessage[]; start: number; hasMore: boolean },
) {
  store.set(splitSessionStatesAtom, (states) =>
    sessionId in states
      ? states
      : {
          ...states,
          [sessionId]: {
            messages: history.messages,
            isTyping: false,
            historyStart: history.start,
            hasMoreHistory: history.hasMore,
          },
        },
  )
}

export function getChatState() {
  return store.get(chatAtom)
}

export function updateChatStore(
  patch:
    | ChatStorePatch
    | ((prev: ChatStoreState) => ChatStorePatch | ChatStoreState),
) {
  store.set(chatAtom, (prev) => {
    const nextPatch = typeof patch === "function" ? patch(prev) : patch
    const next = { ...prev, ...nextPatch }

    if (next.activeSessionId !== prev.activeSessionId) {
      writeStoredSessionId(next.activeSessionId)
    }

    return next
  })
}
