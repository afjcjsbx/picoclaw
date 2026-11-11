import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { ChatComposer, type ChatInputDisabledReason } from "./chat-composer"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

function composer(
  focusSignal: number,
  inputDisabledReason: ChatInputDisabledReason | null = null,
) {
  return (
    <ChatComposer
      input=""
      attachments={[]}
      onInputChange={() => {}}
      onAddFiles={() => {}}
      onPaste={() => {}}
      onDragEnter={() => {}}
      onDragLeave={() => {}}
      onDragOver={() => {}}
      onDrop={() => {}}
      onRemoveAttachment={() => {}}
      onSend={() => {}}
      onStop={() => {}}
      onSendAudio={() => true}
      pendingFollowUps={[]}
      onSteerFollowUp={() => {}}
      onRemoveFollowUp={() => {}}
      inputDisabledReason={inputDisabledReason}
      canSend={false}
      isTyping={false}
      isDragActive={false}
      focusSignal={focusSignal}
    />
  )
}

describe("ChatComposer", () => {
  it("focuses the message input when the focus signal changes", () => {
    const { rerender } = render(composer(0))
    const textarea = screen.getByRole("textbox")
    expect(textarea).not.toHaveFocus()

    rerender(composer(1))

    expect(textarea).toHaveFocus()
  })

  it("keeps the focus request until the input becomes editable", () => {
    const { rerender } = render(composer(1, "gatewayStarting"))
    const textarea = screen.getByRole("textbox")
    expect(textarea).toBeDisabled()
    expect(textarea).not.toHaveFocus()

    rerender(composer(1))

    expect(textarea).toHaveFocus()
  })
})
