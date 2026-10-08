import { getDefaultStore } from "jotai"
import { toast } from "sonner"

import {
  loadSessionMessages,
  mergeHistoryMessages,
} from "@/features/chat/history"
import { type PicoMessage, handlePicoMessage } from "@/features/chat/protocol"
import {
  clearStoredSessionId,
  generateSessionId,
  readStoredSessionId,
} from "@/features/chat/state"
import { invalidateSocket, isCurrentSocket } from "@/features/chat/websocket"
import i18n from "@/i18n"
import {
  type ChatAttachment,
  type ChatStoreState,
  chatAtom,
  getChatState,
  initializeSplitSessionState,
  splitConversationsAtom,
  splitSessionStatesAtom,
  updateChatStore,
  updateSplitSessionState,
} from "@/store/chat"
import { type GatewayState, gatewayAtom } from "@/store/gateway"

const store = getDefaultStore()

let wsRef: WebSocket | null = null
let isConnecting = false
let msgIdCounter = 0
let activeSessionIdRef = getChatState().activeSessionId
let initialized = false
let unsubscribeGateway: (() => void) | null = null
let hydratePromise: Promise<void> | null = null
let connectionGeneration = 0
let reconnectTimer: number | null = null
let reconnectAttempts = 0
let shouldMaintainConnection = false
let isLoadingOlderHistory = false
const splitConnections = new Map<
  string,
  {
    socket: WebSocket | null
    timer: number | null
    attempts: number
    generation: number
  }
>()
let unsubscribeChat: (() => void) | null = null
let unsubscribeSplitConversations: (() => void) | null = null
let lastSplitSyncKey = ""

function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

function shouldReconnectFor(generation: number, sessionId: string): boolean {
  return (
    shouldMaintainConnection &&
    generation === connectionGeneration &&
    sessionId === activeSessionIdRef &&
    store.get(gatewayAtom).status === "running"
  )
}

function scheduleReconnect(generation: number, sessionId: string) {
  if (!shouldReconnectFor(generation, sessionId) || reconnectTimer !== null) {
    return
  }

  const delay = Math.min(1000 * 2 ** reconnectAttempts, 5000)
  reconnectAttempts += 1
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    if (!shouldReconnectFor(generation, sessionId)) {
      return
    }
    void connectChat()
  }, delay)
}

function needsActiveSessionHydration(): boolean {
  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  return Boolean(
    storedSessionId &&
    storedSessionId === state.activeSessionId &&
    !state.hasHydratedActiveSession,
  )
}

function setActiveSessionId(sessionId: string, patch: Partial<ChatStoreState>) {
  activeSessionIdRef = sessionId
  updateChatStore({ ...patch, activeSessionId: sessionId })
}

function splitSessionIds(): Set<string> {
  const activeSessionId = getChatState().activeSessionId
  return new Set(
    store
      .get(splitConversationsAtom)
      .find((group) => group.includes(activeSessionId))
      ?.filter((sessionId) => sessionId !== activeSessionId) ?? [],
  )
}

function isCurrentSplitConnection(
  sessionId: string,
  connection: { socket: WebSocket | null; generation: number },
  socket: WebSocket,
  generation: number,
): boolean {
  return (
    splitConnections.get(sessionId) === connection &&
    connection.socket === socket &&
    connection.generation === generation &&
    splitSessionIds().has(sessionId) &&
    store.get(gatewayAtom).status === "running"
  )
}

function closeSplitConnection(sessionId: string) {
  const connection = splitConnections.get(sessionId)
  if (!connection) return
  splitConnections.delete(sessionId)
  connection.generation += 1
  if (connection.timer !== null) {
    window.clearTimeout(connection.timer)
  }
  invalidateSocket(connection.socket)
}

function scheduleSplitReconnect(
  sessionId: string,
  connection: NonNullable<ReturnType<typeof splitConnections.get>>,
) {
  if (
    splitConnections.get(sessionId) !== connection ||
    !splitSessionIds().has(sessionId) ||
    store.get(gatewayAtom).status !== "running" ||
    connection.timer !== null
  ) {
    return
  }

  const delay = Math.min(1000 * 2 ** connection.attempts, 5000)
  connection.attempts += 1
  connection.timer = window.setTimeout(() => {
    connection.timer = null
    void connectSplitSession(sessionId, connection)
  }, delay)
}

