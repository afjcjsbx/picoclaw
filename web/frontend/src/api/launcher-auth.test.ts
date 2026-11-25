import { afterEach, describe, expect, it, vi } from "vitest"

import { postLauncherDashboardSetup } from "./launcher-auth"

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("postLauncherDashboardSetup", () => {
  it("sends the bootstrap token for first-run setup", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"status":"ok"}'))
    vi.stubGlobal("fetch", fetchMock)

    await expect(
      postLauncherDashboardSetup(" password ", " password ", " setup-token "),
    ).resolves.toEqual({ ok: true })

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe("/api/auth/setup")
    expect(JSON.parse(String(init.body))).toEqual({
      password: "password",
      confirm: "password",
      setup_token: "setup-token",
    })
  })

  it("omits the bootstrap token for authenticated password changes", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{"status":"ok"}'))
    vi.stubGlobal("fetch", fetchMock)

    await expect(
      postLauncherDashboardSetup("new-password", "new-password"),
    ).resolves.toEqual({ ok: true })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(JSON.parse(String(init.body))).toEqual({
      password: "new-password",
      confirm: "new-password",
    })
  })
})
