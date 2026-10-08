import { getSessionHistory } from "@/api/sessions"
import {
  CHAT_TEXT_ATTACHMENT_INSTRUCTION,
  CHAT_UPLOADED_FILE_PATH_INSTRUCTION,
} from "@/features/chat/image-input"
import { normalizeUnixTimestamp } from "@/features/chat/state"
import {
  parseToolCallsValue,
  toolCallsSignature,
} from "@/features/chat/tool-calls"
import type { ChatAttachment, ChatMessage } from "@/store/chat"

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
    const fileJSON = [
      CHAT_TEXT_ATTACHMENT_INSTRUCTION,
      CHAT_UPLOADED_FILE_PATH_INSTRUCTION,
    ].reduce(
      (value, instruction) =>
        value.startsWith(instruction) ? value.slice(instruction.length) : value,
      serializedFiles,
    )
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

export async function loadSessionMessages(
  sessionId: string,
  before?: number,
): Promise<{ messages: ChatMessage[]; start: number; hasMore: boolean }> {
  const detail = await getSessionHistory(sessionId, before)
  return {
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
        timestamp: message.created_at ?? detail.updated,
      }
    }),
    start: detail.start,
    hasMore: detail.start > 0,
  }
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

function messageSignature(message: ChatMessage): string {
  const attachmentSignature = (message.attachments ?? [])
    .map(
      (attachment) =>
        `${attachment.type}\u0001${attachment.url}\u0001${attachment.filename ?? ""}`,
    )
    .join("\u0002")

  return `${message.role}\u0000${message.content}\u0000${normalizeMessageTimestamp(
    message.timestamp,
  )}\u0000${message.kind ?? ""}\u0000${message.modelName ?? ""}\u0000${attachmentSignature}\u0000${toolCallsSignature(
    message.toolCalls,
  )}`
}

function comparableTimestamp(timestamp: number | string): number {
  const normalized = normalizeMessageTimestamp(timestamp)
  const numeric = Number(normalized)
  return Number.isFinite(numeric) ? numeric : 0
}

export function mergeHistoryMessages(
  historyMessages: ChatMessage[],
  currentMessages: ChatMessage[],
): ChatMessage[] {
  const currentIds = new Set(currentMessages.map((message) => message.id))
  const currentSignatures = new Set(
    currentMessages.map((message) => messageSignature(message)),
  )

  const merged = [
    ...historyMessages.filter(
      (message) =>
        !currentIds.has(message.id) &&
        !currentSignatures.has(messageSignature(message)),
    ),
    ...currentMessages,
  ]

  return merged.sort(
    (left, right) =>
      comparableTimestamp(left.timestamp) -
      comparableTimestamp(right.timestamp),
  )
}
