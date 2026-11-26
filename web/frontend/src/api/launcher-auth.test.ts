import { afterEach, describe, expect, it, vi } from "vitest"

import {
  getLauncherAuthStatus,
  postLauncherDashboardLogin,
  postLauncherDashboardLogout,
  postLauncherDashboardSetup,
} from "./launcher-auth"

afterEach(() => {
  vi.unstubAllGlobals()
})

function mockFetch(response: Response) {
  const fetchMock = vi.fn().mockResolvedValue(response)
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

describe("launcher auth API", () => {
  it("logs in with a trimmed password and same-origin credentials", async () => {
    const fetchMock = mockFetch(new Response(null, { status: 200 }))

    await expect(postLauncherDashboardLogin("  secret  ")).resolves.toEqual({
      ok: true,
    })
    expect(fetchMock).toHaveBeenCalledOnce()
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe("/api/auth/login")
    expect(init.method).toBe("POST")
    expect(init.credentials).toBe("same-origin")
    expect(new Headers(init.headers).get("Content-Type")).toBe(
      "application/json",
    )
    expect(new Headers(init.headers).get("X-Requested-With")).toBe(
      "XMLHttpRequest",
    )
    expect(JSON.parse(String(init.body))).toEqual({ password: "secret" })
  })

  it("returns the server error for a failed login", async () => {
    mockFetch(
      new Response(`{"error":"invalid password"}`, {
        status: 401,
        headers: { "content-type": "application/json" },
      }),
    )

    await expect(postLauncherDashboardLogin("wrong")).resolves.toEqual({
      ok: false,
      status: 401,
      error: "invalid password",
    })
  })

  it("falls back to the status when a login error is not JSON", async () => {
    mockFetch(new Response("unavailable", { status: 503 }))

    await expect(postLauncherDashboardLogin("secret")).resolves.toEqual({
      ok: false,
      status: 503,
      error: "Request failed with status 503",
    })
  })

  it("propagates login network failures", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    )

    await expect(postLauncherDashboardLogin("secret")).rejects.toThrow(
      "Failed to fetch",
    )
  })

  it("loads authentication status", async () => {
    const fetchMock = mockFetch(
      new Response(`{"authenticated":true,"initialized":true}`, {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    )

    await expect(getLauncherAuthStatus()).resolves.toEqual({
      authenticated: true,
      initialized: true,
    })
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe("/api/auth/status")
    expect(init.method).toBe("GET")
    expect(init.credentials).toBe("same-origin")
  })

  it("throws when authentication status cannot be loaded", async () => {
    mockFetch(new Response("error", { status: 500 }))

    await expect(getLauncherAuthStatus()).rejects.toThrow("status 500")
  })

  it("logs out with an empty JSON body", async () => {
    const fetchMock = mockFetch(new Response(null, { status: 204 }))

    await expect(postLauncherDashboardLogout()).resolves.toBe(true)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe("/api/auth/logout")
    expect(init.method).toBe("POST")
    expect(init.credentials).toBe("same-origin")
    expect(init.body).toBe("{}")
  })

  it("reports logout failures", async () => {
    mockFetch(new Response(null, { status: 500 }))

    await expect(postLauncherDashboardLogout()).resolves.toBe(false)
  })

  it("sets up the dashboard with trimmed matching passwords", async () => {
    const fetchMock = mockFetch(new Response(null, { status: 200 }))

    await expect(
      postLauncherDashboardSetup("  new-password  ", " new-password "),
    ).resolves.toEqual({ ok: true })
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe("/api/auth/setup")
    expect(init.method).toBe("POST")
    expect(init.credentials).toBe("same-origin")
    expect(JSON.parse(String(init.body))).toEqual({
      password: "new-password",
      confirm: "new-password",
    })
  })

  it("returns setup validation errors", async () => {
    mockFetch(
      new Response(`{"error":"passwords do not match"}`, {
        status: 400,
        headers: { "content-type": "application/json" },
      }),
    )

    await expect(
      postLauncherDashboardSetup("new-password", "different"),
    ).resolves.toEqual({ ok: false, error: "passwords do not match" })
  })
})
