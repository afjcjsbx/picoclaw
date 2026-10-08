import { IconFileText, IconX } from "@tabler/icons-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import { type FilePreview, getFilePreview } from "@/api/files"
import { Button } from "@/components/ui/button"

type PreviewState =
  | { path: string; status: "loading" }
  | { path: string; status: "error"; error: string }
  | { path: string; status: "ready"; preview: FilePreview }

function fileName(path: string) {
  return path.split(/[\\/]/).at(-1) || path
}

export function FilePreviewPanel({
  activePath,
  onClose,
  width,
}: {
  activePath: string
  onClose: () => void
  width: number
}) {
  const { t } = useTranslation()
  const [state, setState] = useState<PreviewState>({
    path: activePath,
    status: "loading",
  })

  useEffect(() => {
    let cancelled = false
    setState({ path: activePath, status: "loading" })
    void getFilePreview(activePath)
      .then((preview) => {
        if (!cancelled) setState({ path: activePath, status: "ready", preview })
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState({
            path: activePath,
            status: "error",
            error: error instanceof Error ? error.message : String(error),
          })
        }
      })
    return () => {
      cancelled = true
    }
  }, [activePath])

  const currentState =
    state.path === activePath
      ? state
      : { path: activePath, status: "loading" as const }

  return (
    <aside
      aria-label={t("chat.filePreview", { defaultValue: "File preview" })}
      className="bg-background flex min-h-0 min-w-0 flex-col border-l max-sm:absolute max-sm:inset-y-0 max-sm:right-0 max-sm:z-30 max-sm:shadow-xl"
      style={{ width, maxWidth: "100%" }}
    >
      <div className="border-border flex h-11 shrink-0 items-center justify-between gap-2 border-b px-3">
        <div className="text-muted-foreground flex min-w-0 items-center gap-2 text-sm">
          <IconFileText className="size-4 shrink-0" />
          <span className="truncate" title={activePath}>
            {fileName(activePath)}
          </span>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          onClick={onClose}
          aria-label={t("chat.closeFilePreview", {
            defaultValue: "Close file preview",
          })}
          title={t("chat.closeFilePreview", {
            defaultValue: "Close file preview",
          })}
        >
          <IconX />
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {currentState.status === "loading" ? (
          <div className="text-muted-foreground p-4 text-sm" role="status">
            {t("chat.filePreviewLoading", { defaultValue: "Loading file…" })}
          </div>
        ) : currentState.status === "error" ? (
          <div className="text-destructive p-4 text-sm" role="alert">
            {currentState.error}
          </div>
        ) : (
          <>
            {currentState.preview.truncated && (
              <div className="border-b border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">
                {t("chat.filePreviewTruncated", {
                  defaultValue: "Preview limited to the first 384 KiB.",
                })}
              </div>
            )}
            <div className="flex min-w-max font-mono text-xs leading-5">
              <div
                aria-hidden="true"
                className="bg-background text-muted-foreground/60 border-border sticky left-0 z-10 shrink-0 border-r px-3 py-3 text-right select-none"
              >
                {currentState.preview.content.split("\n").map((_, index) => (
                  <div key={index} className="h-5">
                    {index + 1}
                  </div>
                ))}
              </div>
              <pre className="text-foreground m-0 min-w-max px-4 py-3 font-mono text-xs leading-5 whitespace-pre">
                {currentState.preview.content}
              </pre>
            </div>
          </>
        )}
      </div>
    </aside>
  )
}
