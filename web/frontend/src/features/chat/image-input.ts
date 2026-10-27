import type { TFunction } from "i18next"
import { toast } from "sonner"

import type { ChatAttachment } from "@/store/chat"

const CHAT_IMAGE_MIME_TYPES = [
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
  "image/bmp",
] as const

const CHAT_IMAGE_MIME_TYPE_SET = new Set<string>(CHAT_IMAGE_MIME_TYPES)
const CHAT_IMAGE_EXTENSION_BY_MIME: Record<string, string> = {
  "image/jpeg": ".jpg",
  "image/png": ".png",
  "image/gif": ".gif",
  "image/webp": ".webp",
  "image/bmp": ".bmp",
}
const CHAT_IMAGE_MIME_BY_EXTENSION: Record<string, string> = {
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".png": "image/png",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".bmp": "image/bmp",
}

const CHAT_TEXT_FILE_TYPES: Record<string, string> = {
  ".txt": "text/plain",
  ".md": "text/markdown",
  ".markdown": "text/markdown",
  ".yaml": "application/yaml",
  ".yml": "application/yaml",
  ".json": "application/json",
  ".csv": "text/csv",
  ".tsv": "text/tab-separated-values",
  ".xml": "application/xml",
  ".html": "text/html",
  ".log": "text/plain",
  ".toml": "application/toml",
  ".ini": "text/plain",
  ".py": "text/x-python",
  ".js": "text/javascript",
  ".ts": "text/typescript",
  ".go": "text/x-go",
  ".sh": "text/x-shellscript",
  ".sql": "application/sql",
  ".css": "text/css",
  ".rst": "text/plain",
  ".conf": "text/plain",
  ".env": "text/plain",
  ".properties": "text/plain",
  ".c": "text/x-c",
  ".cpp": "text/x-c++src",
  ".h": "text/x-c",
  ".java": "text/x-java-source",
  ".rs": "text/x-rust",
  ".rb": "text/x-ruby",
  ".php": "text/x-php",
  ".tf": "text/plain",
  ".proto": "text/plain",
}

const CHAT_DOCUMENT_ACCEPT = [
  "application/pdf",
  ".pdf",
  ...Object.keys(CHAT_TEXT_FILE_TYPES),
].join(",")

export const CHAT_ATTACHMENT_ACCEPT = [
  ...CHAT_IMAGE_MIME_TYPES,
  ...Object.keys(CHAT_IMAGE_MIME_BY_EXTENSION),
  CHAT_DOCUMENT_ACCEPT,
].join(",")

const MAX_CHAT_BINARY_ATTACHMENT_SIZE_BYTES = 7 * 1024 * 1024
const MAX_CHAT_TEXT_SIZE_BYTES = 1024 * 1024

function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === "string") {
        resolve(reader.result)
        return
      }
      reject(new Error("Failed to read file"))
    }
    reader.onerror = () =>
      reject(reader.error || new Error("Failed to read file"))
    reader.readAsDataURL(file)
  })
}

function getFileExtension(fileName: string): string {
  const lastDotIndex = fileName.lastIndexOf(".")
  if (lastDotIndex === -1) {
    return ""
  }
  return fileName.slice(lastDotIndex).toLowerCase()
}

function getSupportedImageMimeType(file: File): string | null {
  const normalizedType = file.type.trim().toLowerCase()
  if (normalizedType && CHAT_IMAGE_MIME_TYPE_SET.has(normalizedType)) {
    return normalizedType
  }

  const extension = getFileExtension(file.name)
  return CHAT_IMAGE_MIME_BY_EXTENSION[extension] ?? null
}

function normalizeFileForDataUrl(
  file: File,
  filename: string,
  mimeType: string,
): File {
  if (file.type.trim().toLowerCase() === mimeType) {
    return file
  }

  return new File([file], filename, { type: mimeType })
}

function getAttachmentFilename(file: File, index: number): string {
  const trimmedName = file.name.trim()
  if (trimmedName) {
    return trimmedName
  }

  if (file.type.trim().toLowerCase() === "application/pdf") {
    return `attachment-${index + 1}.pdf`
  }

  const extension = getFileExtension(file.name)
  if (extension) return `file-${index + 1}${extension}`
  const mimeType = getSupportedImageMimeType(file)
  return `image-${index + 1}${mimeType ? CHAT_IMAGE_EXTENSION_BY_MIME[mimeType] : ".png"}`
}

function getTransferItemFiles(dataTransfer: DataTransfer | null): File[] {
  if (!dataTransfer) {
    return []
  }

  const files = Array.from(dataTransfer.files)
  if (files.length > 0) {
    return files
  }

  return Array.from(dataTransfer.items)
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter((file): file is File => file !== null)
}

export function hasFileTransfer(dataTransfer: DataTransfer | null): boolean {
  if (!dataTransfer) {
    return false
  }

  if (dataTransfer.files.length > 0) {
    return true
  }

  return Array.from(dataTransfer.items).some((item) => item.kind === "file")
}

export function getTransferredFiles(dataTransfer: DataTransfer | null) {
  return getTransferItemFiles(dataTransfer)
}

export async function buildChatAttachments(
  files: readonly File[],
  t: TFunction,
): Promise<ChatAttachment[]> {
  const nextAttachments: ChatAttachment[] = []

  for (const [index, file] of files.entries()) {
    const filename = getAttachmentFilename(file, index)

    const imageMimeType = getSupportedImageMimeType(file)
    const extension = getFileExtension(file.name)
    const fileType = file.type.trim().toLowerCase()
    const textMimeType =
      CHAT_TEXT_FILE_TYPES[extension] ??
      (fileType.startsWith("text/") ? fileType : undefined)
    const isPdf = extension === ".pdf" || fileType === "application/pdf"
    const mimeType = imageMimeType ?? (isPdf ? "application/pdf" : textMimeType)
    if (!mimeType) {
      toast.error(
        t("chat.invalidFile", {
          name: filename,
        }),
      )
      continue
    }

    const isTextFile = !imageMimeType && !isPdf
    const maxSize = isTextFile
      ? MAX_CHAT_TEXT_SIZE_BYTES
      : MAX_CHAT_BINARY_ATTACHMENT_SIZE_BYTES
    if (file.size > maxSize) {
      toast.error(
        t("chat.fileTooLarge", {
          name: filename,
          size: isTextFile ? "1 MB" : "7 MB",
        }),
      )
      continue
    }

    try {
      const normalizedFile = normalizeFileForDataUrl(file, filename, mimeType)
      nextAttachments.push({
        type: imageMimeType ? "image" : "file",
        filename,
        url: await readFileAsDataUrl(normalizedFile),
        contentType: mimeType,
      })
    } catch {
      toast.error(
        t("chat.fileReadFailed", {
          name: filename,
        }),
      )
    }
  }

  return nextAttachments
}
