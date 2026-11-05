import {
  IconDownload,
  IconPlayerPause,
  IconPlayerPlay,
  IconSearch,
  IconTrash,
  IconX,
} from "@tabler/icons-react"
import { useDeferredValue, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { LogLevelSelect } from "@/components/logs/log-level-select"
import { LogsPanel } from "@/components/logs/logs-panel"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useGatewayLogs } from "@/hooks/use-gateway-logs"
import { useLogWrapColumns } from "@/hooks/use-log-wrap-columns"
import {
  buildLogFilename,
  downloadLogText,
  filterLogLines,
} from "@/lib/log-export"

export function LogsPage() {
  const { t } = useTranslation()
  const { clearLogs, clearing, logs } = useGatewayLogs()
  const { contentRef, measureRef, wrapColumns } = useLogWrapColumns()
  const [search, setSearch] = useState("")
  const [tailPaused, setTailPaused] = useState(false)
  const [resumeToken, setResumeToken] = useState(0)
  const [pausedAtCount, setPausedAtCount] = useState(0)

  const deferredSearch = useDeferredValue(search)
  const visibleLogs = useMemo(
    () => filterLogLines(logs, deferredSearch),
    [logs, deferredSearch],
  )
  const newLines = tailPaused ? Math.max(0, logs.length - pausedAtCount) : 0

  const resumeTail = () => {
    setTailPaused(false)
    setResumeToken((token) => token + 1)
  }

  const toggleTail = () => {
    if (tailPaused) {
      resumeTail()
      return
    }

    setPausedAtCount(logs.length)
    setTailPaused(true)
  }

  const clearAllLogs = async () => {
    await clearLogs()
    setPausedAtCount(0)
    setTailPaused(false)
  }

  const downloadLogs = () => {
    if (logs.length === 0) {
      return
    }

    downloadLogText(logs, buildLogFilename())
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.logs")}
        children={
          <>
            <div className="relative">
              <IconSearch className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
              <Input
                type="text"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Escape") {
                    setSearch("")
                  }
                }}
                placeholder={t("pages.logs.search")}
                aria-label={t("pages.logs.search")}
                className="h-8 w-44 pr-7 pl-8 text-sm"
              />
              {search && (
                <button
                  type="button"
                  aria-label={t("pages.logs.search_clear")}
                  onClick={() => setSearch("")}
                  className="text-muted-foreground hover:text-foreground absolute top-1/2 right-1.5 -translate-y-1/2 rounded-sm p-0.5"
                >
                  <IconX className="size-3.5" />
                </button>
              )}
            </div>

            <LogLevelSelect />

            <Button variant="outline" size="sm" onClick={toggleTail}>
              {tailPaused ? (
                <IconPlayerPlay className="size-4" />
              ) : (
                <IconPlayerPause className="size-4" />
              )}
              {tailPaused
                ? t("pages.logs.resume_tail")
                : t("pages.logs.pause_tail")}
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={downloadLogs}
              disabled={logs.length === 0}
            >
              <IconDownload className="size-4" />
              {t("pages.logs.download")}
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={clearAllLogs}
              disabled={logs.length === 0 || clearing}
            >
              <IconTrash className="size-4" />
              {t("pages.logs.clear")}
            </Button>
          </>
        }
      />

      <div className="flex flex-1 flex-col gap-4 overflow-hidden p-4 sm:p-8">
        <LogsPanel
          logs={visibleLogs}
          emptyMessage={
            logs.length === 0
              ? t("pages.logs.empty")
              : t("pages.logs.no_matches")
          }
          wrapColumns={wrapColumns}
          contentRef={contentRef}
          measureRef={measureRef}
          paused={tailPaused}
          newLines={newLines}
          resumeToken={resumeToken}
          onResume={resumeTail}
        />
      </div>
    </div>
  )
}
