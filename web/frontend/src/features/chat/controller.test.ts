import { getDefaultStore } from "jotai"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { getSessionHistory, type SessionDetail } from "@/api/sessions"
import {
  chatAtom,
  getChatState,
  sessionRunningAtom,
  splitConversationsAtom,
  splitSessionStatesAtom,
  updateChatStore,
} from "@/store/chat"
import { gatewayAtom } from "@/store/gateway"

import {
  initializeChatStore,
  newChatSession,
  sendChatMessage,
  switchChatSession,
  teardownChatStore,
} from "./controller"

vi.mock("@/api/sessions", () => ({ getSessionHistory: vi.fn() }))
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }))

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static readonly instances: FakeWebSocket[] = []
  static throwOnConstruct = false

  readonly url: string
  readyState = FakeWebSocket.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  readonly sent: string[] = []
  throwOnSend = false

  constructor(url: string | URL) {
    if (FakeWebSocket.throwOnConstruct) {
      FakeWebSocket.throwOnConstruct = false
      throw new Error("socket construction failed")
    }
    this.url = String(url)
    FakeWebSocket.instances.push(this)
  }

  send(data: string) {
    if (this.throwOnSend || this.readyState !== FakeWebSocket.OPEN) {
      throw new Error("socket is not writable")
    }
    this.sent.push(data)
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.(new Event("open"))
  }

  emit(message: unknown) {
    this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(message) }))
  }

  emitRaw(data: string) {
    this.onmessage?.(new MessageEvent("message", { data }))
  }

  serverClose() {
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.(new CloseEvent("close"))
  }

  fail() {
    this.onerror?.(new Event("error"))
  }
}

const store = getDefaultStore()
const historyMock = vi.mocked(getSessionHistory)

function history(
  id: string,
  messages: SessionDetail["messages"] = [],
): SessionDetail {
  return {
    id,
    start: 0,
    total: messages.length,
    messages,
    summary: "",
    created: "2026-01-01T00:00:00Z",
    updated: "2026-01-01T00:00:00Z",
  }
}

function chatSockets() {
  return FakeWebSocket.instances.filter(
    (socket) => !socket.url.includes("all_sessions=1"),
  )
}

function activitySockets() {
  return FakeWebSocket.instances.filter((socket) =>
    socket.url.includes("all_sessions=1"),
  )
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.spyOn(Math, "random").mockReturnValue(0.5)
  teardownChatStore()
  FakeWebSocket.instances.length = 0
  FakeWebSocket.throwOnConstruct = false
  vi.stubGlobal("WebSocket", FakeWebSocket)
  historyMock.mockReset().mockImplementation(async (id) => history(id))

  store.set(chatAtom, {
    messages: [],
    connectionState: "disconnected",
    isTyping: false,
    activeSessionId: "session-a",
    hasHydratedActiveSession: true,
    historyStart: 0,
    hasMoreHistory: false,
  })
  store.set(gatewayAtom, {
    status: "running",
    canStart: true,
    restartRequired: false,
  })
  store.set(splitConversationsAtom, [])
  store.set(splitSessionStatesAtom, {})
  store.set(sessionRunningAtom, {})
  globalThis.localStorage.clear()
  initializeChatStore()
})

