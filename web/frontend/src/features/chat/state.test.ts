import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  clearStoredSessionId,
  generateSessionId,
  getInitialActiveSessionId,
  normalizeUnixTimestamp,
  readStoredSessionId,
  writeStoredSessionId,
} from "./state"

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("stored session id", () => {
  it("writes, trims and clears the last session id", () => {
    writeStoredSessionId("session-1")
    expect(readStoredSessionId()).toBe("session-1")

    localStorage.setItem("picoclaw:last-session-id", "  session-2  ")
    expect(readStoredSessionId()).toBe("session-2")

    writeStoredSessionId("")
    expect(readStoredSessionId()).toBe("")
  })

  it("clears the stored id explicitly", () => {
    writeStoredSessionId("session-1")
    clearStoredSessionId()

    expect(readStoredSessionId()).toBe("")
  })
})

describe("getInitialActiveSessionId", () => {
  it("reuses the stored session id", () => {
    writeStoredSessionId("stored-session")

    expect(getInitialActiveSessionId()).toBe("stored-session")
  })

  it("generates a session id when storage is empty", () => {
    const id = getInitialActiveSessionId()

    expect(id).not.toBe("")
    expect(id).not.toBe(getInitialActiveSessionId())
  })
})

describe("generateSessionId", () => {
  it("prefers crypto.randomUUID", () => {
    vi.stubGlobal("crypto", {
      randomUUID: () => "11111111-2222-4333-8444-555555555555",
    })

    expect(generateSessionId()).toBe("11111111-2222-4333-8444-555555555555")
  })

  it("builds a v4 uuid from getRandomValues", () => {
    vi.stubGlobal("crypto", {
      getRandomValues: (target: Uint8Array) => {
        target.fill(0xff)
        return target
      },
    })

    expect(generateSessionId()).toBe("ffffffff-ffff-4fff-bfff-ffffffffffff")
  })

  it("falls back to a timestamped id without web crypto", () => {
    vi.stubGlobal("crypto", undefined)

    expect(generateSessionId()).toMatch(/^session-\d+-[0-9a-f]{1,8}$/)
  })
})

describe("normalizeUnixTimestamp", () => {
  it("promotes second timestamps to milliseconds", () => {
    expect(normalizeUnixTimestamp(1_700_000_000)).toBe(1_700_000_000_000)
  })

  it("leaves millisecond timestamps untouched", () => {
    expect(normalizeUnixTimestamp(1_700_000_000_000)).toBe(1_700_000_000_000)
  })

  it("keeps zero stable", () => {
    expect(normalizeUnixTimestamp(0)).toBe(0)
  })
})
