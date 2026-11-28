import { getDefaultStore } from "jotai"
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import type { ComponentProps, ReactNode } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import type { ChatComposer } from "@/components/chat/chat-composer"
import type { ApprovalMessage } from "@/components/chat/approval-message"
import type { AssistantMessage } from "@/components/chat/assistant-message"
import type { UserMessage } from "@/components/chat/user-message"
import {
  loadOlderChatHistory,
  newChatSession,
  sendChatMessage,
  switchChatSession,
} from "@/features/chat/controller"
import { loadSessionMessages } from "@/features/chat/history"
import { buildChatAttachments } from "@/features/chat/image-input"
import { useChatModels } from "@/hooks/use-chat-models"
import { useGateway } from "@/hooks/use-gateway"
import {
  chatAtom,
  type ChatStoreState,
  composerFocusSignalAtom,
  composerInsertionAtom,
  pendingScrollTargetAtom,
  sessionRunningAtom,
  sessionTitlesAtom,
  splitConversationsAtom,
  splitLayoutSizesAtom,
  splitLayoutsAtom,
  splitSessionStatesAtom,
} from "@/store/chat"

import { ChatPage } from "./chat-page"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }))
vi.mock("@/api/sessions", () => ({
  getSessionHistory: vi.fn().mockResolvedValue({
    id: "session-a",
    start: 0,
    total: 0,
    messages: [],
    summary: "",
    created: "2026-01-01T00:00:00Z",
    updated: "2026-01-01T00:00:00Z",
  }),
  forkSession: vi.fn(),
}))
vi.mock("@/features/chat/controller", () => ({
  sendChatMessage: vi.fn(),
  switchChatSession: vi.fn(),
  loadOlderChatHistory: vi.fn(),
  newChatSession: vi.fn(),
}))
vi.mock("@/features/chat/history", () => ({
  loadSessionMessages: vi.fn(),
}))
vi.mock("@/features/chat/image-input", () => ({
  CHAT_ATTACHMENT_ACCEPT: "image/*,audio/*,.pdf",
  buildChatAttachments: vi.fn(),
  getTransferredFiles: vi.fn(() => []),
  hasFileTransfer: vi.fn(() => false),
}))
vi.mock("@/hooks/use-chat-models", () => ({ useChatModels: vi.fn() }))
vi.mock("@/hooks/use-gateway", () => ({ useGateway: vi.fn() }))
vi.mock("@/hooks/use-slash-commands", () => ({
  useSlashCommands: () => [],
}))
vi.mock("@/components/page-header", () => ({
  PageHeader: ({ children }: { children: ReactNode }) => <header>{children}</header>,
}))
vi.mock("@/components/chat/chat-empty-state", () => ({
  ChatEmptyState: () => <div>chat empty</div>,
}))
vi.mock("@/components/chat/typing-indicator", () => ({
  TypingIndicator: () => <div>typing</div>,
}))
vi.mock("@/components/chat/model-selector", () => ({
  ModelSelector: () => null,
}))
vi.mock("@/components/chat/file-preview-panel", () => ({
  FilePreviewPanel: () => <div data-testid="file-preview" />,
}))
vi.mock("@/components/chat/chat-composer", () => ({
  ChatComposer: (props: ComponentProps<typeof ChatComposer>) => (
    <div>
      <textarea
        aria-label="composer"
        value={props.input}
        onChange={(event) => props.onInputChange(event.target.value)}
      />
      <button type="button" onClick={props.onSend}>
        send
      </button>
      <button type="button" onClick={props.onStop}>
        stop
      </button>
      <button
        type="button"
        onClick={() =>
          props.onSendAudio({
            type: "audio",
            url: "media://voice.webm",
            filename: "voice.webm",
          })
        }
      >
        send audio
      </button>
      {props.attachments.map((attachment, index) => (
        <button
          type="button"
          key={`${attachment.url}-${index}`}
          aria-label={`attachment-${attachment.filename ?? index}`}
          onClick={() => props.onRemoveAttachment(index)}
        >
          {attachment.filename}
        </button>
      ))}
      {props.pendingFollowUps.map((followUp) => (
        <div key={followUp.id} data-testid="pending-follow-up">
          <span>{followUp.content}</span>
          <button type="button" onClick={() => props.onSteerFollowUp(followUp.id)}>
            steer
          </button>
          <button
            type="button"
            onClick={() => props.onRemoveFollowUp(followUp.id)}
          >
            remove
          </button>
        </div>
      ))}
    </div>
  ),
}))
vi.mock("@/components/chat/user-message", () => ({
  UserMessage: (props: ComponentProps<typeof UserMessage>) => (
    <div data-testid="user-message">
      <span>{props.content}</span>
      {props.attachments?.map((attachment) => (
        <span key={attachment.url}>{attachment.filename}</span>
      ))}
    </div>
  ),
}))
vi.mock("@/components/chat/assistant-message", () => ({
  AssistantMessage: (props: ComponentProps<typeof AssistantMessage>) => (
    <div data-testid="assistant-message">
      <span>{props.content}</span>
      {props.onFork && (
        <button type="button" onClick={props.onFork}>
          fork
        </button>
      )}
    </div>
  ),
}))
vi.mock("@/components/chat/approval-message", () => ({
  ApprovalMessage: (props: ComponentProps<typeof ApprovalMessage>) => (
    <div data-testid="approval-message">
      <button type="button" onClick={props.onApprove}>
        approve
      </button>
      <button type="button" onClick={props.onDeny}>
        deny
      </button>
    </div>
  ),
}))

