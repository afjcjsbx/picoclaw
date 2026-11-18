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
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe("/api/models")
    expect(init.credentials).toBe("same-origin")
    expect(new Headers(init.headers).get("X-Requested-With")).toBe(
      "XMLHttpRequest",
    )
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

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe("POST")
    expect(init.body).toBe("{}")
    expect(init.credentials).toBe("include")
    const headers = new Headers(init.headers)
    expect(headers.get("content-type")).toBe("application/json")
    expect(headers.get("X-Requested-With")).toBe("XMLHttpRequest")
  })

  it("does not overwrite an explicit X-Requested-With value", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("ok"))
    vi.stubGlobal("fetch", fetchMock)

    await launcherFetch("/api/models", {
      headers: { "X-Requested-With": "CustomValue" },
    })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(new Headers(init.headers).get("X-Requested-With")).toBe(
      "CustomValue",
    )
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