async function connectSplitSession(
  sessionId: string,
  connection: NonNullable<ReturnType<typeof splitConnections.get>>,
) {
  if (
    splitConnections.get(sessionId) !== connection ||
    !splitSessionIds().has(sessionId) ||
    store.get(gatewayAtom).status !== "running" ||
    connection.socket
  ) {
    return
  }

  if (
    connection.generation === 0 &&
    !store.get(splitSessionStatesAtom)[sessionId]
  ) {
    try {
      const history = await loadSessionMessages(sessionId)
      if (splitConnections.get(sessionId) !== connection) return
      initializeSplitSessionState(sessionId, history.messages)
    } catch (error) {
      console.error("Failed to load split conversation history:", error)
      if (splitConnections.get(sessionId) !== connection) return
      initializeSplitSessionState(sessionId, [])
    }
  }

  if (
    splitConnections.get(sessionId) !== connection ||
    !splitSessionIds().has(sessionId) ||
    store.get(gatewayAtom).status !== "running"
  ) {
    return
  }

  const generation = ++connection.generation
  try {
    const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:"
    const socket = new WebSocket(
      `${wsScheme}//${window.location.host}/pico/ws?session_id=${encodeURIComponent(sessionId)}`,
    )
    connection.socket = socket

    socket.onmessage = (event) => {
      if (
        !isCurrentSplitConnection(sessionId, connection, socket, generation)
      ) {
        return
      }
      try {
        handlePicoMessage(JSON.parse(event.data) as PicoMessage, sessionId)
      } catch {
        console.warn("Non-JSON message from pico:", event.data)
      }
    }

    socket.onopen = () => {
      if (
        !isCurrentSplitConnection(sessionId, connection, socket, generation)
      ) {
        return
      }
      connection.attempts = 0
    }

    socket.onclose = () => {
      if (
        !isCurrentSplitConnection(sessionId, connection, socket, generation)
      ) {
        return
      }
      connection.socket = null
      scheduleSplitReconnect(sessionId, connection)
    }

    socket.onerror = () => {
      if (
        !isCurrentSplitConnection(sessionId, connection, socket, generation)
      ) {
        return
      }
      connection.socket = null
      invalidateSocket(socket)
      scheduleSplitReconnect(sessionId, connection)
    }
  } catch (error) {
    console.error("Failed to connect split conversation:", error)
    scheduleSplitReconnect(sessionId, connection)
  }
}

function syncSplitConnections() {
  const targets =
    store.get(gatewayAtom).status === "running"
      ? splitSessionIds()
      : new Set<string>()
  const key = `${store.get(gatewayAtom).status}:${getChatState().activeSessionId}:${[...targets].sort().join(",")}`
  if (key === lastSplitSyncKey) return
  lastSplitSyncKey = key

  for (const sessionId of splitConnections.keys()) {
    if (!targets.has(sessionId)) closeSplitConnection(sessionId)
  }
  for (const sessionId of targets) {
    if (splitConnections.has(sessionId)) continue
    const connection = { socket: null, timer: null, attempts: 0, generation: 0 }
    splitConnections.set(sessionId, connection)
    void connectSplitSession(sessionId, connection)
  }
}

function disconnectChatInternal({
  clearDesiredConnection,
}: {
  clearDesiredConnection: boolean
}) {
  connectionGeneration += 1
  clearReconnectTimer()

  if (clearDesiredConnection) {
    shouldMaintainConnection = false
  }

  const socket = wsRef
  wsRef = null
  isConnecting = false

  invalidateSocket(socket)

  updateChatStore({
    connectionState: "disconnected",
    isTyping: false,
  })
}

export async function connectChat() {
  if (
    store.get(gatewayAtom).status !== "running" ||
    needsActiveSessionHydration()
  ) {
    return
  }

  if (
    isConnecting ||
    (wsRef &&
      (wsRef.readyState === WebSocket.OPEN ||
        wsRef.readyState === WebSocket.CONNECTING))
  ) {
    return
  }

  const generation = connectionGeneration + 1
  connectionGeneration = generation
  isConnecting = true
  clearReconnectTimer()
  updateChatStore({ connectionState: "connecting" })

  try {
    const sessionId = activeSessionIdRef

    if (generation !== connectionGeneration) {
      isConnecting = false
      return
    }

    const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:"
    const wsUrl = `${wsScheme}//${window.location.host}/pico/ws`
    const url = `${wsUrl}?session_id=${encodeURIComponent(sessionId)}`
    const socket = new WebSocket(url)

    if (generation !== connectionGeneration) {
      isConnecting = false
      invalidateSocket(socket)
      return
    }

    socket.onopen = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      updateChatStore({ connectionState: "connected" })
      isConnecting = false
      reconnectAttempts = 0
    }

    socket.onmessage = (event) => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }

      try {
        const message = JSON.parse(event.data) as PicoMessage
        handlePicoMessage(message, sessionId)
      } catch {
        console.warn("Non-JSON message from pico:", event.data)
      }
    }

    socket.onclose = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      wsRef = null
      isConnecting = false
      updateChatStore({
        connectionState: "disconnected",
        isTyping: false,
      })
      scheduleReconnect(generation, sessionId)
    }

    socket.onerror = () => {
      if (
        !isCurrentSocket({
          socket,
          currentSocket: wsRef,
          generation,
          currentGeneration: connectionGeneration,
          sessionId,
          currentSessionId: activeSessionIdRef,
        })
      ) {
        return
      }
      isConnecting = false
      updateChatStore({ connectionState: "error" })
      scheduleReconnect(generation, sessionId)
    }

    wsRef = socket
  } catch (error) {
    if (generation !== connectionGeneration) {
      isConnecting = false
      return
    }
    console.error("Failed to connect to pico:", error)
    updateChatStore({ connectionState: "error" })
    isConnecting = false
    scheduleReconnect(generation, activeSessionIdRef)
  }
}

