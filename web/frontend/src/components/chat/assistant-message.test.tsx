import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { AssistantMessage } from "./assistant-message"

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}))

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
