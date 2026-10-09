import { IconTrash } from "@tabler/icons-react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { type AppConfig, getAppConfig, patchAppConfig } from "@/api/channels"
import { getGatewayStatus, restartGateway } from "@/api/gateway"
import {
  type GatewayLogLevel,
  LogLevelSelect,
} from "@/components/logs/log-level-select"
import { LogsPanel } from "@/components/logs/logs-panel"
import { PageHeader } from "@/components/page-header"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { useGatewayLogs } from "@/hooks/use-gateway-logs"
import { useLogWrapColumns } from "@/hooks/use-log-wrap-columns"
import { refreshGatewayState } from "@/store/gateway"

const LOG_LEVEL_OPTIONS = ["debug", "info", "warn", "error", "fatal"] as const

function getGatewayLogLevel(config: AppConfig | undefined): GatewayLogLevel {
  const gateway = config?.gateway
  if (typeof gateway === "object" && gateway !== null) {
    const logLevel = (gateway as Record<string, unknown>).log_level
    if (
      typeof logLevel === "string" &&
      LOG_LEVEL_OPTIONS.includes(logLevel as GatewayLogLevel)
    ) {
      return logLevel as GatewayLogLevel
    }
  }
  return "warn"
}

export function LogsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { clearLogs, clearing, logs } = useGatewayLogs()
  const [logLevel, setLogLevel] = useState<GatewayLogLevel>("warn")
  const [pendingLogLevel, setPendingLogLevel] =
    useState<GatewayLogLevel | null>(null)
  const [savingLogLevel, setSavingLogLevel] = useState(false)
  const { contentRef, measureRef, wrapColumns } = useLogWrapColumns()

  const { data: configData } = useQuery({
    queryKey: ["config"],
    queryFn: getAppConfig,
  })

  useEffect(() => {
    setLogLevel(getGatewayLogLevel(configData))
  }, [configData])

  const saveLogLevel = async () => {
    if (!pendingLogLevel) return

    const nextLevel = pendingLogLevel
    setSavingLogLevel(true)
    try {
      await patchAppConfig({
        gateway: {
          log_level: nextLevel,
        },
      })
      setLogLevel(nextLevel)
      setPendingLogLevel(null)
      await queryClient.invalidateQueries({ queryKey: ["config"] })

      const gatewayStatus = await getGatewayStatus()
      if (gatewayStatus.gateway_status === "running") {
        await restartGateway()
      }
      await refreshGatewayState({ force: true })
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("pages.logs.log_level_error"),
      )
      const latestConfig = await getAppConfig().catch(() => undefined)
      if (latestConfig) {
        setLogLevel(getGatewayLogLevel(latestConfig))
      }
    } finally {
      setSavingLogLevel(false)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.logs")}
        children={
          <>
            <LogLevelSelect
              value={pendingLogLevel ?? logLevel}
              onValueChange={(nextLevel) => {
                if (nextLevel !== logLevel) setPendingLogLevel(nextLevel)
              }}
            />

            <Button
              variant="outline"
              size="sm"
              onClick={clearLogs}
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
          logs={logs}
          wrapColumns={wrapColumns}
          contentRef={contentRef}
          measureRef={measureRef}
        />
      </div>

      <AlertDialog
        open={pendingLogLevel !== null}
        onOpenChange={(open) => {
          if (!open && !savingLogLevel) setPendingLogLevel(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("common.restartRequiredTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("common.restartRequired")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={savingLogLevel}>
              {t("common.cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={savingLogLevel}
              onClick={(event) => {
                event.preventDefault()
                void saveLogLevel()
              }}
            >
              {savingLogLevel ? t("common.saving") : t("common.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
