import type { TFunction } from "i18next"
import { toast } from "sonner"
import { afterEach, describe, expect, it, vi } from "vitest"

import {
  CHAT_ATTACHMENT_ACCEPT,
  buildChatAttachments,
  getTransferredFiles,
  hasFileTransfer,
} from "./image-input"

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }))

const t = ((key: string, options?: { name?: string }) =>
  options?.name ? `${key}:${options.name}` : key) as unknown as TFunction

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.mocked(toast.error).mockClear()
})

describe("buildChatAttachments", () => {
  it("accepts supported image, text, and PDF files", async () => {
    const attachments = await buildChatAttachments(
      [
        new File(["image"], "photo.PNG", { type: "" }),
        new File(["package main"], "main.go", { type: "" }),
        new File(["pdf"], "report.pdf", { type: "application/pdf" }),
      ],
      t,
    )

    expect(attachments).toHaveLength(3)
    expect(attachments[0]).toMatchObject({
      type: "image",
      filename: "photo.PNG",
      contentType: "image/png",
    })
    expect(attachments[0].url).toMatch(/^data:image\/png;base64,/)
    expect(attachments[1]).toMatchObject({
      type: "file",
      filename: "main.go",
      contentType: "text/x-go",
    })
    expect(attachments[2]).toMatchObject({
      type: "file",
      filename: "report.pdf",
      contentType: "application/pdf",
    })
    expect(toast.error).not.toHaveBeenCalled()
  })

  it("skips unsupported files and continues with valid ones", async () => {
    const attachments = await buildChatAttachments(
      [
        new File(["binary"], "program.exe", {
          type: "application/octet-stream",
        }),
        new File(["text"], "note.txt", { type: "text/plain" }),
      ],
      t,
    )

    expect(attachments).toHaveLength(1)
    expect(attachments[0].filename).toBe("note.txt")
    expect(toast.error).toHaveBeenCalledWith("chat.invalidFile:program.exe")
  })

  it("enforces separate text and binary size limits", async () => {
    const oversizedText = {
      name: "large.txt",
      type: "text/plain",
      size: 1024 * 1024 + 1,
    } as File
    const oversizedImage = {
      name: "large.png",
      type: "image/png",
      size: 7 * 1024 * 1024 + 1,
    } as File

    const attachments = await buildChatAttachments(
      [oversizedText, oversizedImage],
      t,
    )

    expect(attachments).toEqual([])
    expect(toast.error).toHaveBeenCalledWith("chat.fileTooLarge:large.txt")
    expect(toast.error).toHaveBeenCalledWith("chat.fileTooLarge:large.png")
  })

  it("reports FileReader failures without aborting the batch", async () => {
    class FailingFileReader {
      onload: (() => void) | null = null
      onerror: (() => void) | null = null
      result: string | ArrayBuffer | null = null
      error: DOMException | null = null

      readAsDataURL() {
        queueMicrotask(() => {
          this.error = new DOMException("read failed", "NotReadableError")
          this.onerror?.()
        })
      }
    }
    vi.stubGlobal("FileReader", FailingFileReader)

    const attachments = await buildChatAttachments(
      [new File(["text"], "note.txt", { type: "text/plain" })],
      t,
    )

    expect(attachments).toEqual([])
    expect(toast.error).toHaveBeenCalledWith("chat.fileReadFailed:note.txt")
  })

  it("generates fallback filenames for unnamed PDFs", async () => {
    const file = new File(["pdf"], "", { type: "application/pdf" })
    const attachments = await buildChatAttachments([file], t)

    expect(attachments[0].filename).toBe("attachment-1.pdf")
  })

  it("advertises supported attachment types", () => {
    expect(CHAT_ATTACHMENT_ACCEPT).toContain("image/webp")
    expect(CHAT_ATTACHMENT_ACCEPT).toContain(".pdf")
    expect(CHAT_ATTACHMENT_ACCEPT).toContain(".go")
  })
})

describe("file transfer helpers", () => {
  it("handles null transfers", () => {
    expect(hasFileTransfer(null)).toBe(false)
    expect(getTransferredFiles(null)).toEqual([])
  })

  it("prefers the files list", () => {
    const file = new File(["text"], "note.txt", { type: "text/plain" })
    const transfer = {
      files: [file],
      items: [{ kind: "file", getAsFile: () => null }],
    } as unknown as DataTransfer

    expect(hasFileTransfer(transfer)).toBe(true)
    expect(getTransferredFiles(transfer)).toEqual([file])
  })

  it("falls back to file items when the files list is empty", () => {
    const file = new File(["text"], "note.txt", { type: "text/plain" })
    const transfer = {
      files: [],
      items: [
        { kind: "text", getAsFile: () => null },
        { kind: "file", getAsFile: () => file },
        { kind: "file", getAsFile: () => null },
      ],
    } as unknown as DataTransfer

    expect(hasFileTransfer(transfer)).toBe(true)
    expect(getTransferredFiles(transfer)).toEqual([file])
  })
})