export function disconnectChat() {
  disconnectChatInternal({ clearDesiredConnection: true })
}

export async function hydrateActiveSession() {
  if (hydratePromise) {
    return hydratePromise
  }

  const state = getChatState()
  const storedSessionId = readStoredSessionId()

  if (
    !storedSessionId ||
    state.hasHydratedActiveSession ||
    storedSessionId !== state.activeSessionId
  ) {
    if (!state.hasHydratedActiveSession) {
      updateChatStore({ hasHydratedActiveSession: true })
    }
    return
  }

  hydratePromise = loadSessionMessages(storedSessionId)
    .then((historyPage) => {
      const currentState = getChatState()
      if (currentState.activeSessionId !== storedSessionId) {
        return
      }

      if (currentState.messages.length > 0) {
        updateChatStore({
          messages: mergeHistoryMessages(
            historyPage.messages,
            currentState.messages,
          ),
          hasHydratedActiveSession: true,
          historyStart: historyPage.start,
          hasMoreHistory: historyPage.hasMore,
        })
        return
      }

      updateChatStore({
        messages: historyPage.messages,
        isTyping: false,
        hasHydratedActiveSession: true,
        historyStart: historyPage.start,
        hasMoreHistory: historyPage.hasMore,
      })
    })
    .catch((error) => {
      console.error("Failed to restore last session history:", error)

      const currentState = getChatState()
      if (currentState.activeSessionId !== storedSessionId) {
        return
      }

      if (currentState.messages.length > 0) {
        updateChatStore({ hasHydratedActiveSession: true })
        return
      }

      clearStoredSessionId()
      updateChatStore({
        messages: [],
        isTyping: false,
        hasHydratedActiveSession: true,
        historyStart: 0,
        hasMoreHistory: false,
      })
    })
    .finally(() => {
      hydratePromise = null
    })

  return hydratePromise
}

export async function loadOlderChatHistory(
  sessionId: string,
): Promise<boolean> {
  const state = getChatState()
  if (
    isLoadingOlderHistory ||
    sessionId !== state.activeSessionId ||
    !state.hasMoreHistory
  ) {
    return false
  }

  isLoadingOlderHistory = true
  try {
    const page = await loadSessionMessages(sessionId, state.historyStart)
    const currentState = getChatState()
    if (
      currentState.activeSessionId !== sessionId ||
      page.start >= currentState.historyStart
    ) {
      return false
    }

    updateChatStore((prev) => ({
      messages: [...page.messages, ...prev.messages],
      historyStart: page.start,
      hasMoreHistory: page.hasMore,
    }))
    return true
  } catch (error) {
    console.error("Failed to load older session messages:", error)
    return false
  } finally {
    isLoadingOlderHistory = false
  }
}

interface SendChatMessageInput {
  content: string
  attachments?: ChatAttachment[]
}

