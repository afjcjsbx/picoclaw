import {
  IconBook,
  IconLanguage,
  IconLoader2,
  IconLogout,
  IconMoon,
  IconPlayerPlay,
  IconPower,
  IconRefresh,
  IconSun,
} from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { postLauncherDashboardLogout } from "@/api/launcher-auth"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog.tsx"
import { Button } from "@/components/ui/button.tsx"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu.tsx"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useGateway } from "@/hooks/use-gateway.ts"
import { useTheme } from "@/hooks/use-theme.ts"

export function AppSidebarControls() {
  const { i18n, t } = useTranslation()
  const { theme, toggleTheme } = useTheme()
  const {
    state: gwState,
    loading: gwLoading,
    canStart,
    startReason,
    restartRequired,
    start,
    restart,
    stop,
    error: gwError,
  } = useGateway()

  const isRunning = gwState === "running"
  const isStarting = gwState === "starting"
  const isRestarting = gwState === "restarting"
  const isStopping = gwState === "stopping"
  const isStopped = gwState === "stopped" || gwState === "unknown"
  const [showStopDialog, setShowStopDialog] = React.useState(false)
  const [showLogoutDialog, setShowLogoutDialog] = React.useState(false)

  const handleLogout = async () => {
    await postLauncherDashboardLogout()
    globalThis.location.assign("/launcher-login")
  }

  const handleGatewayToggle = () => {
    if (gwLoading || isRestarting || isStopping || (!isRunning && !canStart)) {
      return
    }
    if (isRunning) {
      setShowStopDialog(true)
    } else {
      void start()
    }
  }

  const handleGatewayRestart = () => {
    if (gwLoading || isRestarting || !restartRequired || !canStart) return
    void restart()
  }

  const confirmStop = () => {
    setShowStopDialog(false)
    stop()
  }

  const gatewayLabel = isRunning
    ? t("header.gateway.action.stop")
    : isStopping
      ? t("header.gateway.status.stopping")
      : isRestarting
        ? t("header.gateway.status.restarting")
        : isStarting
          ? t("header.gateway.status.starting")
          : t("header.gateway.action.start")
  const gatewayDisabled =
    gwLoading ||
    isStarting ||
    isRestarting ||
    isStopping ||
    (!isRunning && !canStart)
  const gatewayTooltip =
    gwError ?? (!canStart && startReason ? startReason : gatewayLabel)
  const docsLabel = t("tour.docs.title")
  const languageLabel = t("header.language", { defaultValue: "Language" })
  const themeLabel =
    theme === "dark"
      ? t("header.lightMode", { defaultValue: "Light mode" })
      : t("header.darkMode", { defaultValue: "Dark mode" })

  return (
    <>
      <AlertDialog open={showStopDialog} onOpenChange={setShowStopDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("header.gateway.stopDialog.title")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("header.gateway.stopDialog.description")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmStop}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {t("header.gateway.stopDialog.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={showLogoutDialog} onOpenChange={setShowLogoutDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("header.logout.tooltip")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("header.logout.description")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void handleLogout()}>
              {t("header.logout.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <div className="flex flex-wrap items-center justify-center gap-1 group-data-[collapsible=icon]:flex-col">
        {restartRequired && (
          <Tooltip delayDuration={700}>
            <TooltipTrigger asChild>
              <Button
                variant="secondary"
                size="icon-sm"
                className="bg-amber-500/15 text-amber-700 hover:bg-amber-500/25 hover:text-amber-800 dark:text-amber-300 dark:hover:bg-amber-500/25 size-8"
                onClick={handleGatewayRestart}
                disabled={gwLoading || isRestarting || isStopping || !canStart}
                aria-label={t("header.gateway.action.restart")}
                title={t("header.gateway.action.restart")}
              >
                <IconRefresh className="size-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {t("header.gateway.restartRequired")}
            </TooltipContent>
          </Tooltip>
        )}

        <Tooltip
          delayDuration={gwError || (!canStart && startReason) ? 0 : 700}
        >
          <TooltipTrigger asChild>
            <span
              className={
                !canStart && !isRunning && startReason
                  ? "cursor-not-allowed"
                  : undefined
              }
              tabIndex={!canStart && !isRunning && startReason ? 0 : undefined}
            >
              <Button
                variant={
                  isRunning
                    ? "destructive"
                    : isStarting || isRestarting || isStopping
                      ? "secondary"
                      : "default"
                }
                size="icon-sm"
                className={`size-8 ${
                  isStopped ? "bg-green-500 text-white hover:bg-green-600" : ""
                } ${!canStart && !isRunning ? "pointer-events-none" : ""}`}
                data-tour="gateway-button"
                onClick={handleGatewayToggle}
                disabled={gatewayDisabled}
                aria-label={gatewayLabel}
                title={gatewayLabel}
              >
                {gwLoading || isStarting || isRestarting || isStopping ? (
                  <IconLoader2 className="size-4 animate-spin opacity-70" />
                ) : isRunning ? (
                  <IconPower className="size-4 opacity-80" />
                ) : (
                  <IconPlayerPlay className="size-4 opacity-80" />
                )}
              </Button>
            </span>
          </TooltipTrigger>
          <TooltipContent>{gatewayTooltip}</TooltipContent>
        </Tooltip>

        {/* Docs Link */}
        <Tooltip delayDuration={700}>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="size-8"
              data-tour="docs-button"
              aria-label={docsLabel}
              title={docsLabel}
              asChild
            >
              <a
                href="https://afjcjsbx.github.io/picoclaw/"
                target="_blank"
                rel="noreferrer"
              >
                <IconBook className="size-4.5" />
              </a>
            </Button>
          </TooltipTrigger>
          <TooltipContent>{docsLabel}</TooltipContent>
        </Tooltip>

        {/* Language Switcher */}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="size-8"
              aria-label={languageLabel}
              title={languageLabel}
            >
              <IconLanguage className="size-4.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent side="top" align="center">
            <DropdownMenuItem onClick={() => i18n.changeLanguage("en")}>
              English
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => i18n.changeLanguage("pt-BR")}>
              Português (Brasil)
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => i18n.changeLanguage("bn-IN")}>
              বাংলা
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => i18n.changeLanguage("zh")}>
              简体中文
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => i18n.changeLanguage("cs")}>
              Čeština
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>

        {/* Theme Toggle */}
        <Button
          variant="ghost"
          size="icon-sm"
          className="size-8"
          onClick={toggleTheme}
          aria-label={themeLabel}
          title={themeLabel}
        >
          {theme === "dark" ? (
            <IconSun className="size-4.5" />
          ) : (
            <IconMoon className="size-4.5" />
          )}
        </Button>

        {/* Logout */}
        <Tooltip delayDuration={700}>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              className="size-8"
              onClick={() => setShowLogoutDialog(true)}
              aria-label={t("header.logout.tooltip")}
              title={t("header.logout.tooltip")}
            >
              <IconLogout className="size-4.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t("header.logout.tooltip")}</TooltipContent>
        </Tooltip>
      </div>
    </>
  )
}
