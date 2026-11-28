import { toast } from "sonner"

import {
  parseAssistantMessageCreateState,
  parseAssistantMessageUpdateState,
} from "@/features/chat/assistant-message-state"
import { normalizeUnixTimestamp } from "@/features/chat/state"
import { parseTokenUsage } from "@/features/chat/usage"
import {
  type ChatAttachment,
  type ChatMessage,
  type ContextUsage,
  type SplitSessionState,
  getChatState,
  markSessionUnread,
  setSessionRunning,
  updateChatStore,
  updateSplitSessionState,
} from "@/store/chat"

export interface PicoMessage {
  type: string
  id?: string
  session_id?: string
  timestamp?: number | string
  payload?: Record<string, unknown>
}

let compactionMarkerSeq = 0

function parseAttachments(
  payload: Record<string, unknown>,
): ChatAttachment[] | undefined {
  const raw = payload.attachments
  if (!Array.isArray(raw)) {
    return undefined
  }

  const attachments: ChatAttachment[] = []
  for (const item of raw) {
    if (!item || typeof item !== "object") {
      continue
    }

    const attachment = item as Record<string, unknown>
    const url = typeof attachment.url === "string" ? attachment.url : ""
    if (!url) {
      continue
    }

    const type =
      attachment.type === "audio" ||
      attachment.type === "video" ||
      attachment.type === "file" ||
      attachment.type === "image"
        ? attachment.type
        : "file"

    const filename =
      typeof attachment.filename === "string" ? attachment.filename : undefined
    const contentType =
      typeof attachment.content_type === "string"
        ? attachment.content_type
        : undefined

    attachments.push({
      type,
      url,
      ...(filename ? { filename } : {}),
      ...(contentType ? { contentType } : {}),
    })
  }

  return attachments.length > 0 ? attachments : undefined
}

function parseContextUsage(
  payload: Record<string, unknown>,
): ContextUsage | undefined {
  const raw = payload.context_usage
  if (!raw || typeof raw !== "object") return undefined
  const obj = raw as Record<string, unknown>
  const used = Number(obj.used_tokens)
  const total = Number(obj.total_tokens)
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0)
    return undefined
  return {
    used_tokens: used,
    total_tokens: total,
    history_tokens:
      obj.history_tokens != null ? Number(obj.history_tokens) : undefined,
    compress_at_tokens: Number(obj.compress_at_tokens) || 0,
    summarize_at_tokens:
      obj.summarize_at_tokens != null
        ? Number(obj.summarize_at_tokens)
        : undefined,
    used_percent: Number(obj.used_percent) || 0,
  }
}

function parseModelName(payload: Record<string, unknown>): string | undefined {
  if (typeof payload.model_name !== "string") {
    return undefined
  }
  const modelName = payload.model_name.trim()
  return modelName || undefined
}

interface ToolResultPayload {
  toolCallId: string
  output: string
}

function parseToolResultPayload(
  payload: Record<string, unknown>,
): ToolResultPayload | undefined {
  if (payload.kind !== "tool_result") {
    return undefined
  }
  const toolCallId =
    typeof payload.tool_call_id === "string" ? payload.tool_call_id.trim() : ""
  if (!toolCallId) {
    return undefined
  }
  return {
    toolCallId,
    output: typeof payload.content === "string" ? payload.content : "",
  }
}

function mergeToolResult(
  messages: ChatMessage[],
  { toolCallId, output }: ToolResultPayload,
): ChatMessage[] {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (message.kind !== "tool_calls" || !message.toolCalls) continue
    const callIndex = message.toolCalls.findIndex(
      (toolCall) => toolCall.id === toolCallId,
    )
    if (callIndex < 0) continue

    const toolCalls = [...message.toolCalls]
    toolCalls[callIndex] = { ...toolCalls[callIndex], output }
    const next = [...messages]
    next[index] = { ...message, toolCalls }
    return next
  }
  return messages
}

export function handlePicoActivityMessage(message: PicoMessage) {
  if (message.type !== "session.activity") {
    return
  }

  const sessionId = message.session_id
  if (!sessionId) {
    return
  }

  const event =
    typeof message.payload?.event === "string" ? message.payload.event : ""
  const isFinal = message.payload?.final === true

  switch (event) {
    case "typing.start":
      setSessionRunning(sessionId, true)
      break
    case "typing.stop":
    case "error":
      setSessionRunning(sessionId, false)
      break
    case "message.create":
    case "media.create":
    case "message.update":
      setSessionRunning(sessionId, !isFinal)
      if (isFinal) {
        markSessionUnread(sessionId)
      }
      break
    default:
      break
  }
}