const store = getDefaultStore()
const sendMock = vi.mocked(sendChatMessage)
const switchMock = vi.mocked(switchChatSession)
const loadOlderMock = vi.mocked(loadOlderChatHistory)
const newChatMock = vi.mocked(newChatSession)
const loadSplitMock = vi.mocked(loadSessionMessages)
const buildAttachmentsMock = vi.mocked(buildChatAttachments)

function setChatState(patch: Partial<ChatStoreState>) {
  store.set(chatAtom, (current) => ({ ...current, ...patch }))
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
    callback(0)
    return 1
  })
  store.set(chatAtom, {
    messages: [],
    connectionState: "connected",
    isTyping: false,
    activeSessionId: "session-a",
    hasHydratedActiveSession: true,
    historyStart: 0,
    hasMoreHistory: false,
  })
  store.set(splitConversationsAtom, [])
  store.set(splitLayoutsAtom, {})
  store.set(splitLayoutSizesAtom, {})
  store.set(splitSessionStatesAtom, {})
  store.set(sessionTitlesAtom, {})
  store.set(sessionRunningAtom, {})
  store.set(composerFocusSignalAtom, 0)
  store.set(composerInsertionAtom, null)
  store.set(pendingScrollTargetAtom, null)
  globalThis.localStorage.clear()

  sendMock.mockReturnValue(true)
  switchMock.mockResolvedValue(undefined)
  loadOlderMock.mockResolvedValue(true)
  newChatMock.mockResolvedValue("session-b")
  loadSplitMock.mockResolvedValue({ messages: [], start: 0, hasMore: false })
  buildAttachmentsMock.mockResolvedValue([
    {
      type: "file",
      url: "data:text/plain;base64,aGVsbG8=",
      filename: "note.txt",
      contentType: "text/plain",
    },
  ])
  vi.mocked(useGateway).mockReturnValue({
    state: "running",
    loading: false,
    canStart: false,
    startReason: undefined,
    restartRequired: false,
    start: vi.fn(),
    stop: vi.fn(),
    restart: vi.fn(),
    error: null,
  })
  vi.mocked(useChatModels).mockReturnValue({
    defaultModelName: "default",
    hasAvailableModels: true,
    apiKeyModels: [],
    oauthModels: [],
    localModels: [],
    settingDefault: false,
    handleSetDefault: vi.fn(),
  })
})

function renderPage() {
  return render(<ChatPage />)
}

function typeMessage(content: string) {
  fireEvent.change(screen.getByRole("textbox", { name: "composer" }), {
    target: { value: content },
  })
}

