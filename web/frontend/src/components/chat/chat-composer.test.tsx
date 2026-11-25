import { fireEvent, render, screen } from "@testing-library/react"
import { useState } from "react"
import { describe, expect, it, vi } from "vitest"

import type { SlashCommand } from "@/api/commands"

import { ChatComposer, type ChatInputDisabledReason } from "./chat-composer"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

interface ComposerOptions {
  focusSignal?: number
  inputDisabledReason?: ChatInputDisabledReason | null
  promptHistory?: string[]
  input?: string
  onInputChange?: (value: string) => void
  onSend?: () => void
  slashCommands?: SlashCommand[]
}

function composer({
  focusSignal = 0,
  inputDisabledReason = null,
  promptHistory = [],
  input = "",
  onInputChange = () => {},
  onSend = () => {},
  slashCommands = [],
}: ComposerOptions = {}) {
  return (
    <ChatComposer
      input={input}
      attachments={[]}
      onInputChange={onInputChange}
      onAddFiles={() => {}}
      onPaste={() => {}}
      onDragEnter={() => {}}
      onDragLeave={() => {}}
      onDragOver={() => {}}
      onDrop={() => {}}
      onRemoveAttachment={() => {}}
      onSend={onSend}
      onStop={() => {}}
      onSendAudio={() => true}
      pendingFollowUps={[]}
      onSteerFollowUp={() => {}}
      onRemoveFollowUp={() => {}}
      inputDisabledReason={inputDisabledReason}
      canSend={false}
      isTyping={false}
      isDragActive={false}
      slashCommands={slashCommands}
      promptHistory={promptHistory}
      focusSignal={focusSignal}
    />
  )
}

function ControlledComposer({
  promptHistory,
  initialInput = "",
  onSend,
  slashCommands,
}: {
  promptHistory: string[]
  initialInput?: string
  onSend?: () => void
  slashCommands?: SlashCommand[]
}) {
  const [input, setInput] = useState(initialInput)

  return composer({
    input,
    onInputChange: setInput,
    onSend: () => {
      setInput("")
      onSend?.()
    },
    promptHistory,
    slashCommands,
  })
}

describe("ChatComposer", () => {
  it("focuses the message input when the focus signal changes", () => {
    const { rerender } = render(composer({ focusSignal: 0 }))
    const textarea = screen.getByRole("textbox")
    expect(textarea).not.toHaveFocus()

    rerender(composer({ focusSignal: 1 }))

    expect(textarea).toHaveFocus()
  })

  it("keeps the focus request until the input becomes editable", () => {
    const { rerender } = render(
      composer({ focusSignal: 1, inputDisabledReason: "gatewayStarting" }),
    )
    const textarea = screen.getByRole("textbox")
    expect(textarea).toBeDisabled()
    expect(textarea).not.toHaveFocus()

    rerender(composer({ focusSignal: 1 }))

    expect(textarea).toHaveFocus()
  })

  it("recalls prompts from newest to oldest and moves forward with ArrowDown", () => {
    render(<ControlledComposer promptHistory={["newest prompt", "older"]} />)
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement
    textarea.focus()

    fireEvent.keyDown(textarea, { key: "ArrowUp" })

    expect(textarea).toHaveValue("newest prompt")
    expect(textarea).toHaveFocus()
    expect(textarea.selectionStart).toBe("newest prompt".length)
    expect(textarea.selectionEnd).toBe("newest prompt".length)

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    expect(textarea).toHaveValue("older")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    expect(textarea).toHaveValue("older")

    fireEvent.keyDown(textarea, { key: "ArrowDown" })
    expect(textarea).toHaveValue("newest prompt")

    fireEvent.keyDown(textarea, { key: "ArrowDown" })
    expect(textarea).toHaveValue("")
    expect(textarea.selectionStart).toBe(0)
  })

  it("does not replace a draft or intercept modified arrow keys", () => {
    render(
      <ControlledComposer
        promptHistory={["previous"]}
        initialInput="unfinished draft"
      />,
    )
    const textarea = screen.getByRole("textbox")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    fireEvent.keyDown(textarea, { key: "ArrowUp", shiftKey: true })
    fireEvent.keyDown(textarea, { key: "ArrowUp", ctrlKey: true })

    expect(textarea).toHaveValue("unfinished draft")
  })

  it("restarts from the newest prompt after editing a recalled prompt", () => {
    render(<ControlledComposer promptHistory={["newest", "older"]} />)
    const textarea = screen.getByRole("textbox")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    fireEvent.change(textarea, { target: { value: "edited prompt" } })
    fireEvent.change(textarea, { target: { value: "" } })
    fireEvent.keyDown(textarea, { key: "ArrowUp" })

    expect(textarea).toHaveValue("newest")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    expect(textarea).toHaveValue("older")
  })

  it("resets prompt navigation after sending", () => {
    const onSend = vi.fn()
    render(
      <ControlledComposer
        promptHistory={["newest", "older"]}
        onSend={onSend}
      />,
    )
    const textarea = screen.getByRole("textbox")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    fireEvent.keyDown(textarea, { key: "Enter" })

    expect(onSend).toHaveBeenCalledOnce()
    expect(textarea).toHaveValue("")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })
    expect(textarea).toHaveValue("newest")
  })

  it("keeps ArrowUp navigation in the slash command palette", () => {
    render(
      <ControlledComposer
        initialInput="/"
        promptHistory={["previous"]}
        slashCommands={[
          { command: "/help", description: "Show help" },
          { command: "/clear", description: "Clear conversation" },
        ]}
      />,
    )
    const textarea = screen.getByRole("textbox")
    const options = screen.getAllByRole("option")
    expect(options[0]).toHaveAttribute("aria-selected", "true")

    fireEvent.keyDown(textarea, { key: "ArrowUp" })

    expect(options[1]).toHaveAttribute("aria-selected", "true")
    expect(textarea).toHaveValue("/")
  })
})
