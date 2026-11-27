import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { DiscordForm } from "@/components/channels/channel-forms/discord-form"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

describe("DiscordForm", () => {
  it("allows inbound attachments by default", () => {
    render(
      <DiscordForm config={{}} onChange={vi.fn()} configuredSecrets={[]} />,
    )

    expect(
      screen.getByRole("switch", { name: "channels.field.allowAttachments" }),
    ).toBeChecked()
  })

  it("reflects and updates the inbound attachments setting", () => {
    const onChange = vi.fn()
    render(
      <DiscordForm
        config={{ allow_attachments: false }}
        onChange={onChange}
        configuredSecrets={[]}
      />,
    )

    const toggle = screen.getByRole("switch", {
      name: "channels.field.allowAttachments",
    })
    expect(toggle).not.toBeChecked()

    fireEvent.click(toggle)
    expect(onChange).toHaveBeenCalledWith("allow_attachments", true)
  })
})