export function handlePicoMessage(
  message: PicoMessage,
  expectedSessionId: string,
) {
  if (message.session_id && message.session_id !== expectedSessionId) {
    return
  }

  const payload = message.payload || {}
  const isFinal = payload.final === true
  const updateSession = (
    patch:
      | Partial<SplitSessionState>
      | ((prev: SplitSessionState) => Partial<SplitSessionState>),
  ) => {
    if (getChatState().activeSessionId === expectedSessionId) {
      updateChatStore((prev) =>
        typeof patch === "function" ? patch(prev) : patch,
      )
    } else {
      updateSplitSessionState(expectedSessionId, patch)
    }
  }

  switch (message.type) {
    case "message.create":
    case "media.create": {
      if (isFinal) {
        markSessionUnread(expectedSessionId)
        setSessionRunning(expectedSessionId, false)
      }
      const messageId = (payload.message_id as string) || `pico-${Date.now()}`
      const toolResult = parseToolResultPayload(payload)
      if (toolResult) {
        updateSession((prev) => ({
          messages: mergeToolResult(prev.messages, toolResult),
        }))
        break
      }
      const { content, kind, toolCalls, approval } =
        parseAssistantMessageCreateState(payload)
      const attachments = parseAttachments(payload)
      const contextUsage = parseContextUsage(payload)
      const usage = parseTokenUsage(payload)
      const isPlaceholder = payload.placeholder === true
      const modelName = parseModelName(payload)
      const timestamp =
        message.timestamp !== undefined &&
        Number.isFinite(Number(message.timestamp))
          ? normalizeUnixTimestamp(Number(message.timestamp))
          : Date.now()

      updateSession((prev) => ({
        messages: [
          ...prev.messages,
          {
            id: messageId,
            role: "assistant",
            content,
            kind,
            ...(modelName ? { modelName } : {}),
            ...(toolCalls ? { toolCalls } : {}),
            ...(approval ? { approval } : {}),
            ...(usage ? { usage } : {}),
            attachments,
            timestamp,
          },
        ],
        isTyping:
          isFinal ||
          (!isPlaceholder &&
            (kind === "normal" || message.type === "media.create"))
            ? false
            : prev.isTyping,
        ...(isFinal ? { isCompacting: false } : {}),
        ...(contextUsage ? { contextUsage } : {}),
      }))
      break
    }

    case "message.update": {
      if (isFinal) {
        markSessionUnread(expectedSessionId)
        setSessionRunning(expectedSessionId, false)
      }
      const messageId = payload.message_id as string
      const attachments = parseAttachments(payload)
      const contextUsage = parseContextUsage(payload)
      const usage = parseTokenUsage(payload)
      const modelName = parseModelName(payload)
      const timestamp =
        message.timestamp !== undefined &&
        Number.isFinite(Number(message.timestamp))
          ? normalizeUnixTimestamp(Number(message.timestamp))
          : Date.now()
      if (!messageId) {
        break
      }

      updateSession((prev) => ({
        messages: (() => {
          let found = false
          const messages = prev.messages.map((msg) => {
            if (msg.id !== messageId) {
              return msg
            }
            found = true
            const { content, kind, toolCalls } =
              parseAssistantMessageUpdateState(payload, msg)
            return {
              ...msg,
              id: messageId,
              content,
              kind,
              toolCalls,
              ...(modelName ? { modelName } : {}),
              ...(attachments ? { attachments } : {}),
              ...(usage ? { usage } : {}),
            }
          })
          if (found) {
            return messages
          }

          const { content, kind, toolCalls } =
            parseAssistantMessageUpdateState(payload)

          return [
            ...messages,
            {
              id: messageId,
              role: "assistant" as const,
              content,
              kind,
              toolCalls,
              ...(modelName ? { modelName } : {}),
              ...(attachments ? { attachments } : {}),
              ...(usage ? { usage } : {}),
              timestamp,
            },
          ]
        })(),
        ...(isFinal ? { isTyping: false, isCompacting: false } : {}),
        ...(contextUsage ? { contextUsage } : {}),
      }))
      break
    }

    case "message.delete": {
      const messageId = payload.message_id as string
      if (!messageId) {
        break
      }

      updateSession((prev) => ({
        messages: prev.messages.filter((msg) => msg.id !== messageId),
      }))
      break
    }

    case "typing.start":
      setSessionRunning(expectedSessionId, true)
      updateSession({ isTyping: true })
      break

    case "typing.stop":
      setSessionRunning(expectedSessionId, false)
      updateSession({ isTyping: false, isCompacting: false })
      break

    case "session.compaction": {
      const phase =
        typeof payload.phase === "string"
          ? payload.phase.trim().toLowerCase()
          : ""
      if (phase === "running") {
        updateSession({ isCompacting: true })
        break
      }
      if (phase === "completed") {
        const markerId =
          typeof message.id === "string" && message.id
            ? message.id
            : `compaction-${Date.now()}-${++compactionMarkerSeq}`
        updateSession((prev) => {
          if (prev.compactionMarkers.some((marker) => marker.id === markerId)) {
            return { isCompacting: false }
          }
          return {
            isCompacting: false,
            compactionMarkers: [
              ...prev.compactionMarkers,
              {
                id: markerId,
                afterMessageId: prev.messages.at(-1)?.id,
              },
            ],
          }
        })
        break
      }
      if (phase === "idle") {
        updateSession({ isCompacting: false })
        break
      }
      // Older gateways sent a boolean "active" flag without a phase.
      updateSession({ isCompacting: payload.active === true })
      break
    }

    case "error": {
      const requestId =
        typeof payload.request_id === "string" ? payload.request_id : ""
      const errorMessage =
        typeof payload.message === "string" ? payload.message : ""

      setSessionRunning(expectedSessionId, false)
      console.error("Pico error:", payload)
      if (errorMessage) {
        toast.error(errorMessage)
      }
      updateSession((prev) => ({
        messages: requestId
          ? prev.messages.filter((msg) => msg.id !== requestId)
          : prev.messages,
        isTyping: false,
        isCompacting: false,
      }))
      break
    }

    case "pong":
      break

    default:
      console.log("Unknown pico message type:", message.type)
  }
}
