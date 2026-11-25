import { fireEvent, render, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { Route } from "./launcher-setup"

const { setupMock } = vi.hoisted(() => ({ setupMock: vi.fn() }))

vi.mock("@/api/launcher-auth", () => ({
  postLauncherDashboardSetup: setupMock,
}))

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { changeLanguage: vi.fn() },
  }),
}))

const LauncherSetupPage = Route.options.component
if (!LauncherSetupPage) {
  throw new Error("Launcher setup route component is missing")
}

describe("LauncherSetupPage", () => {
  beforeEach(() => {
    setupMock.mockResolvedValue({ ok: false, error: "invalid setup token" })
  })

  it("masks and submits the one-time setup token", async () => {
    render(<LauncherSetupPage />)

    const token = screen.getByLabelText("launcherSetup.tokenLabel")
    expect(token).toHaveAttribute("type", "password")
    fireEvent.change(token, { target: { value: "bootstrap-token" } })
    fireEvent.change(screen.getByLabelText("launcherSetup.passwordLabel"), {
      target: { value: "dashboard-password" },
    })
    fireEvent.change(screen.getByLabelText("launcherSetup.confirmLabel"), {
      target: { value: "dashboard-password" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: "launcherSetup.submit" }),
    )

    await waitFor(() => {
      expect(setupMock).toHaveBeenCalledWith(
        "dashboard-password",
        "dashboard-password",
        "bootstrap-token",
      )
    })
  })

  it("does not submit when the passwords do not match", () => {
    render(<LauncherSetupPage />)

    fireEvent.change(screen.getByLabelText("launcherSetup.tokenLabel"), {
      target: { value: "bootstrap-token" },
    })
    fireEvent.change(screen.getByLabelText("launcherSetup.passwordLabel"), {
      target: { value: "dashboard-password" },
    })
    fireEvent.change(screen.getByLabelText("launcherSetup.confirmLabel"), {
      target: { value: "different-password" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: "launcherSetup.submit" }),
    )

    expect(setupMock).not.toHaveBeenCalled()
    expect(screen.getByRole("alert")).toHaveTextContent(
      "launcherSetup.errorMismatch",
    )
  })
})
