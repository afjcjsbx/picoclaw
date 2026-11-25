import { describe, expect, it } from "vitest"

import { isExecApprovalReply } from "./approval"

describe("isExecApprovalReply", () => {
  it("accepts short approve and deny replies with an optional slash", () => {
    expect(isExecApprovalReply("approve")).toBe(true)
    expect(isExecApprovalReply("DENY")).toBe(true)
    expect(isExecApprovalReply("/approve")).toBe(true)
    expect(isExecApprovalReply("  /deny  ")).toBe(true)
  })

  it("accepts a tokenized reply, including a leading mention", () => {
    expect(
      isExecApprovalReply("approve 0123456789abcdef0123456789abcdef"),
    ).toBe(true)
    expect(
      isExecApprovalReply("@picoclaw deny 0123456789abcdef0123456789abcdef"),
    ).toBe(true)
  })

  it("rejects ordinary chat and malformed tokens", () => {
    expect(isExecApprovalReply("")).toBe(false)
    expect(isExecApprovalReply("approve later")).toBe(false)
    expect(isExecApprovalReply("approve 1234")).toBe(false)
    expect(isExecApprovalReply("please approve")).toBe(false)
    expect(
      isExecApprovalReply("approve 0123456789abcdef0123456789abcdeZ"),
    ).toBe(false)
  })
})
