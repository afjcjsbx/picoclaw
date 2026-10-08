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

export function updateSplitSessionState(
  sessionId: string,
  patch:
    | Partial<SplitSessionState>
    | ((prev: SplitSessionState) => Partial<SplitSessionState>),
) {
  store.set(splitSessionStatesAtom, (states) => {
    const prev = states[sessionId] ?? { messages: [], isTyping: false }
    const nextPatch = typeof patch === "function" ? patch(prev) : patch
    return { ...states, [sessionId]: { ...prev, ...nextPatch } }
  })
}

export function initializeSplitSessionState(
  sessionId: string,
  messages: ChatMessage[],
) {
  store.set(splitSessionStatesAtom, (states) =>
    sessionId in states
      ? states
      : { ...states, [sessionId]: { messages, isTyping: false } },
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
