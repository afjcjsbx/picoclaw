import { IconLoader2 } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import type { MCPServerTransport } from "@/api/mcp"
import { Button } from "@/components/ui/button"
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
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"

import { EMPTY_MCP_SERVER_FORM, type MCPServerForm } from "./mcp-form"

interface MCPServerDialogProps {
  mode: "add" | "edit"
  server?: MCPServerForm
  existingNames?: string[]
  saving: boolean
  onClose: () => void
  onSave: (server: MCPServerForm) => void
}

export function MCPServerDialog({
  mode,
  server,
  existingNames = [],
  saving,
  onClose,
  onSave,
}: MCPServerDialogProps) {
  const { t } = useTranslation()
  const [form, setForm] = useState<MCPServerForm>(
    server ?? EMPTY_MCP_SERVER_FORM,
  )
  const trimmedName = form.name.trim()
  const nameInUse =
    mode === "add" && trimmedName !== "" && existingNames.includes(trimmedName)
  const canSubmit =
    trimmedName !== "" &&
    !nameInUse &&
    (form.type === "stdio"
      ? form.command.trim() !== ""
      : form.url.trim() !== "")

  const update = <K extends keyof MCPServerForm>(
    key: K,
    value: MCPServerForm[K],
  ) => {
    setForm((previous) => ({ ...previous, [key]: value }))
  }

  const deferredValue =
    form.deferred === null ? "inherit" : form.deferred ? "deferred" : "eager"

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!canSubmit) return
    onSave({ ...form, name: trimmedName })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {mode === "add"
              ? t("pages.agent.mcp.add_title", "Add MCP server")
              : t("pages.agent.mcp.edit_title", "Edit MCP server")}
          </DialogTitle>
          <DialogDescription>
            {mode === "add"
              ? t(
                  "pages.agent.mcp.add_description",
                  "Add an MCP server. Local stdio commands and remote HTTP/SSE endpoints are supported.",
                )
              : t("pages.agent.mcp.edit_description", {
                  name: form.name,
                  defaultValue: "Update the connection settings for {{name}}.",
                })}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          <label className="grid gap-2 text-sm font-medium" htmlFor="mcp-name">
            {t("pages.agent.mcp.name_label", "Name")}
            <Input
              id="mcp-name"
              value={form.name}
              onChange={(event) => update("name", event.target.value)}
              disabled={saving || mode === "edit"}
              autoFocus={mode === "add"}
            />
            {nameInUse && (
              <span className="text-destructive text-xs font-normal">
                {t("pages.agent.mcp.name_in_use")}
              </span>
            )}
          </label>

          <label
            className="grid gap-2 text-sm font-medium"
            htmlFor="mcp-transport"
          >
            {t("pages.agent.mcp.transport_label", "Transport")}
            <Select
              value={form.type}
              disabled={saving}
              onValueChange={(value) =>
                update("type", value as MCPServerTransport)
              }
            >
              <SelectTrigger id="mcp-transport">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="stdio">stdio</SelectItem>
                <SelectItem value="sse">sse</SelectItem>
                <SelectItem value="http">http</SelectItem>
              </SelectContent>
            </Select>
          </label>

          {form.type === "stdio" ? (
            <>
              <label
                className="grid gap-2 text-sm font-medium"
                htmlFor="mcp-command"
              >
                {t("pages.agent.mcp.command_label", "Command")}
                <Input
                  id="mcp-command"
                  value={form.command}
                  onChange={(event) => update("command", event.target.value)}
                  placeholder={t(
                    "pages.agent.mcp.command_placeholder",
                    "e.g. npx",
                  )}
                  disabled={saving}
                  autoFocus={mode === "edit"}
                />
              </label>

              <label
                className="grid gap-2 text-sm font-medium"
                htmlFor="mcp-args"
              >
                {t("pages.agent.mcp.args_label", "Arguments")}
                <Textarea
                  id="mcp-args"
                  value={form.argsText}
                  onChange={(event) => update("argsText", event.target.value)}
                  disabled={saving}
                  className="min-h-28 font-mono text-xs"
                />
                <span className="text-muted-foreground text-xs font-normal">
                  {t("pages.agent.mcp.args_hint", "One argument per line.")}
                </span>
              </label>

              <label
                className="grid gap-2 text-sm font-medium"
                htmlFor="mcp-env-file"
              >
                {t("pages.agent.mcp.env_file_label", "Environment file")}
                <Input
                  id="mcp-env-file"
                  value={form.envFile}
                  onChange={(event) => update("envFile", event.target.value)}
                  placeholder={t(
                    "pages.agent.mcp.env_file_placeholder",
                    "Environment file path (optional)",
                  )}
                  disabled={saving}
                />
              </label>

              <label
                className="grid gap-2 text-sm font-medium"
                htmlFor="mcp-env"
              >
                {t("pages.agent.mcp.env_label", "Environment variables (JSON)")}
                <Textarea
                  id="mcp-env"
                  value={form.envText}
                  onChange={(event) => update("envText", event.target.value)}
                  disabled={saving}
                  className="min-h-28 font-mono text-xs"
                />
              </label>

              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <Switch
                  size="sm"
                  checked={form.inheritEnv}
                  disabled={saving}
                  onCheckedChange={(checked) => update("inheritEnv", checked)}
                />
                {t(
                  "pages.agent.mcp.inherit_env_label",
                  "Inherit parent environment",
                )}
              </label>
            </>
          ) : (
            <>
              <label
                className="grid gap-2 text-sm font-medium"
                htmlFor="mcp-url"
              >
                {t("pages.agent.mcp.url_label", "Server URL")}
                <Input
                  id="mcp-url"
                  value={form.url}
                  onChange={(event) => update("url", event.target.value)}
                  placeholder={t(
                    "pages.agent.mcp.url_placeholder",
                    "https://example.com/mcp",
                  )}
                  disabled={saving}
                  autoFocus={mode === "edit"}
                />
              </label>

              <label
                className="grid gap-2 text-sm font-medium"
                htmlFor="mcp-headers"
              >
                {t("pages.agent.mcp.headers_label", "Headers (JSON)")}
                <Textarea
                  id="mcp-headers"
                  value={form.headersText}
                  onChange={(event) =>
                    update("headersText", event.target.value)
                  }
                  disabled={saving}
                  className="min-h-28 font-mono text-xs"
                />
              </label>
            </>
          )}

          <div className="bg-muted/30 flex flex-wrap items-center gap-x-5 gap-y-3 rounded-lg px-3 py-2.5">
            <label className="flex cursor-pointer items-center gap-2 text-sm">
              <Switch
                size="sm"
                checked={form.enabled}
                disabled={saving}
                onCheckedChange={(checked) => update("enabled", checked)}
              />
              {t("pages.agent.mcp.server_enabled", "Server enabled")}
            </label>
            <label className="flex items-center gap-2 text-sm">
              {t("pages.agent.mcp.deferred_mode_label", "Discovery mode")}
              <Select
                value={deferredValue}
                disabled={saving}
                onValueChange={(value) =>
                  update(
                    "deferred",
                    value === "inherit" ? null : value === "deferred",
                  )
                }
              >
                <SelectTrigger className="h-9">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="inherit">
                    {t(
                      "pages.agent.mcp.deferred_inherit",
                      "Follow global discovery mode",
                    )}
                  </SelectItem>
                  <SelectItem value="deferred">
                    {t("pages.agent.mcp.deferred_true", "Deferred discovery")}
                  </SelectItem>
                  <SelectItem value="eager">
                    {t("pages.agent.mcp.deferred_false", "Eager registration")}
                  </SelectItem>
                </SelectContent>
              </Select>
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
            <Button type="submit" disabled={saving || !canSubmit}>
              {saving && <IconLoader2 className="size-4 animate-spin" />}
              {mode === "add"
                ? t("pages.agent.mcp.add", "Add server")
                : t("pages.agent.mcp.save", "Save changes")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
