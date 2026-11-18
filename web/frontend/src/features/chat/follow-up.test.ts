import { describe, expect, it } from "vitest"

import {
  type PendingFollowUp,
  enqueuePendingFollowUp,
  nextPendingFollowUp,
  pendingFollowUpsForSession,
  removePendingFollowUp,
} from "./follow-up"

function followUp(id: string, sessionId = "session-1"): PendingFollowUp {
  return { id, sessionId, content: `content-${id}`, attachments: [] }
}

describe("enqueuePendingFollowUp", () => {
  it("keeps follow-ups in submission order", () => {
    const queue = enqueuePendingFollowUp([], followUp("a"))
    const next = enqueuePendingFollowUp(queue, followUp("b"))

    expect(next.map((item) => item.id)).toEqual(["a", "b"])
  })
})

describe("removePendingFollowUp", () => {
  it("removes only the requested follow-up", () => {
    const queue = [followUp("a"), followUp("b")]

    expect(removePendingFollowUp(queue, "a").map((item) => item.id)).toEqual([
      "b",
    ])
  })

  it("leaves the queue unchanged for an unknown id", () => {
    const queue = [followUp("a")]

    expect(removePendingFollowUp(queue, "missing")).toEqual(queue)
  })
})

describe("nextPendingFollowUp", () => {
  it("returns the oldest follow-up for the session", () => {
    const queue = [
      followUp("other", "session-2"),
      followUp("a", "session-1"),
      followUp("b", "session-1"),
    ]

    expect(nextPendingFollowUp(queue, "session-1")?.id).toBe("a")
  })

  it("returns undefined when the session has no follow-ups", () => {
    expect(nextPendingFollowUp([followUp("a")], "session-2")).toBeUndefined()
  })
})

describe("pendingFollowUpsForSession", () => {
  it("filters follow-ups by session", () => {
    const queue = [
      followUp("a", "session-1"),
      followUp("b", "session-2"),
      followUp("c", "session-1"),
    ]

    expect(
      pendingFollowUpsForSession(queue, "session-1").map((item) => item.id),
    ).toEqual(["a", "c"])
  })
})
