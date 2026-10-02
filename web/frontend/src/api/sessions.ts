import { launcherFetch } from "@/api/http"

export interface SessionSummary {
  id: string
  title: string
  preview: string
  message_count: number
  created: string
  updated: string
  forked_from?: string
  fork_index?: number
}

export interface SessionDetail {
  id: string
  start: number
  total: number
  messages: {
    role: "user" | "assistant"
    content: string
    created_at?: string
    kind?: "normal" | "thought" | "tool_calls"
    model_name?: string
    media?: string[]
    attachments?: {
      type?: "image" | "audio" | "video" | "file"
      url: string
      filename?: string
      content_type?: string
    }[]
    tool_calls?: {
      id?: string
      type?: string
      function?: {
        name?: string
        arguments?: string
      }
      extra_content?: {
        tool_feedback_explanation?: string
      }
    }[]
  }[]
  summary: string
  created: string
  updated: string
  forked_from?: string
  fork_index?: number
}

export async function forkSession(
  id: string,
  messageIndex: number,
): Promise<{ id: string; forked_from: string; fork_index: number }> {
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}/fork`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ message_index: messageIndex }),
    },
  )
  if (!res.ok) throw new Error(`Failed to fork session: ${res.status}`)
  return res.json()
}

export async function getSessions(
  offset: number = 0,
  limit: number = 20,
): Promise<SessionSummary[]> {
  const params = new URLSearchParams({
    offset: offset.toString(),
    limit: limit.toString(),
  })

  const res = await launcherFetch(`/api/sessions?${params.toString()}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch sessions: ${res.status}`)
  }
  return res.json()
}

export async function getSessionHistory(
  id: string,
  before?: number,
): Promise<SessionDetail> {
  const params = new URLSearchParams({ limit: "50" })
  if (before !== undefined) {
    params.set("before", String(before))
  }
  const res = await launcherFetch(
    `/api/sessions/${encodeURIComponent(id)}?${params.toString()}`,
  )
  if (!res.ok) {
    throw new Error(`Failed to fetch session ${id}: ${res.status}`)
  }
  return res.json()
}

export async function deleteSession(id: string): Promise<void> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
  if (!res.ok) {
    throw new Error(`Failed to delete session ${id}: ${res.status}`)
  }
}
