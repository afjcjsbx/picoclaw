import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import type { PendingFollowUp } from "@/features/chat/follow-up"

import { PendingFollowUpOverlay } from "./pending-follow-up-overlay"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

const FOLLOW_UP: PendingFollowUp = {
  id: "follow-up-1",
  sessionId: "session-1",
  content: "Please adjust the colors",
  attachments: [],
}

describe("PendingFollowUpOverlay", () => {
  it("renders the queued message with its attachments", () => {
    render(
      <PendingFollowUpOverlay
        followUps={[
          {
            ...FOLLOW_UP,
            attachments: [
              {
                type: "file",
                url: "blob:report",
                filename: "report.pdf",
              },
            ],
          },
        ]}
        canSteer
        onSteer={() => {}}
        onRemove={() => {}}
      />,
    )

    expect(screen.getByText("Please adjust the colors")).toBeInTheDocument()
    expect(screen.getByText("report.pdf")).toBeInTheDocument()
    expect(screen.getByText("chat.queuedFollowUp")).toBeInTheDocument()
  })

  it("steers and removes the selected follow-up", () => {
    const onSteer = vi.fn()
    const onRemove = vi.fn()
    render(
      <PendingFollowUpOverlay
        followUps={[FOLLOW_UP]}
        canSteer
        onSteer={onSteer}
        onRemove={onRemove}
      />,
    )

    fireEvent.click(screen.getByRole("button", { name: "chat.steerNow" }))
    fireEvent.click(
      screen.getByRole("button", { name: "chat.removeQueuedMessage" }),
    )

    expect(onSteer).toHaveBeenCalledWith("follow-up-1")
    expect(onRemove).toHaveBeenCalledWith("follow-up-1")
  })

  it("disables steering when the connection is unavailable", () => {
    render(
      <PendingFollowUpOverlay
        followUps={[FOLLOW_UP]}
        canSteer={false}
        onSteer={() => {}}
        onRemove={() => {}}
      />,
    )

    expect(screen.getByRole("button", { name: "chat.steerNow" })).toBeDisabled()
  })
})
