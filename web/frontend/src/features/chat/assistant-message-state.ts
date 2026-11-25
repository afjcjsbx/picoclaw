import {
  parseToolCallsFromContent,
  parseToolCallsValue,
} from "@/features/chat/tool-calls"
import type {
  AssistantMessageKind,
  ChatApproval,
  ChatMessage,
} from "@/store/chat"

type AssistantToolCalls = ChatMessage["toolCalls"]
type ExistingAssistantMessageState = Pick<ChatMessage, "kind" | "toolCalls">

export interface AssistantMessageCreateState {
  content: string
  kind: AssistantMessageKind
  toolCalls?: AssistantToolCalls
  approval?: ChatApproval
}

export interface AssistantMessageUpdateState {
  content: string
  kind: AssistantMessageKind
  toolCalls?: AssistantToolCalls
}

function normalizeAssistantMessageKind(
  payload: Record<string, unknown>,
): string | undefined {
  if (typeof payload.kind !== "string") {
    return undefined
  }
  const kind = payload.kind.trim().toLowerCase()
  return kind || undefined
}

function parseAssistantMessageKind(
  payload: Record<string, unknown>,
  toolCalls?: AssistantToolCalls,
): AssistantMessageKind {
  const kind = normalizeAssistantMessageKind(payload)
  if (kind === "approval") {
    return "approval"
  }
  if (kind === "thought") {
    return "thought"
  }
  if (kind === "tool_feedback") {
    return "tool_feedback"
  }
  if (kind === "tool_calls" || toolCalls) {
    return "tool_calls"
  }
  return "normal"
}

function parseApproval(value: unknown): ChatApproval | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined
  }

  const raw = value as Record<string, unknown>
  const token = typeof raw.token === "string" ? raw.token.trim() : ""
  if (!token) {
    return undefined
  }

  const command =
    typeof raw.command === "string" && raw.command.trim()
      ? raw.command
      : undefined
  const reason =
    typeof raw.reason === "string" && raw.reason.trim() ? raw.reason : undefined
  const rawExpiresAt = raw.expires_at
  const numericExpiresAt =
    typeof rawExpiresAt === "number"
      ? rawExpiresAt
      : typeof rawExpiresAt === "string" && rawExpiresAt.trim()
        ? Number(rawExpiresAt)
        : Number.NaN
  const expiresAt = Number.isFinite(numericExpiresAt)
    ? numericExpiresAt
    : undefined

  return {
    token,
    ...(command ? { command } : {}),
    ...(reason ? { reason } : {}),
    ...(expiresAt !== undefined ? { expiresAt } : {}),
  }
}

function hasExplicitAssistantKindPayload(
  payload: Record<string, unknown>,
): boolean {
  return (
    normalizeAssistantMessageKind(payload) !== undefined ||
    payload.tool_calls !== undefined
  )
}

function parseAssistantToolCalls(
  payload: Record<string, unknown>,
  content: string,
): AssistantToolCalls {
  return (
    parseToolCallsValue(payload.tool_calls) ??
    parseToolCallsFromContent(content)
  )
}

export function parseAssistantMessageCreateState(
  payload: Record<string, unknown>,
): AssistantMessageCreateState {
  const content = typeof payload.content === "string" ? payload.content : ""
  const toolCalls = parseAssistantToolCalls(payload, content)
  const approval = parseApproval(payload.approval)

  return {
    content,
    kind: parseAssistantMessageKind(payload, toolCalls),
    toolCalls,
    ...(approval ? { approval } : {}),
  }
}

export function parseAssistantMessageUpdateState(
  payload: Record<string, unknown>,
  existing?: ExistingAssistantMessageState,
): AssistantMessageUpdateState {
  const content = typeof payload.content === "string" ? payload.content : ""
  const toolCalls = parseAssistantToolCalls(payload, content)

  if (hasExplicitAssistantKindPayload(payload)) {
    const kind = parseAssistantMessageKind(payload, toolCalls)
    return {
      content,
      kind,
      toolCalls: kind === "tool_calls" ? toolCalls : undefined,
    }
  }

  if (toolCalls) {
    return {
      content,
      kind: "tool_calls",
      toolCalls,
    }
  }

  if (existing?.kind === "thought" || existing?.kind === "tool_calls") {
    return {
      content,
      kind: "normal",
      toolCalls: undefined,
    }
  }

  if (existing?.toolCalls) {
    return {
      content,
      kind: existing.kind ?? "normal",
      toolCalls: undefined,
    }
  }

  return {
    content,
    kind: existing?.kind ?? "normal",
  }
}