export function sendChatMessage({
  content,
  attachments = [],
}: SendChatMessageInput) {
  if (!wsRef || wsRef.readyState !== WebSocket.OPEN) {
    console.warn("WebSocket not connected")
    return false
  }

  const normalizedContent = content.trim()
  const normalizedAttachments = attachments
    .filter((attachment) => attachment.url)
    .map((attachment) => ({ ...attachment }))

  if (!normalizedContent && normalizedAttachments.length === 0) {
    return false
  }

  const socket = wsRef
  const id = `msg-${++msgIdCounter}-${Date.now()}`

  updateChatStore((prev) => ({
    messages: [
      ...prev.messages,
      {
        id,
        role: "user",
        content: normalizedContent,
        attachments:
          normalizedAttachments.length > 0 ? normalizedAttachments : undefined,
        timestamp: Date.now(),
      },
    ],
    isTyping: true,
  }))

  try {
    const payload: Record<string, unknown> = {
      content: normalizedContent,
      attachments: normalizedAttachments.map((attachment) => ({
        type: attachment.type,
        filename: attachment.filename,
        content_type: attachment.contentType,
        url: attachment.url,
      })),
    }

    socket.send(
      JSON.stringify({
        type: "message.send",
        id,
        payload,
      }),
    )
    return true
  } catch (error) {
    console.error("Failed to send pico message:", error)
    updateChatStore((prev) => ({
      messages: prev.messages.filter((message) => message.id !== id),
      isTyping: false,
    }))
    return false
  }
}

export async function switchChatSession(sessionId: string) {
  if (sessionId === activeSessionIdRef) {
    return
  }

  try {
    const historyPage = await loadSessionMessages(sessionId)
    const currentState = getChatState()
    const sharesSplitGroup = store
      .get(splitConversationsAtom)
      .some(
        (group) =>
          group.includes(currentState.activeSessionId) &&
          group.includes(sessionId),
      )
    if (sharesSplitGroup && currentState.hasHydratedActiveSession) {
      updateSplitSessionState(currentState.activeSessionId, {
        messages: currentState.messages,
        isTyping: currentState.isTyping,
        contextUsage: currentState.contextUsage,
      })
    }

    disconnectChatInternal({ clearDesiredConnection: false })
    setActiveSessionId(sessionId, {
      messages: historyPage.messages,
      isTyping: false,
      hasHydratedActiveSession: true,
      historyStart: historyPage.start,
      hasMoreHistory: historyPage.hasMore,
      contextUsage: undefined,
    })

    if (store.get(gatewayAtom).status === "running") {
      shouldMaintainConnection = true
      await connectChat()
    }
  } catch (error) {
    console.error("Failed to load session history:", error)
    toast.error(i18n.t("chat.historyOpenFailed"))
  }
}

export async function newChatSession(force = false) {
  if (!force && getChatState().messages.length === 0) {
    return undefined
  }

  disconnectChatInternal({ clearDesiredConnection: false })
  const sessionId = generateSessionId()
  setActiveSessionId(sessionId, {
    messages: [],
    isTyping: false,
    hasHydratedActiveSession: true,
    historyStart: 0,
    hasMoreHistory: false,
    contextUsage: undefined,
  })

  if (store.get(gatewayAtom).status === "running") {
    shouldMaintainConnection = true
    await connectChat()
  }
  return sessionId
}

export function initializeChatStore() {
  if (initialized) {
    return
  }

  initialized = true
  activeSessionIdRef = getChatState().activeSessionId
  let lastGatewayStatus: GatewayState | null = null

  const syncConnectionWithGateway = (force: boolean = false) => {
    const gatewayStatus = store.get(gatewayAtom).status
    syncSplitConnections()
    if (!force && gatewayStatus === lastGatewayStatus) {
      return
    }
    lastGatewayStatus = gatewayStatus

    if (gatewayStatus === "running") {
      shouldMaintainConnection = true
      if (needsActiveSessionHydration()) {
        return
      }
      void connectChat()
      return
    }

    if (gatewayStatus === "stopped" || gatewayStatus === "error") {
      disconnectChatInternal({ clearDesiredConnection: true })
    }
  }

  unsubscribeChat = store.sub(chatAtom, syncSplitConnections)
  unsubscribeSplitConversations = store.sub(
    splitConversationsAtom,
    syncSplitConnections,
  )
  unsubscribeGateway = store.sub(gatewayAtom, syncConnectionWithGateway)
  syncSplitConnections()

  if (!readStoredSessionId()) {
    updateChatStore({ hasHydratedActiveSession: true })
    syncConnectionWithGateway(true)
    return
  }

  void hydrateActiveSession().finally(() => {
    if (!initialized) {
      return
    }
    syncConnectionWithGateway(true)
  })
}

export function teardownChatStore() {
  unsubscribeGateway?.()
  unsubscribeGateway = null
  unsubscribeChat?.()
  unsubscribeChat = null
  unsubscribeSplitConversations?.()
  unsubscribeSplitConversations = null
  initialized = false
  lastSplitSyncKey = ""
  for (const sessionId of splitConnections.keys()) {
    closeSplitConnection(sessionId)
  }
  disconnectChat()
}
