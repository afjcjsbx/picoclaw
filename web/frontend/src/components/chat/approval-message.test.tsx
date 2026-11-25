import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import type { ChatApproval } from "@/store/chat"

import { ApprovalMessage } from "./approval-message"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

const APPROVAL: ChatApproval = {
  token: "0123456789abcdef0123456789abcdef",
  command: '"docker ps"',
  reason: "command is not in the risk table",
  expiresAt: Date.now() + 60_000,
}

describe("ApprovalMessage", () => {
  it("renders the command and sends an approval", () => {
    const onApprove = vi.fn(() => true)
    const onDeny = vi.fn(() => true)

    render(
      <ApprovalMessage
        approval={APPROVAL}
        onApprove={onApprove}
        onDeny={onDeny}
      />,
    )

    expect(screen.getByText('"docker ps"')).toBeInTheDocument()
    expect(
      screen.getByText("command is not in the risk table"),
    ).toBeInTheDocument()

    fireEvent.click(
      screen.getByRole("button", { name: "chat.approval.approve" }),
    )

    expect(onApprove).toHaveBeenCalledOnce()
    expect(onDeny).not.toHaveBeenCalled()
    expect(screen.getByText("chat.approval.approved")).toBeInTheDocument()
  })

  it("sends a denial", () => {
    const onApprove = vi.fn(() => true)
    const onDeny = vi.fn(() => true)

    render(
      <ApprovalMessage
        approval={APPROVAL}
        onApprove={onApprove}
        onDeny={onDeny}
      />,
    )

    fireEvent.click(screen.getByRole("button", { name: "chat.approval.deny" }))

    expect(onDeny).toHaveBeenCalledOnce()
    expect(onApprove).not.toHaveBeenCalled()
    expect(screen.getByText("chat.approval.denied")).toBeInTheDocument()
  })

  it("disables both actions after the request expires", () => {
    render(
      <ApprovalMessage
        approval={{ ...APPROVAL, expiresAt: Date.now() - 1_000 }}
        onApprove={() => true}
        onDeny={() => true}
      />,
    )

    expect(
      screen.getByRole("button", { name: "chat.approval.approve" }),
    ).toBeDisabled()
    expect(
      screen.getByRole("button", { name: "chat.approval.deny" }),
    ).toBeDisabled()
    expect(screen.getByText("chat.approval.expired")).toBeInTheDocument()
  })
})
