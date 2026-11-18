import type { ChatAttachment } from "@/store/chat"

export const MAX_AUDIO_RECORDING_MS = 5 * 60 * 1000
export const MAX_AUDIO_ATTACHMENT_BYTES = 15 * 1024 * 1024

// Opus in WebM covers Chromium and Firefox; MP4 covers Safari. The bare
// entries let MediaRecorder pick its default codec when a preference is not
// supported.
const PREFERRED_AUDIO_MIME_TYPES = [
  "audio/webm;codecs=opus",
  "audio/webm",
  "audio/ogg;codecs=opus",
  "audio/ogg",
  "audio/mp4",
  "audio/mpeg",
] as const

export interface RecordedAudio {
  blob: Blob
  mimeType: string
  durationMs: number
}

export class AudioAttachmentTooLargeError extends Error {
  constructor() {
    super("audio attachment exceeds the size limit")
    this.name = "AudioAttachmentTooLargeError"
  }
}

export function isAudioRecordingSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.MediaRecorder !== "undefined" &&
    typeof navigator !== "undefined" &&
    typeof navigator.mediaDevices?.getUserMedia === "function"
  )
}

// Browsers only expose getUserMedia in secure contexts. The dashboard is often
// opened over plain HTTP on a LAN address, where the API is missing entirely.
export function audioRecordingNeedsSecureContext(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.MediaRecorder !== "undefined" &&
    !window.isSecureContext
  )
}

// Returns an empty string when MediaRecorder supports no preferred MIME type:
// the browser default is still usable.
export function pickAudioRecordingMimeType(): string {
  if (typeof MediaRecorder === "undefined") return ""
  for (const mimeType of PREFERRED_AUDIO_MIME_TYPES) {
    if (MediaRecorder.isTypeSupported(mimeType)) return mimeType
  }
  return ""
}

export function normalizeAudioMimeType(mimeType: string | undefined): string {
  const base = (mimeType ?? "").split(";")[0]?.trim().toLowerCase()
  return base || "audio/webm"
}

export function audioExtensionForMimeType(mimeType: string): string {
  switch (normalizeAudioMimeType(mimeType)) {
    case "audio/ogg":
      return ".ogg"
    case "audio/mp4":
    case "audio/x-m4a":
      return ".m4a"
    case "audio/mpeg":
      return ".mp3"
    case "audio/wav":
    case "audio/x-wav":
      return ".wav"
    case "audio/flac":
      return ".flac"
    case "audio/aac":
      return ".aac"
    default:
      return ".webm"
  }
}

export function formatRecordingDuration(durationMs: number): string {
  const totalSeconds = Math.max(0, Math.floor(durationMs / 1000))
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${String(seconds).padStart(2, "0")}`
}

export type AudioRecordingErrorKind =
  "permissionDenied" | "unavailable" | "unsupported" | "failed"

export function audioRecordingErrorKind(
  error: unknown,
): AudioRecordingErrorKind {
  const name = error instanceof Error ? error.name : ""
  switch (name) {
    case "NotAllowedError":
    case "SecurityError":
      return "permissionDenied"
    case "NotFoundError":
    case "DevicesNotFoundError":
      return "unavailable"
    case "NotSupportedError":
      return "unsupported"
    default:
      return "failed"
  }
}

function readBlobAsDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === "string") {
        resolve(reader.result)
        return
      }
      reject(new Error("Failed to read audio"))
    }
    reader.onerror = () =>
      reject(reader.error || new Error("Failed to read audio"))
    reader.readAsDataURL(blob)
  })
}

export async function buildAudioAttachment(
  audio: RecordedAudio,
): Promise<ChatAttachment> {
  if (audio.blob.size > MAX_AUDIO_ATTACHMENT_BYTES) {
    throw new AudioAttachmentTooLargeError()
  }

  const mimeType = normalizeAudioMimeType(audio.mimeType || audio.blob.type)
  const normalizedBlob =
    audio.blob.type === mimeType
      ? audio.blob
      : audio.blob.slice(0, audio.blob.size, mimeType)
  const extension = audioExtensionForMimeType(mimeType)

  return {
    type: "audio",
    url: await readBlobAsDataUrl(normalizedBlob),
    filename: `voice-${Date.now()}${extension}`,
    contentType: mimeType,
  }
}
