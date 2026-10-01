import {
  IconAlertTriangle,
  IconChevronDown,
  IconLoader2,
  IconPencil,
  IconPlugConnected,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconServer,
  IconTerminal2,
  IconTool,
  IconTrash,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useDeferredValue, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import type { MCPConnectionStatus, MCPServer, MCPTool } from "@/api/mcp"
import {
  createMCPServer,
  getMCPDashboard,
  removeMCPServer,
  updateMCPServerCommand,
  updateMCPServerSettings,
} from "@/api/mcp"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader } from "@/components/ui/card"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"

import { MCPStatusBadge } from "./mcp-status-badge"

type StatusFilter = "all" | MCPConnectionStatus

export function MCPPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [search, setSearch] = useState("")
  const deferredSearch = useDeferredValue(search)
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all")
  const [editingServer, setEditingServer] = useState<MCPServer | null>(null)
  const [deletingServer, setDeletingServer] = useState<MCPServer | null>(null)
  const [addingServer, setAddingServer] = useState(false)
  const query = useQuery({
    queryKey: ["agents", "mcp"],
    queryFn: getMCPDashboard,
    refetchInterval: 3000,
    refetchOnWindowFocus: true,
  })

  const refreshAfterMutation = () => {
    void queryClient.invalidateQueries({ queryKey: ["agents", "mcp"] })
    void queryClient.invalidateQueries({ queryKey: ["config"] })
  }
  const updateMutation = useMutation({
    mutationFn: ({
      name,
      command,
      args,
    }: {
      name: string
      command: string
      args: string[]
    }) => updateMCPServerCommand(name, command, args),
    onSuccess: () => {
      setEditingServer(null)
      refreshAfterMutation()
      toast.success(t("pages.agent.mcp.save_success"))
    },
    onError: () => toast.error(t("pages.agent.mcp.mutation_error")),
  })
  const removeMutation = useMutation({
    mutationFn: (name: string) => removeMCPServer(name),
    onSuccess: () => {
      setDeletingServer(null)
      refreshAfterMutation()
      toast.success(t("pages.agent.mcp.remove_success"))
    },
    onError: () => toast.error(t("pages.agent.mcp.mutation_error")),
  })
  const settingsMutation = useMutation({
    mutationFn: ({
      name,
      settings,
    }: {
      name: string
      settings: { enabled?: boolean; deferred?: boolean }
    }) => updateMCPServerSettings(name, settings),
    onSuccess: () => {
      refreshAfterMutation()
      toast.success(t("pages.agent.mcp.settings_success"))
    },
    onError: () => toast.error(t("pages.agent.mcp.mutation_error")),
  })
  const createMutation = useMutation({
    mutationFn: ({
      name,
      command,
      args,
      enabled,
      deferred,
    }: {
      name: string
      command: string
      args: string[]
      enabled: boolean
      deferred: boolean
    }) =>
      createMCPServer(name, {
        type: "stdio",
        command,
        args,
        enabled,
        deferred,
      }),
    onSuccess: () => {
      setAddingServer(false)
      refreshAfterMutation()
      toast.success(t("pages.agent.mcp.create_success"))
    },
    onError: () => toast.error(t("pages.agent.mcp.mutation_error")),
  })

  const servers = useMemo(() => {
    const needle = deferredSearch.trim().toLowerCase()
    return (query.data?.servers ?? []).filter((server) => {
      if (statusFilter !== "all" && server.status !== statusFilter) return false
      if (!needle) return true
      return [
        server.name,
        server.command ?? "",
        server.transport,
        ...server.tools.flatMap((tool) => [tool.name, tool.description ?? ""]),
      ].some((value) => value.toLowerCase().includes(needle))
    })
  }, [deferredSearch, query.data?.servers, statusFilter])

  const connectedCount =
    query.data?.servers.filter((server) => server.status === "connected")
      .length ?? 0
  const toolCount =
    query.data?.servers.reduce(
      (total, server) => total + server.tool_count,
      0,
    ) ?? 0

  return (
    <div className="bg-background flex h-full flex-col">
      <PageHeader title={t("navigation.mcp", "MCP")}>
        <Button
          type="button"
          size="sm"
          disabled={createMutation.isPending}
          onClick={() => setAddingServer(true)}
        >
          <IconPlus className="size-4" />
          {t("pages.agent.mcp.add", "Add server")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <IconRefresh
            className={cn("size-4", query.isFetching && "animate-spin")}
          />
          {t("pages.agent.mcp.refresh", "Refresh")}
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-auto px-6 py-6 pb-20">
        <div className="mx-auto w-full max-w-6xl space-y-6">
          <div className="flex flex-col gap-5 md:flex-row md:items-end md:justify-between">
            <div className="space-y-2">
              <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
                {t("pages.agent.mcp.title", "MCP Servers")}
              </h1>
              <p className="text-muted-foreground/80 max-w-2xl text-sm leading-relaxed">
                {t(
                  "pages.agent.mcp.description",
                  "Monitor configured Model Context Protocol servers and inspect their exposed tools.",
                )}
              </p>
            </div>
            <div className="flex w-full flex-col gap-3 sm:flex-row md:w-auto">
              <div className="group relative sm:w-72">
                <IconSearch className="text-muted-foreground/60 absolute top-1/2 left-3.5 size-4 -translate-y-1/2" />
                <Input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder={t(
                    "pages.agent.mcp.search_placeholder",
                    "Search servers or tools...",
                  )}
                  className="bg-muted/40 h-11 rounded-xl border-transparent pl-10 shadow-none"
                />
              </div>
              <Select
                value={statusFilter}
                onValueChange={(value) =>
                  setStatusFilter(value as StatusFilter)
                }
              >
                <SelectTrigger className="bg-muted/40 h-11 w-full rounded-xl border-transparent shadow-none sm:w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(
                    [
                      "all",
                      "connected",
                      "connecting",
                      "error",
                      "disconnected",
                      "disabled",
                    ] as const
                  ).map((status) => (
                    <SelectItem key={status} value={status}>
                      {t(`pages.agent.mcp.status.${status}`, status)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {query.data &&
            (!query.data.enabled || !query.data.runtime_available) && (
              <RuntimeNotice
                enabled={query.data.enabled}
                runtimeAvailable={query.data.runtime_available}
              />
            )}

          <div className="grid gap-4 sm:grid-cols-3">
            <SummaryCard
              icon={IconServer}
              label={t("pages.agent.mcp.summary.servers", "Configured servers")}
              value={query.data?.servers.length ?? 0}
            />
            <SummaryCard
              icon={IconPlugConnected}
              label={t("pages.agent.mcp.summary.connected", "Connected")}
              value={connectedCount}
            />
            <SummaryCard
              icon={IconTool}
              label={t("pages.agent.mcp.summary.tools", "Discovered tools")}
              value={toolCount}
            />
          </div>

          {query.isLoading ? (
            <LoadingState />
          ) : query.error ? (
            <div className="border-destructive/20 bg-destructive/5 text-destructive rounded-xl border p-8 text-center text-sm">
              {t("pages.agent.mcp.load_error", "Failed to load MCP status.")}
            </div>
          ) : servers.length === 0 ? (
            <div className="border-border/60 rounded-xl border border-dashed p-12 text-center">
              <IconServer className="text-muted-foreground/50 mx-auto mb-3 size-8" />
              <p className="font-medium">
                {t("pages.agent.mcp.empty", "No MCP servers found")}
              </p>
              <p className="text-muted-foreground mt-1 text-sm">
                {t(
                  "pages.agent.mcp.empty_hint",
                  "Configure an MCP server in Settings to see it here.",
                )}
              </p>
            </div>
          ) : (
            <div className="space-y-4">
              {servers.map((server) => (
                <ServerCard
                  key={server.name}
                  server={server}
                  actionsDisabled={
                    updateMutation.isPending ||
                    removeMutation.isPending ||
                    settingsMutation.isPending ||
                    createMutation.isPending
                  }
                  onEdit={setEditingServer}
                  onRemove={setDeletingServer}
                  onUpdateSettings={(settings) =>
                    settingsMutation.mutate({ name: server.name, settings })
                  }
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {editingServer && (
        <EditMCPServerDialog
          key={editingServer.name}
          server={editingServer}
          saving={updateMutation.isPending}
          onClose={() => setEditingServer(null)}
          onSave={(command, args) =>
            updateMutation.mutate({
              name: editingServer.name,
              command,
              args,
            })
          }
        />
      )}
      {addingServer && (
        <AddMCPServerDialog
          existingNames={query.data?.servers.map((server) => server.name) ?? []}
          saving={createMutation.isPending}
          onClose={() => setAddingServer(false)}
          onSave={(name, command, args, enabled, deferred) =>
            createMutation.mutate({ name, command, args, enabled, deferred })
          }
        />
      )}
      {deletingServer && (
        <RemoveMCPServerDialog
          server={deletingServer}
          removing={removeMutation.isPending}
          onClose={() => setDeletingServer(null)}
          onRemove={() => removeMutation.mutate(deletingServer.name)}
        />
      )}
    </div>
  )
}

function AddMCPServerDialog({
  existingNames,
  saving,
  onClose,
  onSave,
}: {
  existingNames: string[]
  saving: boolean
  onClose: () => void
  onSave: (
    name: string,
    command: string,
    args: string[],
    enabled: boolean,
    deferred: boolean,
  ) => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState("")
  const [command, setCommand] = useState("")
  const [argsText, setArgsText] = useState("")
  const [enabled, setEnabled] = useState(true)
  const [deferred, setDeferred] = useState(false)
  const trimmedName = name.trim()
  const trimmedCommand = command.trim()
  const nameInUse = existingNames.includes(trimmedName)

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!trimmedName || !trimmedCommand || nameInUse) return
    onSave(
      trimmedName,
      trimmedCommand,
      argsText
        .split(/\r?\n/)
        .map((arg) => arg.trim())
        .filter((arg) => arg !== ""),
      enabled,
      deferred,
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("pages.agent.mcp.add_title")}</DialogTitle>
          <DialogDescription>
            {t("pages.agent.mcp.add_description")}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          <label className="grid gap-2 text-sm font-medium" htmlFor="mcp-name">
            {t("pages.agent.mcp.name_label", "Name")}
            <Input
              id="mcp-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              disabled={saving}
              autoFocus
            />
            {nameInUse && (
              <span className="text-destructive text-xs font-normal">
                {t("pages.agent.mcp.name_in_use")}
              </span>
            )}
          </label>
          <label
            className="grid gap-2 text-sm font-medium"
            htmlFor="mcp-new-command"
          >
            {t("pages.agent.mcp.command_label", "Command")}
            <Input
              id="mcp-new-command"
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              disabled={saving}
            />
          </label>
          <label
            className="grid gap-2 text-sm font-medium"
            htmlFor="mcp-new-args"
          >
            {t("pages.agent.mcp.args_label", "Arguments")}
            <Textarea
              id="mcp-new-args"
              value={argsText}
              onChange={(event) => setArgsText(event.target.value)}
              disabled={saving}
              className="min-h-28 font-mono text-xs"
            />
            <span className="text-muted-foreground text-xs font-normal">
              {t("pages.agent.mcp.args_hint", "One argument per line.")}
            </span>
          </label>
          <div className="bg-muted/30 flex flex-wrap items-center gap-x-5 gap-y-3 rounded-lg px-3 py-2.5">
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <Switch
                size="sm"
                checked={enabled}
                disabled={saving}
                onCheckedChange={setEnabled}
              />
              {t("pages.agent.mcp.server_enabled", "Server enabled")}
            </label>
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <Switch
                size="sm"
                checked={deferred}
                disabled={saving}
                onCheckedChange={setDeferred}
              />
              {t("pages.agent.mcp.deferred_mode", "Deferred discovery")}
            </label>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={saving}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              disabled={saving || !trimmedName || !trimmedCommand || nameInUse}
            >
              {saving && <IconLoader2 className="size-4 animate-spin" />}
              {t("pages.agent.mcp.add", "Add server")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RuntimeNotice({
  enabled,
  runtimeAvailable,
}: {
  enabled: boolean
  runtimeAvailable: boolean
}) {
  const { t } = useTranslation()
  return (
    <div className="flex items-start gap-3 rounded-xl border border-amber-500/20 bg-amber-500/5 p-4 text-sm">
      <IconAlertTriangle className="mt-0.5 size-5 shrink-0 text-amber-600 dark:text-amber-400" />
      <div className="space-y-1">
        <p className="font-medium">
          {!enabled
            ? t(
                "pages.agent.mcp.integration_disabled",
                "MCP integration is disabled",
              )
            : t(
                "pages.agent.mcp.runtime_unavailable",
                "Gateway runtime is unavailable",
              )}
        </p>
        <p className="text-muted-foreground">
          {!enabled
            ? t(
                "pages.agent.mcp.integration_disabled_hint",
                "Enable MCP in Config, then restart or reload the gateway.",
              )
            : t(
                "pages.agent.mcp.runtime_unavailable_hint",
                "Configured servers are shown, but live connection and tool data are unavailable.",
              )}
          {!runtimeAvailable && " "}
          <Link
            to="/config"
            className="text-primary underline underline-offset-4"
          >
            {t("pages.agent.mcp.open_config", "Open Config")}
          </Link>
        </p>
      </div>
    </div>
  )
}

function SummaryCard({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof IconServer
  label: string
  value: number
}) {
  return (
    <Card size="sm" className="border-border/50 gap-2 shadow-none">
      <CardContent className="flex items-center gap-3">
        <div className="bg-muted flex size-9 items-center justify-center rounded-lg">
          <Icon className="text-muted-foreground size-4" />
        </div>
        <div>
          <div className="text-xl font-semibold tabular-nums">{value}</div>
          <div className="text-muted-foreground text-xs">{label}</div>
        </div>
      </CardContent>
    </Card>
  )
}

function ServerCard({
  server,
  actionsDisabled,
  onEdit,
  onRemove,
  onUpdateSettings,
}: {
  server: MCPServer
  actionsDisabled: boolean
  onEdit: (server: MCPServer) => void
  onRemove: (server: MCPServer) => void
  onUpdateSettings: (settings: {
    enabled?: boolean
    deferred?: boolean
  }) => void
}) {
  const { t } = useTranslation()
  return (
    <Card className="border-border/50 gap-4 py-5 shadow-none">
      <CardHeader className="grid-cols-[1fr_auto] px-5">
        <div className="min-w-0 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="font-mono text-base font-semibold break-all">
              {server.name}
            </h2>
            <MCPStatusBadge status={server.status} />
            <Badge variant={server.enabled ? "secondary" : "outline"}>
              {server.enabled
                ? t("pages.agent.mcp.enabled", "Enabled")
                : t("pages.agent.mcp.disabled", "Disabled")}
            </Badge>
            <Badge
              variant="outline"
              className="text-muted-foreground uppercase"
            >
              {server.transport || "unknown"}
            </Badge>
            {server.deferred && (
              <Badge variant="secondary">
                {t("pages.agent.mcp.deferred", "Deferred")}
              </Badge>
            )}
          </div>
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <IconTool className="size-3.5" />
            {t("pages.agent.mcp.tool_count", {
              defaultValue: "{{count}} tools",
              count: server.tool_count,
            })}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {server.transport === "stdio" && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={actionsDisabled}
              onClick={() => onEdit(server)}
            >
              <IconPencil className="size-4" />
              {t("pages.agent.mcp.edit", "Edit")}
            </Button>
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={actionsDisabled}
            className="text-destructive hover:text-destructive"
            onClick={() => onRemove(server)}
          >
            <IconTrash className="size-4" />
            {t("pages.agent.mcp.remove", "Remove")}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4 px-5">
        <div className="bg-muted/30 flex flex-wrap items-center gap-x-5 gap-y-3 rounded-lg px-3 py-2.5">
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <Switch
              size="sm"
              checked={server.configured_enabled ?? server.enabled}
              disabled={actionsDisabled}
              onCheckedChange={(enabled) => onUpdateSettings({ enabled })}
            />
            {t("pages.agent.mcp.server_enabled", "Server enabled")}
          </label>
          <label className="flex cursor-pointer items-center gap-2 text-sm">
            <Switch
              size="sm"
              checked={server.deferred}
              disabled={actionsDisabled}
              onCheckedChange={(deferred) => onUpdateSettings({ deferred })}
            />
            {t("pages.agent.mcp.deferred_mode", "Deferred discovery")}
          </label>
        </div>
        {server.command && (
          <div className="space-y-2">
            <div className="text-muted-foreground flex items-center gap-2 text-xs font-medium uppercase">
              <IconTerminal2 className="size-3.5" />
              {t("pages.agent.mcp.command", "Launch command")}
            </div>
            <code className="bg-muted/60 block overflow-x-auto rounded-lg px-3 py-2.5 text-xs leading-relaxed break-all whitespace-pre-wrap">
              {server.command}
            </code>
          </div>
        )}
        {server.error && (
          <div className="border-destructive/20 bg-destructive/5 text-destructive rounded-lg border p-3 text-xs leading-relaxed break-words">
            <div className="mb-1 flex items-center gap-2 font-semibold">
              <IconAlertTriangle className="size-4" />
              {t("pages.agent.mcp.error_details", "Connection error")}
            </div>
            {server.error}
          </div>
        )}
        <ToolList tools={server.tools} />
      </CardContent>
    </Card>
  )
}

function EditMCPServerDialog({
  server,
  saving,
  onClose,
  onSave,
}: {
  server: MCPServer
  saving: boolean
  onClose: () => void
  onSave: (command: string, args: string[]) => void
}) {
  const { t } = useTranslation()
  const [command, setCommand] = useState(server.launch_command ?? "")
  const [argsText, setArgsText] = useState((server.args ?? []).join("\n"))
  const trimmedCommand = command.trim()

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!trimmedCommand) return
    onSave(
      trimmedCommand,
      argsText
        .split(/\r?\n/)
        .map((arg) => arg.trim())
        .filter((arg) => arg !== ""),
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("pages.agent.mcp.edit_title")}</DialogTitle>
          <DialogDescription>
            {t("pages.agent.mcp.edit_description", { name: server.name })}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          <label
            className="grid gap-2 text-sm font-medium"
            htmlFor="mcp-command"
          >
            {t("pages.agent.mcp.command_label", "Command")}
            <Input
              id="mcp-command"
              value={command}
              onChange={(event) => setCommand(event.target.value)}
              disabled={saving}
              autoFocus
            />
          </label>
          <label className="grid gap-2 text-sm font-medium" htmlFor="mcp-args">
            {t("pages.agent.mcp.args_label", "Arguments")}
            <Textarea
              id="mcp-args"
              value={argsText}
              onChange={(event) => setArgsText(event.target.value)}
              disabled={saving}
              className="min-h-32 font-mono text-xs"
            />
            <span className="text-muted-foreground text-xs font-normal">
              {t("pages.agent.mcp.args_hint", "One argument per line.")}
            </span>
          </label>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={saving}
            >
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={saving || !trimmedCommand}>
              {saving && <IconLoader2 className="size-4 animate-spin" />}
              {t("pages.agent.mcp.save", "Save changes")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RemoveMCPServerDialog({
  server,
  removing,
  onClose,
  onRemove,
}: {
  server: MCPServer
  removing: boolean
  onClose: () => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  return (
    <AlertDialog open onOpenChange={(open) => !open && !removing && onClose()}>
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("pages.agent.mcp.remove_title")}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t("pages.agent.mcp.remove_description", { name: server.name })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={onClose} disabled={removing}>
            {t("common.cancel")}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={onRemove}
            disabled={removing}
          >
            {removing && <IconLoader2 className="size-4 animate-spin" />}
            {t("pages.agent.mcp.remove", "Remove")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function ToolList({ tools }: { tools: MCPTool[] }) {
  const { t } = useTranslation()
  if (tools.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        {t("pages.agent.mcp.no_tools", "No tools discovered for this server.")}
      </p>
    )
  }
  return (
    <Collapsible className="group/tools border-border/50 rounded-lg border">
      <CollapsibleTrigger className="hover:bg-muted/40 flex w-full items-center justify-between gap-4 px-4 py-3 text-left">
        <div className="flex items-center gap-2 text-sm font-medium">
          <IconTool className="text-muted-foreground size-4" />
          {t("pages.agent.mcp.tools_section", {
            defaultValue: "Tools ({{count}})",
            count: tools.length,
          })}
        </div>
        <IconChevronDown className="text-muted-foreground size-4 shrink-0 transition-transform group-data-[state=open]/tools:rotate-180" />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="border-border/50 space-y-2 border-t p-3">
          {tools.map((tool) => (
            <Collapsible
              key={tool.name}
              className="group/tool border-border/50 rounded-lg border"
            >
              <CollapsibleTrigger className="hover:bg-muted/40 flex w-full items-center justify-between gap-4 px-4 py-3 text-left">
                <div className="min-w-0">
                  <div className="font-mono text-sm font-medium break-all">
                    {tool.name}
                  </div>
                  {tool.description && (
                    <div className="text-muted-foreground mt-1 line-clamp-2 text-xs">
                      {tool.description}
                    </div>
                  )}
                </div>
                <IconChevronDown className="text-muted-foreground size-4 shrink-0 transition-transform group-data-[state=open]/tool:rotate-180" />
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className="border-border/50 border-t px-4 py-3">
                  {tool.parameters.length === 0 ? (
                    <p className="text-muted-foreground text-xs">
                      {t("pages.agent.mcp.no_parameters", "No parameters")}
                    </p>
                  ) : (
                    <div className="overflow-x-auto">
                      <table className="w-full min-w-[520px] text-left text-xs">
                        <thead className="text-muted-foreground">
                          <tr>
                            <th className="pb-2 font-medium">
                              {t("pages.agent.mcp.parameter", "Parameter")}
                            </th>
                            <th className="pb-2 font-medium">
                              {t("pages.agent.mcp.type", "Type")}
                            </th>
                            <th className="pb-2 font-medium">
                              {t("pages.agent.mcp.requirement", "Requirement")}
                            </th>
                            <th className="pb-2 font-medium">
                              {t(
                                "pages.agent.mcp.parameter_description",
                                "Description",
                              )}
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {tool.parameters.map((parameter) => (
                            <tr
                              key={parameter.name}
                              className="border-border/40 border-t align-top"
                            >
                              <td className="py-2 pr-4 font-mono font-medium break-all">
                                {parameter.name}
                              </td>
                              <td className="text-muted-foreground py-2 pr-4 font-mono">
                                {parameter.type || "—"}
                              </td>
                              <td className="py-2 pr-4">
                                <Badge
                                  variant={
                                    parameter.required ? "default" : "outline"
                                  }
                                >
                                  {parameter.required
                                    ? t("pages.agent.mcp.required", "Required")
                                    : t("pages.agent.mcp.optional", "Optional")}
                                </Badge>
                              </td>
                              <td className="text-muted-foreground py-2 leading-relaxed">
                                {parameter.description || "—"}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
              </CollapsibleContent>
            </Collapsible>
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

function LoadingState() {
  return (
    <div className="space-y-4">
      {[0, 1].map((item) => (
        <Card key={item} className="gap-4 py-5 shadow-none">
          <CardContent className="space-y-4 px-5">
            <div className="flex gap-3">
              <Skeleton className="h-6 w-40" />
              <Skeleton className="h-6 w-24" />
            </div>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-12 w-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
