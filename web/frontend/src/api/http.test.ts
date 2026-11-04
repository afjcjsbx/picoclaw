import { afterEach, describe, expect, it, vi } from "vitest"

import { launcherFetch } from "./http"

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("launcherFetch", () => {
  it("sends same-origin credentials by default", async () => {
    const response = new Response("ok")
    const fetchMock = vi.fn().mockResolvedValue(response)
    vi.stubGlobal("fetch", fetchMock)

    await expect(launcherFetch("/api/models")).resolves.toBe(response)
    expect(fetchMock).toHaveBeenCalledWith("/api/models", {
      credentials: "same-origin",
    })
  })

  it("keeps the caller request options", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("ok"))
    vi.stubGlobal("fetch", fetchMock)

    await launcherFetch("/api/models", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "{}",
      credentials: "include",
    })

    expect(fetchMock).toHaveBeenCalledWith("/api/models", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: "{}",
      credentials: "include",
    })
  })

  it("returns non-JSON 401 responses untouched", async () => {
    const response = new Response("unauthorized", {
      status: 401,
      headers: { "content-type": "text/plain" },
    })
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response))

    const result = await launcherFetch("/api/models")

    expect(result.status).toBe(401)
    expect(await result.text()).toBe("unauthorized")
  })

  it("propagates network errors", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockRejectedValue(new TypeError("Failed to fetch")),
    )

    await expect(launcherFetch("/api/models")).rejects.toThrow(
      "Failed to fetch",
    )
  })
})
