import { fireEvent, render, screen } from "@testing-library/react"
import { createRef } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { ScrollToBottomButton } from "./scroll-to-bottom-button"

const SCROLLER_HEIGHT = 200
const SCROLLER_CONTENT_HEIGHT = 1_000
const BOTTOM_SCROLL_TOP = SCROLLER_CONTENT_HEIGHT - SCROLLER_HEIGHT

function renderButton() {
  const scrollRef = createRef<HTMLDivElement>()
  render(
    <div className="relative h-full">
      <div ref={scrollRef} data-testid="conversation">
        <div>message</div>
      </div>
      <ScrollToBottomButton scrollRef={scrollRef} label="Scroll to bottom" />
    </div>,
  )
  const scroller = screen.getByTestId("conversation") as HTMLDivElement
  // jsdom does not implement Element.scrollTo, so the component falls back to
  // assigning scrollTop. Make that deterministic even if a polyfill appears.
  Object.defineProperty(scroller, "scrollTo", {
    configurable: true,
    value: undefined,
  })
  return { scroller }
}

describe("ScrollToBottomButton", () => {
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(
      function (this: HTMLElement) {
        if (this.dataset.testid === "conversation") return SCROLLER_HEIGHT
        return 0
      },
    )
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(
      function (this: HTMLElement) {
        if (this.dataset.testid === "conversation") {
          return SCROLLER_CONTENT_HEIGHT
        }
        return 0
      },
    )
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("appears when the conversation is scrolled up", () => {
    const { scroller } = renderButton()

    expect(
      screen.getByRole("button", { name: "Scroll to bottom" }),
    ).toBeInTheDocument()

    scroller.scrollTop = BOTTOM_SCROLL_TOP
    fireEvent.scroll(scroller)

    expect(
      screen.queryByRole("button", { name: "Scroll to bottom" }),
    ).not.toBeInTheDocument()
  })

  it("stays hidden when the conversation fits in the viewport", () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(
      function (this: HTMLElement) {
        if (this.dataset.testid === "conversation") return SCROLLER_HEIGHT
        return 0
      },
    )

    renderButton()

    expect(
      screen.queryByRole("button", { name: "Scroll to bottom" }),
    ).not.toBeInTheDocument()
  })

  it("scrolls back to the latest message when clicked", () => {
    const { scroller } = renderButton()
    scroller.scrollTop = 400
    fireEvent.scroll(scroller)

    fireEvent.click(screen.getByRole("button", { name: "Scroll to bottom" }))

    expect(scroller.scrollTop).toBe(SCROLLER_CONTENT_HEIGHT)
  })
})
