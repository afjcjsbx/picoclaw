import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { AssistantMessage } from "./assistant-message"

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}))

describe("AssistantMessage activity", () => {
  it("expands reasoning details", () => {
    render(<AssistantMessage kind="thought" content="Full reasoning details" />)

    expect(screen.queryByText("Full reasoning details")).not.toBeInTheDocument()
    expect(screen.getByText("chat.reasoningLabel")).toHaveClass("text-[13px]")
    fireEvent.click(screen.getByRole("button", { name: /chat.reasoningLabel/ }))

    expect(screen.getByText("Full reasoning details")).toBeInTheDocument()
  })

  it("expands each tool with full input and output", () => {
    render(
      <AssistantMessage
        content=""
        kind="tool_calls"
        toolCalls={[
          {
            id: "call-1",
            function: { name: "exec", arguments: '{"command":"ls"}' },
            input: '{\n  "command": "ls"\n}',
            output: "file.txt",
          },
          {
            id: "call-2",
            function: { name: "read_file", arguments: '{"path":"a.txt"}' },
            input: '{\n  "path": "a.txt"\n}',
            output: "contents",
          },
        ]}
      />,
    )

    expect(screen.queryByText("chat.toolOutputLabel")).not.toBeInTheDocument()
    expect(screen.getByText("Run command")).toHaveClass("text-[13px]")
    fireEvent.click(screen.getByRole("button", { name: /Run command ls/ }))

    expect(screen.getByText("chat.toolInputLabel")).toBeInTheDocument()
    expect(screen.getByText("chat.toolOutputLabel")).toBeInTheDocument()
    expect(screen.getByText(/"command": "ls"/)).toBeInTheDocument()
    expect(screen.getByText("file.txt")).toBeInTheDocument()
    expect(
      screen.getByRole("button", { name: /Read file a.txt/ }),
    ).toHaveAttribute("aria-expanded", "false")
  })
})

describe("AssistantMessage actions", () => {
  it("renders turn stats immediately after the fork action", () => {
    render(
      <AssistantMessage
        content="Done"
        modelName="gpt-4o"
        onFork={() => {}}
        usage={{ input_tokens: 100, output_tokens: 50, total_tokens: 150 }}
      />,
    )

    const fork = screen.getByRole("button", { name: "chat.forkAtMessage" })
    const stats = screen.getByRole("button", { name: "chat.turnStats.open" })
    expect(
      fork.compareDocumentPosition(stats) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it("does not render turn stats without usage data", () => {
    render(<AssistantMessage content="Done" onFork={() => {}} />)

    expect(
      screen.queryByRole("button", { name: "chat.turnStats.open" }),
    ).not.toBeInTheDocument()
  })
})
