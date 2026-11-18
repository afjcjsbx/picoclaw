import { getSessionHistory } from "@/api/sessions"
import { normalizeUnixTimestamp } from "@/features/chat/state"
import {
  parseToolCallsValue,
  toolCallsSignature,
} from "@/features/chat/tool-calls"
import { parseTokenUsage } from "@/features/chat/usage"
import type { ChatAttachment, ChatMessage } from "@/store/chat"

const pendingHistoryLoads = new Map<
  string,
  Promise<{ messages: ChatMessage[]; start: number; hasMore: boolean }>
>()

function toChatAttachments({
  media,
  attachments,
}: {
  media?: string[]
  attachments?: {
    type?: "image" | "audio" | "video" | "file"
    url: string
    filename?: string
    content_type?: string
  }[]
}): ChatAttachment[] | undefined {
  const normalizedAttachments = attachments
    ?.filter((attachment) => attachment.url)
    .map(
      (attachment) =>
        ({
          type: attachment.type ?? "file",
          url: attachment.url,
          filename: attachment.filename,
          contentType: attachment.content_type,
        }) satisfies ChatAttachment,
    )

  const legacyMediaAttachments = (media ?? []).flatMap<ChatAttachment>(
    (url) => {
      if (url.startsWith("data:image/"))
        return [{ type: "image" as const, url }]
      if (url.startsWith("data:application/pdf;")) {
        return [
          {
            type: "file" as const,
            url,
            filename: "attachment.pdf",
            contentType: "application/pdf",
          },
        ]
      }
      return []
    },
  )

  const merged = [...(normalizedAttachments ?? []), ...legacyMediaAttachments]

  return merged.length > 0 ? merged : undefined
}

function stripUploadedFiles(content: string) {
  const opening = "\n\n<uploaded_files>\n"
  const closing = "\n</uploaded_files>"
  const start = content.lastIndexOf(opening)
  if (start < 0 || !content.endsWith(closing)) {
    return { content }
  }

  try {
    const serializedFiles = content.slice(
      start + opening.length,
      -closing.length,
    )
    const jsonStart = serializedFiles.indexOf("[")
    const fileJSON =
      jsonStart >= 0 ? serializedFiles.slice(jsonStart) : serializedFiles
    const files = JSON.parse(fileJSON) as {
      filename?: unknown
      content?: unknown
      path?: unknown
    }[]
    if (
      !Array.isArray(files) ||
      files.some(
        (file) =>
          !file ||
          typeof file.filename !== "string" ||
          (typeof file.content !== "string" && typeof file.path !== "string"),
      )
    ) {
      return { content }
    }

    return {
      content: content.slice(0, start),
      attachments: files.map((file) => ({
        type: "file" as const,
        url: "",
        filename: file.filename as string,
      })),
    }
  } catch {
    return { content }
  }
}

export function loadSessionMessages(
  sessionId: string,
  before?: number,
): Promise<{ messages: ChatMessage[]; start: number; hasMore: boolean }> {
  const key = JSON.stringify([sessionId, before])
  const pending = pendingHistoryLoads.get(key)
  if (pending) return pending

  const request = getSessionHistory(sessionId, before).then((detail) => ({
    messages: detail.messages.map((message, index) => {
      const uploadedFiles =
        message.role === "user"
          ? stripUploadedFiles(message.content)
          : { content: message.content }
      const attachments = [
        ...(toChatAttachments({
          media: message.media,
          attachments: message.attachments,
        }) ?? []),
        ...(uploadedFiles.attachments ?? []),
      ]
      const usage = parseTokenUsage({ usage: message.usage })

      return {
        id: `hist-${detail.start + index}`,
        role: message.role,
        content: uploadedFiles.content,
        kind:
          message.role === "assistant" ? (message.kind ?? "normal") : undefined,
        modelName: message.model_name,
        toolCalls:
          message.role === "assistant"
            ? parseToolCallsValue(message.tool_calls)
            : undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
        ...(usage ? { usage } : {}),
        timestamp: message.created_at ?? detail.updated,
      }
    }),
    start: detail.start,
    hasMore: detail.start > 0,
  }))
  pendingHistoryLoads.set(key, request)
  const clearPending = () => {
    if (pendingHistoryLoads.get(key) === request) {
      pendingHistoryLoads.delete(key)
    }
  }
  void request.then(clearPending, clearPending)
  return request
}

function normalizeMessageTimestamp(timestamp: number | string): string {
  if (typeof timestamp === "number") {
    return String(normalizeUnixTimestamp(timestamp))
  }

  const trimmed = timestamp.trim()
  if (/^-?\d+(\.\d+)?$/.test(trimmed)) {
    return String(normalizeUnixTimestamp(Number(trimmed)))
  }

  const parsed = Date.parse(trimmed)
  return Number.isNaN(parsed) ? trimmed : String(parsed)
}

function messageContentSignature(message: ChatMessage): string {
  const attachmentSignature = (message.attachments ?? [])
    .map(
      (attachment) =>
        `${attachment.type}\u0001${attachment.url}\u0001${attachment.filename ?? ""}`,
    )
    .join("\u0002")

  return `${message.role}\u0000${message.content}\u0000${message.kind ?? ""}\u0000${attachmentSignature}\u0000${toolCallsSignature(
    message.toolCalls,
  )}`
}

function comparableTimestamp(timestamp: number | string): number {
  const normalized = normalizeMessageTimestamp(timestamp)
  const numeric = Number(normalized)
  return Number.isFinite(numeric) ? numeric : 0
}

// Client-side timestamps (Date.now at send/receive) and server timestamps
// (created_at) rarely match to the millisecond, so persisted messages are
// matched by content plus a tolerance window instead of exact timestamp.
const MERGE_TIMESTAMP_TOLERANCE_MS = 2 * 60 * 1000

export function mergeHistoryMessages(
  historyMessages: ChatMessage[],
  currentMessages: ChatMessage[],
): ChatMessage[] {
  const currentIds = new Set(currentMessages.map((message) => message.id))
  const liveMessages = currentMessages.filter(
    (message) => !message.id.startsWith("hist-"),
  )
  const liveIndexesByContent = new Map<string, number[]>()
  liveMessages.forEach((message, index) => {
    const key = messageContentSignature(message)
    const indexes = liveIndexesByContent.get(key)
    if (indexes) {
      indexes.push(index)
    } else {
      liveIndexesByContent.set(key, [index])
    }
  })
  const matchedLiveIndexes = new Set<number>()

  const merged = historyMessages.filter((message) => {
    if (currentIds.has(message.id)) {
      return false
    }

    const candidates = liveIndexesByContent.get(
      messageContentSignature(message),
    )
    if (!candidates) {
      return true
    }

    const historyTimestamp = comparableTimestamp(message.timestamp)
    const tolerance =
      message.role === "user"
        ? Number.POSITIVE_INFINITY
        : MERGE_TIMESTAMP_TOLERANCE_MS

    let bestIndex = -1
    let bestDelta = Number.POSITIVE_INFINITY
    for (const index of candidates) {
      if (matchedLiveIndexes.has(index)) {
        continue
      }
      const delta = Math.abs(
        comparableTimestamp(liveMessages[index].timestamp) - historyTimestamp,
      )
      if (delta < bestDelta) {
        bestDelta = delta
        bestIndex = index
      }
    }

    if (bestIndex >= 0 && bestDelta <= tolerance) {
      matchedLiveIndexes.add(bestIndex)
      return false
    }

    return true
  })

  return [...merged, ...currentMessages].sort(
    (left, right) =>
      comparableTimestamp(left.timestamp) -
      comparableTimestamp(right.timestamp),
  )
}
