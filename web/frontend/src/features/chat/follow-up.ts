import type { ChatAttachment } from "@/store/chat"

// A follow-up composed while the agent is running. It is held above the
// composer until the user steers it into the active turn or the turn ends.
export interface PendingFollowUp {
  id: string
  sessionId: string
  content: string
  attachments: ChatAttachment[]
}

export function enqueuePendingFollowUp(
  queue: PendingFollowUp[],
  followUp: PendingFollowUp,
): PendingFollowUp[] {
  return [...queue, followUp]
}

export function removePendingFollowUp(
  queue: PendingFollowUp[],
  id: string,
): PendingFollowUp[] {
  return queue.filter((followUp) => followUp.id !== id)
}

export function nextPendingFollowUp(
  queue: PendingFollowUp[],
  sessionId: string,
): PendingFollowUp | undefined {
  return queue.find((followUp) => followUp.sessionId === sessionId)
}

export function pendingFollowUpsForSession(
  queue: PendingFollowUp[],
  sessionId: string,
): PendingFollowUp[] {
  return queue.filter((followUp) => followUp.sessionId === sessionId)
}
