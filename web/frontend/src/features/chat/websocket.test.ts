import { afterEach, describe, expect, it, vi } from "vitest"

import {
  invalidateSocket,
  isCurrentSocket,
  normalizeWsUrlForBrowser,
} from "./websocket"

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("normalizeWsUrlForBrowser", () => {
  it("rewrites loopback gateway URLs to the browser host on LAN", () => {
    vi.stubGlobal("location", {
      hostname: "192.168.1.50",
      href: "http://192.168.1.50:18800/",
    })

    expect(normalizeWsUrlForBrowser("ws://127.0.0.1:18800/pico/ws")).toBe(
      "ws://192.168.1.50:18800/pico/ws",
    )
  })

  it("matches localhost and loopback hosts for host-only cookies", () => {
    vi.stubGlobal("location", {
      hostname: "localhost",
      href: "http://localhost:18800/",
    })

    expect(normalizeWsUrlForBrowser("ws://127.0.0.1:18800/pico/ws")).toBe(
      "ws://localhost:18800/pico/ws",
    )
  })

  it("keeps remote gateway URLs unchanged", () => {
    vi.stubGlobal("location", {
      hostname: "192.168.1.50",
      href: "http://192.168.1.50:18800/",
    })

    expect(normalizeWsUrlForBrowser("wss://gateway.example/pico/ws")).toBe(
      "wss://gateway.example/pico/ws",
    )
  })

  it("returns malformed URLs unchanged and warns", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})

    expect(normalizeWsUrlForBrowser("not a url")).toBe("not a url")
    expect(warn).toHaveBeenCalledOnce()
  })
})

describe("invalidateSocket", () => {
  it("detaches handlers before closing", () => {
    const socket = {
      onopen: vi.fn(),
      onmessage: vi.fn(),
      onclose: vi.fn(),
      onerror: vi.fn(),
      close: vi.fn(),
    } as unknown as WebSocket

    invalidateSocket(socket)

    expect(socket.onopen).toBeNull()
    expect(socket.onmessage).toBeNull()
    expect(socket.onclose).toBeNull()
    expect(socket.onerror).toBeNull()
    expect(socket.close).toHaveBeenCalledOnce()
  })

  it("ignores null sockets", () => {
    expect(() => invalidateSocket(null)).not.toThrow()
  })
})

describe("isCurrentSocket", () => {
  const socket = {} as WebSocket
  const other = {} as WebSocket
  const base = {
    socket,
    currentSocket: socket,
    generation: 2,
    currentGeneration: 2,
    sessionId: "session-a",
    currentSessionId: "session-a",
  }

  it("accepts only the matching socket, generation, and session", () => {
    expect(isCurrentSocket(base)).toBe(true)
    expect(isCurrentSocket({ ...base, currentSocket: other })).toBe(false)
    expect(isCurrentSocket({ ...base, currentGeneration: 3 })).toBe(false)
    expect(isCurrentSocket({ ...base, currentSessionId: "session-b" })).toBe(
      false,
    )
  })
})
