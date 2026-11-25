import { describe, expect, it } from "vitest"

import { resolveComposerPrimaryAction } from "./composer-action"

describe("resolveComposerPrimaryAction", () => {
  it("shows send while the agent is running once there is input", () => {
    expect(
      resolveComposerPrimaryAction({
        isTyping: true,
        canInput: true,
        canSend: true,
      }),
    ).toBe("send")
  })

  it("shows stop while the agent is running with an empty composer", () => {
    expect(
      resolveComposerPrimaryAction({
        isTyping: true,
        canInput: true,
        canSend: false,
      }),
    ).toBe("stop")
  })

  it("keeps stop visible but disabled when input is unavailable", () => {
    expect(
      resolveComposerPrimaryAction({
        isTyping: true,
        canInput: false,
        canSend: false,
      }),
    ).toBe("stop")
  })

  it("shows send when idle and input is available", () => {
    expect(
      resolveComposerPrimaryAction({
        isTyping: false,
        canInput: true,
        canSend: false,
      }),
    ).toBe("send")
  })

  it("shows nothing when idle and input is unavailable", () => {
    expect(
      resolveComposerPrimaryAction({
        isTyping: false,
        canInput: false,
        canSend: false,
      }),
    ).toBe("none")
  })
})