describe("ChatPage orchestration", () => {
  it("routes /clear through the controller and resets the composer", () => {
    setChatState({
      messages: [
        { id: "u1", role: "user", content: "old", timestamp: Date.now() },
      ],
    })
    renderPage()

    typeMessage("/clear")
    fireEvent.click(screen.getByRole("button", { name: "send" }))

    expect(sendMock).toHaveBeenCalledWith({
      content: "/clear",
      attachments: [],
    })
    expect(screen.getByRole("textbox", { name: "composer" })).toHaveValue("")
  })

  it("queues follow-ups while running and drains them when idle", async () => {
    setChatState({ isTyping: true })
    renderPage()

    typeMessage("queued follow-up")
    fireEvent.click(screen.getByRole("button", { name: "send" }))

    expect(sendMock).not.toHaveBeenCalled()
    expect(screen.getByTestId("pending-follow-up")).toHaveTextContent(
      "queued follow-up",
    )

    act(() => setChatState({ isTyping: false }))

    await waitFor(() =>
      expect(sendMock).toHaveBeenCalledWith({
        content: "queued follow-up",
        attachments: [],
      }),
    )
    expect(screen.queryByTestId("pending-follow-up")).not.toBeInTheDocument()
  })

  it("sends approval replies immediately instead of queueing them", () => {
    setChatState({
      isTyping: true,
      messages: [
        {
          id: "approval-1",
          role: "assistant",
          content: "",
          kind: "approval",
          timestamp: Date.now(),
          approval: { token: "approval-token", command: "rm -rf /tmp/x" },
        },
      ],
    })
    renderPage()

    fireEvent.click(screen.getByRole("button", { name: "approve" }))

    expect(sendMock).toHaveBeenCalledWith({
      content: "approve approval-token",
      attachments: [],
    })
    expect(screen.queryByTestId("pending-follow-up")).not.toBeInTheDocument()
  })

  it("adds file attachments and sends them with the message", async () => {
    renderPage()
    const input = document.querySelector(
      'input[type="file"]',
    ) as HTMLInputElement
    const file = new File(["hello"], "note.txt", { type: "text/plain" })

    fireEvent.change(input, { target: { files: [file] } })
    expect(
      await screen.findByRole("button", { name: "attachment-note.txt" }),
    ).toBeInTheDocument()
    typeMessage("read this")
    fireEvent.click(screen.getByRole("button", { name: "send" }))

    expect(buildAttachmentsMock).toHaveBeenCalledWith([file], expect.any(Function))
    expect(sendMock).toHaveBeenCalledWith({
      content: "read this",
      attachments: [
        {
          type: "file",
          url: "data:text/plain;base64,aGVsbG8=",
          filename: "note.txt",
          contentType: "text/plain",
        },
      ],
    })
    expect(
      screen.queryByRole("button", { name: "attachment-note.txt" }),
    ).not.toBeInTheDocument()
  })

  it("loads older history when the active transcript scrolls to the top", async () => {
    setChatState({
      historyStart: 50,
      hasMoreHistory: true,
      messages: [
        { id: "u1", role: "user", content: "old", timestamp: Date.now() },
      ],
    })
    const { container } = renderPage()
    const scroller = container.querySelector(
      '[data-session-scroll="session-a"]',
    ) as HTMLDivElement
    Object.defineProperty(scroller, "scrollTop", {
      configurable: true,
      writable: true,
      value: 0,
    })

    fireEvent.scroll(scroller)

    await waitFor(() => expect(loadOlderMock).toHaveBeenCalledWith("session-a"))
  })

  it("adds, activates, and removes split panes", async () => {
    const { container } = renderPage()
    fireEvent.click(screen.getByRole("button", { name: "chat.addSplitPane" }))

    await waitFor(() =>
      expect(container.querySelectorAll("section[aria-label]")).toHaveLength(2),
    )
    expect(newChatMock).toHaveBeenCalledWith(true)
    const panes = Array.from(
      container.querySelectorAll<HTMLElement>("section[aria-label]"),
    )
    const secondPane = panes.find((pane) =>
      pane.getAttribute("aria-label")?.includes("2"),
    )
    expect(secondPane).toBeTruthy()
    fireEvent.click(secondPane!)
    expect(switchMock).toHaveBeenCalledWith("session-b")

    fireEvent.click(
      within(secondPane!).getByRole("button", {
        name: "chat.removeSplitPane",
      }),
    )
    await waitFor(() =>
      expect(container.querySelector("section[aria-label]")).toBeNull(),
    )
    expect(
      container.querySelector('[data-session-scroll="session-a"]'),
    ).not.toBeNull()
  })
})
