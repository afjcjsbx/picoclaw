import { launcherFetch } from "@/api/http"

export interface FilePreview {
  path: string
  content: string
  truncated?: boolean
}

export async function getFilePreview(path: string): Promise<FilePreview> {
  const params = new URLSearchParams({ path })
  const res = await launcherFetch(`/api/files/preview?${params.toString()}`)
  if (!res.ok) {
    throw new Error(
      (await res.text()) || `Failed to preview file: ${res.status}`,
    )
  }
  return res.json()
}
