import { fireEvent, render, screen } from "@testing-library/react"
import { createRef } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ConversationNavigator } from "./conversation-navigator"

const SCROLLER_HEIGHT = 200
const SCROLLER_CONTENT_HEIGHT = 1_000
const TRACK_TOP = 100
const TRACK_HEIGHT = 300
const MESSAGE_HEIGHT = 100

function Message({
  index,
  role,
  kind = "normal",
  top,
}: {
  index: number
  role: "user" | "assistant"
  kind?: string
  top: number
}) {
  return (
    <div
      data-chat-index={index}
      data-chat-role={role}
      data-chat-kind={kind}
      data-message-top={top}
    >
      {role} message {index + 1}
    </div>
  )
}

function renderNavigator() {
  const scrollRef = createRef<HTMLDivElement>()
  render(
    <div className="relative h-full">
      <div ref={scrollRef} data-testid="conversation">
        <Message index={0} role="user" top={0} />
        <Message index={1} role="assistant" top={400} />
        <Message index={2} role="assistant" kind="tool_calls" top={500} />
        <Message index={3} role="user" top={800} />
      </div>
      <ConversationNavigator
        scrollRef={scrollRef}
        label="Navigate conversation"
      />
    </div>,
  )
  const scroller = screen.getByTestId("conversation") as HTMLDivElement
  const navigation = screen.getByRole("navigation", {
    name: "Navigate conversation",
  })
  const track = navigation.querySelector("[data-conversation-track]")
  if (!track) throw new Error("Conversation track missing")
  return { scroller, navigation, track }
}

describe("ConversationNavigator", () => {
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(
      function (this: HTMLElement) {
        if (this.dataset.testid === "conversation") return SCROLLER_HEIGHT
        return TRACK_HEIGHT
      },
    )
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(
      SCROLLER_CONTENT_HEIGHT,
    )
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
      function (this: HTMLElement) {
        const messageTop = Number(this.dataset.messageTop)
        const markerIndex = Number(this.dataset.markerIndex)
        const markerCount = Number(this.dataset.markerCount)
        const markerSpacing = 8
        const markerHeight = 8
        const markerStackHeight =
          (markerCount - 1) * markerSpacing + markerHeight
        const scroller = this.closest('[data-testid="conversation"]')
        const top = this.hasAttribute("data-conversation-track")
          ? TRACK_TOP
          : Number.isFinite(markerIndex)
            ? TRACK_TOP +
              (TRACK_HEIGHT - markerStackHeight) / 2 +
              markerIndex * markerSpacing
            : Number.isFinite(messageTop)
              ? messageTop - (scroller?.scrollTop ?? 0)
              : 0
        const height = this.hasAttribute("data-conversation-track")
          ? TRACK_HEIGHT
          : Number.isFinite(markerIndex)
            ? markerHeight
            : Number.isFinite(messageTop)
              ? MESSAGE_HEIGHT
              : SCROLLER_HEIGHT

        return {
          x: 0,
          y: top,
          top,
          left: 0,
          right: 20,
          bottom: top + height,
          width: 20,
          height,
          toJSON: () => ({}),
        }
      },
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("renders one line for each question and answer, excluding activity", () => {
    const { navigation, track } = renderNavigator()
    const firstMarker = navigation.querySelector("[data-message-marker]")

    expect(navigation.querySelectorAll("[data-message-marker]")).toHaveLength(3)
    expect(track).toHaveClass("justify-center")
    expect(firstMarker).toHaveClass("min-h-0")
    expect(firstMarker).not.toHaveClass("shrink-0")
    expect(firstMarker).not.toHaveAttribute("style")
    expect(
      screen.getByRole("button", {
        name: "Navigate conversation 2: assistant message 2",
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: /assistant message 3/ }),
    ).not.toBeInTheDocument()
  })

  it("keeps inactive lines equal and emphasizes only the current message", () => {
    const { navigation } = renderNavigator()
    const markers = Array.from(
      navigation.querySelectorAll<HTMLElement>("[data-message-marker]"),
    )
    const currentLine = markers[0].querySelector("span")

    expect(currentLine).toHaveClass("h-0.5", "w-5", "bg-foreground")
    markers.slice(1).forEach((marker) => {
      const line = marker.querySelector("span")
      expect(line).toHaveClass("h-0.5", "w-3.5")
      expect(line).not.toHaveClass("w-5", "bg-foreground")
    })
  })

  it("jumps directly to the selected message", () => {
    const { scroller } = renderNavigator()

    fireEvent.click(
      screen.getByRole("button", {
        name: "Navigate conversation 2: assistant message 2",
      }),
    )

    expect(scroller.scrollTop).toBe(350)
  })

  it("jumps to the nearest message when the rail is clicked", () => {
    const { scroller, track } = renderNavigator()

    fireEvent.click(track, { clientY: TRACK_TOP + TRACK_HEIGHT / 2 + 14 })

    expect(scroller.scrollTop).toBe(750)
  })

  it("marks the message closest to the viewport center as current", () => {
    const { scroller } = renderNavigator()
    const firstMessage = screen.getByRole("button", {
      name: "Navigate conversation 1: user message 1",
    })
    const secondMessage = screen.getByRole("button", {
      name: "Navigate conversation 2: assistant message 2",
    })

    expect(firstMessage).toHaveAttribute("aria-current", "true")
    scroller.scrollTop = 400
    fireEvent.scroll(scroller)

    expect(secondMessage).toHaveAttribute("aria-current", "true")
    expect(firstMessage).not.toHaveAttribute("aria-current")
  })

  it("keeps the jumped-to message selected until manual scrolling resumes", () => {
    const { scroller } = renderNavigator()
    const firstMessage = screen.getByRole("button", {
      name: "Navigate conversation 1: user message 1",
    })
    const secondMessage = screen.getByRole("button", {
      name: "Navigate conversation 2: assistant message 2",
    })

    fireEvent.click(firstMessage)
    scroller.scrollTop = 400
    fireEvent.scroll(scroller)

    expect(firstMessage).toHaveAttribute("aria-current", "true")
    fireEvent.wheel(scroller)
    expect(secondMessage).toHaveAttribute("aria-current", "true")
  })

  it("stays hidden when the conversation fits in the viewport", () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(
      SCROLLER_HEIGHT,
    )
    const scrollRef = createRef<HTMLDivElement>()

    render(
      <div className="relative h-full">
        <div ref={scrollRef} data-testid="conversation">
          <Message index={0} role="user" top={0} />
        </div>
        <ConversationNavigator
          scrollRef={scrollRef}
          label="Navigate conversation"
        />
      </div>,
    )

    expect(
      screen.queryByRole("navigation", { name: "Navigate conversation" }),
    ).not.toBeInTheDocument()
  })
})
