import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { A2AForm } from "@/components/channels/channel-forms/a2a-form"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

const baseConfig = {
  host: "127.0.0.1",
  port: 9900,
  url: "",
  name: "PicoClaw",
  description: "",
  max_body_bytes: 1048576,
  rate_limit_per_minute: 60,
  max_tasks: 256,
  max_concurrent: 4,
  task_timeout_seconds: 300,
  push_allow_private: false,
  streaming: { enabled: false },
  _tokens: [],
  _tokens_cleared: false,
}

describe("A2AForm", () => {
  it("renders the listener defaults and relevant settings", () => {
    render(
      <A2AForm config={baseConfig} onChange={vi.fn()} configuredSecrets={[]} />,
    )

    expect(screen.getByLabelText("channels.a2a.host")).toHaveValue("127.0.0.1")
    expect(screen.getByLabelText("channels.a2a.port")).toHaveValue(9900)
    expect(screen.getByLabelText("channels.a2a.maxConcurrent")).toHaveValue(4)
    expect(
      screen.queryByText("channels.a2a.tokensRequired"),
    ).not.toBeInTheDocument()
  })

  it("warns when a non-loopback listener has no tokens", () => {
    render(
      <A2AForm
        config={{ ...baseConfig, host: "0.0.0.0" }}
        onChange={vi.fn()}
        configuredSecrets={[]}
      />,
    )

    expect(screen.getByText("channels.a2a.tokensRequired")).toBeInTheDocument()
  })

  it("masks bearer tokens and adds new values as an array", () => {
    const onChange = vi.fn()
    render(
      <A2AForm
        config={{ ...baseConfig, _tokens: ["new-secret-token"] }}
        onChange={onChange}
        configuredSecrets={["tokens"]}
      />,
    )

    expect(screen.getByText("••••••••")).toBeInTheDocument()
    expect(screen.queryByText("new-secret-token")).not.toBeInTheDocument()
    const input = screen.getByLabelText("channels.a2a.tokens")
    expect(input).toHaveAttribute("type", "password")

    fireEvent.change(input, { target: { value: "another-token" } })
    fireEvent.click(screen.getByRole("button", { name: "common.confirm" }))

    expect(onChange).toHaveBeenCalledWith("_tokens", [
      "new-secret-token",
      "another-token",
    ])
  })

  it("requests explicit removal of configured tokens", () => {
    const onChange = vi.fn()
    render(
      <A2AForm
        config={baseConfig}
        onChange={onChange}
        configuredSecrets={["tokens"]}
      />,
    )

    fireEvent.click(
      screen.getByRole("button", { name: "channels.a2a.clearTokens" }),
    )

    expect(onChange).toHaveBeenCalledWith("_tokens", [])
    expect(onChange).toHaveBeenCalledWith("_tokens_cleared", true)
  })
})