afterEach(() => {
  teardownChatStore()
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("chat controller sockets", () => {
  it("connects the active session and activity stream", () => {
    expect(chatSockets()).toHaveLength(1)
    expect(chatSockets()[0].url).toContain("session_id=session-a")
    expect(activitySockets()).toHaveLength(1)
    expect(getChatState().connectionState).toBe("connecting")

    chatSockets()[0].open()
    expect(getChatState().connectionState).toBe("connected")

    activitySockets()[0].open()
    activitySockets()[0].emit({
      type: "session.activity",
      session_id: "session-a",
      payload: { event: "typing.start" },
    })
    expect(store.get(sessionRunningAtom)["session-a"]).toBe(true)
  })

  it("ignores malformed socket messages without changing state", () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    const socket = chatSockets()[0]
    socket.open()

    socket.emitRaw("not-json")

    expect(warn).toHaveBeenCalledOnce()
    expect(getChatState().messages).toEqual([])
  })

  it("reconnects after a close and resyncs missed history", async () => {
    updateChatStore({
      messages: [
        {
          id: "local-user",
          role: "user",
          content: "question",
          timestamp: Date.now(),
        },
      ],
    })
    historyMock.mockResolvedValue(
      history("session-a", [
        {
          role: "assistant",
          content: "missed final answer",
          created_at: new Date().toISOString(),
        },
      ]),
    )
    const first = chatSockets()[0]
    first.open()
    first.serverClose()
    expect(getChatState().connectionState).toBe("disconnected")

    await vi.advanceTimersByTimeAsync(1000)
    expect(chatSockets()).toHaveLength(2)
    const second = chatSockets()[1]
    second.open()
    await Promise.resolve()
    await Promise.resolve()

    expect(getChatState().connectionState).toBe("connected")
    expect(getChatState().messages.map((message) => message.content)).toContain(
      "missed final answer",
    )
  })

  it("schedules a reconnect after socket construction fails", async () => {
    const first = chatSockets()[0]
    first.open()
    first.serverClose()
    FakeWebSocket.throwOnConstruct = true
    const error = vi.spyOn(console, "error").mockImplementation(() => {})

    await vi.advanceTimersByTimeAsync(1000)
    expect(getChatState().connectionState).toBe("error")
    expect(error).toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(2000)
    expect(chatSockets()).toHaveLength(2)
  })

  it("teardown detaches sockets and prevents further reconnects", async () => {
    const socket = chatSockets()[0]
    socket.open()
    teardownChatStore()

    expect(socket.onopen).toBeNull()
    expect(socket.onmessage).toBeNull()
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(chatSockets()).toHaveLength(1)
  })
})

describe("chat controller sessions", () => {
  it("ignores stale socket callbacks after switching sessions", async () => {
    const first = chatSockets()[0]
    first.open()
    const staleOnMessage = first.onmessage
    historyMock.mockResolvedValue(history("session-b"))

    await switchChatSession("session-b")

    expect(getChatState().activeSessionId).toBe("session-b")
    expect(chatSockets()).toHaveLength(2)
    staleOnMessage?.(
      new MessageEvent("message", {
        data: JSON.stringify({
          type: "message.create",
          session_id: "session-a",
          payload: { message_id: "stale", content: "stale answer" },
        }),
      }),
    )
    expect(getChatState().messages).toEqual([])

    const second = chatSockets()[1]
    second.open()
    second.emit({
      type: "message.create",
      session_id: "session-a",
      payload: { message_id: "wrong-session", content: "wrong answer" },
    })
    second.emit({
      type: "message.create",
      session_id: "session-b",
      payload: { message_id: "right-session", content: "right answer" },
    })
    expect(getChatState().messages.map((message) => message.id)).toEqual([
      "right-session",
    ])
  })

  it("keeps the current session when history loading fails", async () => {
    const socket = chatSockets()[0]
    socket.open()
    historyMock.mockRejectedValue(new Error("history unavailable"))
    const error = vi.spyOn(console, "error").mockImplementation(() => {})

    await switchChatSession("missing-session")

    expect(getChatState().activeSessionId).toBe("session-a")
    expect(getChatState().connectionState).toBe("connected")
    expect(chatSockets()).toHaveLength(1)
    expect(error).toHaveBeenCalled()
  })

  it("creates a new session and connects it", async () => {
    updateChatStore({
      messages: [
        { id: "u1", role: "user", content: "old", timestamp: Date.now() },
      ],
    })

    const id = await newChatSession(true)

    expect(id).toBeTruthy()
    expect(getChatState().activeSessionId).toBe(id)
    expect(getChatState().messages).toEqual([])
    expect(chatSockets()).toHaveLength(2)
    expect(chatSockets()[1].url).toContain(`session_id=${id}`)
  })
})

describe("sendChatMessage", () => {
  it("sends normalized content and attachments", () => {
    const socket = chatSockets()[0]
    socket.open()

    expect(
      sendChatMessage({
        content: "  hello  ",
        attachments: [
          { type: "image", url: "https://example.test/a.png", filename: "a.png" },
          { type: "file", url: "" },
        ],
      }),
    ).toBe(true)

    expect(socket.sent).toHaveLength(1)
    expect(JSON.parse(socket.sent[0])).toMatchObject({
      type: "message.send",
      payload: {
        content: "hello",
        attachments: [
          {
            type: "image",
            url: "https://example.test/a.png",
            filename: "a.png",
          },
        ],
      },
    })
    expect(getChatState().messages[0]).toMatchObject({
      role: "user",
      content: "hello",
    })
    expect(getChatState().isTyping).toBe(true)
  })

  it("rolls back the optimistic message when send throws", () => {
    const socket = chatSockets()[0]
    socket.open()
    socket.throwOnSend = true
    const error = vi.spyOn(console, "error").mockImplementation(() => {})

    expect(sendChatMessage({ content: "hello" })).toBe(false)

    expect(getChatState().messages).toEqual([])
    expect(getChatState().isTyping).toBe(false)
    expect(error).toHaveBeenCalledOnce()
  })

  it("rejects empty messages and clears history for /clear", () => {
    const socket = chatSockets()[0]
    socket.open()
    expect(sendChatMessage({ content: "   " })).toBe(false)
    expect(socket.sent).toEqual([])

    updateChatStore({
      messages: [
        { id: "u1", role: "user", content: "old", timestamp: Date.now() },
      ],
      historyStart: 10,
      hasMoreHistory: true,
    })
    store.set(splitSessionStatesAtom, {
      "session-a": {
        messages: [
          { id: "u1", role: "user", content: "old", timestamp: Date.now() },
        ],
        isTyping: false,
        historyStart: 10,
        hasMoreHistory: true,
      },
    })

    expect(sendChatMessage({ content: "/clear" })).toBe(true)
    expect(getChatState().messages.map((message) => message.content)).toEqual([
      "/clear",
    ])
    expect(getChatState().historyStart).toBe(0)
    expect(getChatState().hasMoreHistory).toBe(false)
    expect(store.get(splitSessionStatesAtom)["session-a"].messages).toEqual([])
  })
})
