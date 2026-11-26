import { afterEach, describe, expect, it, vi } from "vitest"

import {
  AudioAttachmentTooLargeError,
  MAX_AUDIO_ATTACHMENT_BYTES,
  audioExtensionForMimeType,
  audioRecordingErrorKind,
  audioRecordingNeedsSecureContext,
  buildAudioAttachment,
  formatRecordingDuration,
  isAudioRecordingSupported,
  normalizeAudioMimeType,
  pickAudioRecordingMimeType,
} from "./audio-recorder"

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("audio recording capability", () => {
  it("requires MediaRecorder and getUserMedia", () => {
    vi.stubGlobal("MediaRecorder", class {})
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn() } })
    expect(isAudioRecordingSupported()).toBe(true)

    vi.stubGlobal("navigator", { mediaDevices: {} })
    expect(isAudioRecordingSupported()).toBe(false)
  })

  it("detects insecure browser contexts", () => {
    vi.stubGlobal("MediaRecorder", class {})
    vi.stubGlobal("isSecureContext", false)
    expect(audioRecordingNeedsSecureContext()).toBe(true)

    vi.stubGlobal("isSecureContext", true)
    expect(audioRecordingNeedsSecureContext()).toBe(false)
  })

  it("picks the first supported preferred MIME type", () => {
    const isTypeSupported = vi.fn((mime: string) => mime === "audio/ogg")
    vi.stubGlobal("MediaRecorder", { isTypeSupported })

    expect(pickAudioRecordingMimeType()).toBe("audio/ogg")
    expect(isTypeSupported).toHaveBeenCalledWith("audio/webm;codecs=opus")
    expect(isTypeSupported).toHaveBeenCalledWith("audio/ogg")
  })

  it("returns an empty MIME type when no preference is supported", () => {
    vi.stubGlobal("MediaRecorder", { isTypeSupported: () => false })
    expect(pickAudioRecordingMimeType()).toBe("")
  })
})

describe("audio formatting", () => {
  it.each([
    ["audio/webm;codecs=opus", "audio/webm", ".webm"],
    ["audio/ogg", "audio/ogg", ".ogg"],
    ["audio/mp4", "audio/mp4", ".m4a"],
    ["audio/x-m4a", "audio/x-m4a", ".m4a"],
    ["audio/mpeg", "audio/mpeg", ".mp3"],
    ["audio/wav", "audio/wav", ".wav"],
    ["audio/flac", "audio/flac", ".flac"],
    ["application/octet-stream", "application/octet-stream", ".webm"],
    [undefined, "audio/webm", ".webm"],
  ])("normalizes %j", (input, normalized, extension) => {
    expect(normalizeAudioMimeType(input)).toBe(normalized)
    expect(audioExtensionForMimeType(input ?? "")).toBe(extension)
  })

  it("formats non-negative durations", () => {
    expect(formatRecordingDuration(-1000)).toBe("0:00")
    expect(formatRecordingDuration(0)).toBe("0:00")
    expect(formatRecordingDuration(65_999)).toBe("1:05")
  })

  it.each([
    ["NotAllowedError", "permissionDenied"],
    ["SecurityError", "permissionDenied"],
    ["NotFoundError", "unavailable"],
    ["DevicesNotFoundError", "unavailable"],
    ["NotSupportedError", "unsupported"],
    ["AbortError", "failed"],
  ])("classifies %s", (name, expected) => {
    const error = new Error(name)
    error.name = name
    expect(audioRecordingErrorKind(error)).toBe(expected)
  })

  it("classifies non-errors as generic failures", () => {
    expect(audioRecordingErrorKind("failed")).toBe("failed")
  })
})

describe("buildAudioAttachment", () => {
  it("normalizes the blob MIME type and reads it as a data URL", async () => {
    const attachment = await buildAudioAttachment({
      blob: new Blob(["audio"], { type: "audio/webm;codecs=opus" }),
      mimeType: "audio/webm;codecs=opus",
      durationMs: 1000,
    })

    expect(attachment).toMatchObject({
      type: "audio",
      url: "data:audio/webm;base64,YXVkaW8=",
      contentType: "audio/webm",
    })
    expect(attachment.filename).toMatch(/^voice-\d+\.webm$/)
  })

  it("rejects oversized audio before reading it", async () => {
    const blob = {
      size: MAX_AUDIO_ATTACHMENT_BYTES + 1,
      type: "audio/webm",
    } as Blob

    await expect(
      buildAudioAttachment({ blob, mimeType: "audio/webm", durationMs: 1 }),
    ).rejects.toBeInstanceOf(AudioAttachmentTooLargeError)
  })

  it("propagates FileReader failures", async () => {
    vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(
      function (this: FileReader) {
        queueMicrotask(() =>
          this.onerror?.(
            new ProgressEvent("error") as ProgressEvent<FileReader>,
          ),
        )
      },
    )

    await expect(
      buildAudioAttachment({
        blob: new Blob(["audio"], { type: "audio/webm" }),
        mimeType: "audio/webm",
        durationMs: 1,
      }),
    ).rejects.toThrow("Failed to read audio")
  })

  it("rejects non-string FileReader results", async () => {
    vi.spyOn(FileReader.prototype, "result", "get").mockReturnValue(
      new ArrayBuffer(1),
    )
    vi.spyOn(FileReader.prototype, "readAsDataURL").mockImplementation(
      function (this: FileReader) {
        queueMicrotask(() =>
          this.onload?.(new ProgressEvent("load") as ProgressEvent<FileReader>),
        )
      },
    )

    await expect(
      buildAudioAttachment({
        blob: new Blob(["audio"], { type: "audio/webm" }),
        mimeType: "audio/webm",
        durationMs: 1,
      }),
    ).rejects.toThrow("Failed to read audio")
  })
})
