import {
  IconBook,
  IconCheck,
  IconCloud,
  IconCode,
  IconDatabase,
  IconExternalLink,
  IconLoader2,
  IconMicroscope,
  IconPalette,
  IconPlugConnected,
  IconPlus,
  IconSearch,
  IconTool,
  IconWorldWww,
} from "@tabler/icons-react"
import { useMutation } from "@tanstack/react-query"
import { useDeferredValue, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { installMCPServer } from "@/api/mcp"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
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

import {
  type MCPCatalogCategory,
  type MCPCatalogEntry,
  MCP_CATALOG,
  buildMCPCatalogServer,
} from "./catalog"

type CategoryFilter = MCPCatalogCategory | "all"

const CATEGORY_ORDER: MCPCatalogCategory[] = [
  "browser",
  "web",
  "docs",
  "code",
  "database",
  "productivity",
  "research",
  "design",
  "cloud",
  "utility",
]

const CATEGORY_ICONS: Record<
  MCPCatalogCategory,
  React.ComponentType<{ className?: string }>
> = {
  browser: IconWorldWww,
  web: IconSearch,
  docs: IconBook,
  code: IconCode,
  database: IconDatabase,
  productivity: IconPlugConnected,
  research: IconMicroscope,
  design: IconPalette,
  cloud: IconCloud,
  utility: IconTool,
}

interface MCPCatalogTabProps {
  installedNames: ReadonlySet<string>
  onInstalled: () => void
}

export function MCPCatalogTab({
  installedNames,
  onInstalled,
}: MCPCatalogTabProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState("")
  const deferredSearch = useDeferredValue(search)
  const [category, setCategory] = useState<CategoryFilter>("all")
  const [pendingEntry, setPendingEntry] = useState<MCPCatalogEntry | null>(null)

  const installMutation = useMutation({
    mutationFn: ({
      entry,
      values,
    }: {
      entry: MCPCatalogEntry
      values: Record<string, string>
    }) => installMCPServer(entry.name, buildMCPCatalogServer(entry, values)),
    onSuccess: (_data, variables) => {
      toast.success(
        t("pages.agent.mcp.catalog.install_success", {
          name: variables.entry.displayName,
        }),
      )
      setPendingEntry(null)
      onInstalled()
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t("pages.agent.mcp.catalog.install_error"),
      )
    },
  })

  const entries = useMemo(() => {
    const needle = deferredSearch.trim().toLowerCase()
    return MCP_CATALOG.filter((entry) => {
      if (category !== "all" && entry.category !== category) return false
      if (!needle) return true
      return [
        entry.displayName,
        entry.name,
        entry.description,
        entry.requires ?? "",
      ].some((value) => value.toLowerCase().includes(needle))
    })
  }, [category, deferredSearch])

  const pendingName = installMutation.isPending
    ? installMutation.variables?.entry.name
    : null

  const handleInstall = (entry: MCPCatalogEntry) => {
    if (entry.fields?.length) {
      setPendingEntry(entry)
      return
    }
    installMutation.mutate({ entry, values: {} })
  }

  return (
    <>
      <div className="flex-1 overflow-auto px-6 py-6 pb-20">
        <div className="animate-in fade-in mx-auto w-full max-w-6xl space-y-6 pt-2 duration-300 md:duration-500">
          <div className="flex flex-col gap-5 md:flex-row md:items-end md:justify-between">
            <div className="hidden max-w-sm space-y-2 md:block">
              <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
                {t("pages.agent.mcp.catalog.title", "MCP Catalog")}
              </h1>
              <p className="text-muted-foreground/80 text-[14px] leading-relaxed">
                {t(
                  "pages.agent.mcp.catalog.description",
                  "Install popular MCP servers with one click. You can review or remove them from the Servers tab.",
                )}
              </p>
            </div>
            <div className="flex w-full flex-col gap-3 sm:flex-row sm:items-center md:w-auto">
              <div className="group relative flex-1 md:w-80">
                <IconSearch className="text-muted-foreground/60 group-focus-within:text-foreground/80 absolute top-1/2 left-3.5 size-4 -translate-y-1/2 transition-colors" />
                <Input
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder={t(
                    "pages.agent.mcp.catalog.search_placeholder",
                    "Search the MCP catalog...",
                  )}
                  className="bg-muted/40 hover:bg-muted/60 focus-visible:bg-background focus-visible:border-border/80 focus-visible:ring-foreground/5 h-11 w-full rounded-xl border-transparent pl-10 shadow-none transition-all duration-300"
                />
              </div>
              <Select
                value={category}
                onValueChange={(value) => setCategory(value as CategoryFilter)}
              >
                <SelectTrigger className="bg-muted/40 hover:bg-muted/60 focus:ring-foreground/5 focus:border-border/80 h-11 w-full rounded-xl border-transparent shadow-none transition-all duration-300 sm:w-44">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className="border-border/40 rounded-xl shadow-lg">
                  <SelectItem value="all">
                    {t(
                      "pages.agent.mcp.catalog.categories.all",
                      "All categories",
                    )}
                  </SelectItem>
                  {CATEGORY_ORDER.map((value) => (
                    <SelectItem key={value} value={value}>
                      {t(`pages.agent.mcp.catalog.categories.${value}`, value)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {entries.length === 0 ? (
            <div className="border-border/40 flex flex-col items-center justify-center rounded-2xl border border-dashed px-6 py-16 text-center">
              <div className="bg-muted/30 ring-border/10 mb-6 rounded-full p-6 shadow-xs ring-1">
                <IconSearch className="text-muted-foreground/60 size-8" />
              </div>
              <h3 className="text-foreground/80 mb-2 text-xl font-semibold tracking-tight">
                {t(
                  "pages.agent.mcp.catalog.no_results",
                  "No MCP servers match your search",
                )}
              </h3>
              <p className="text-muted-foreground max-w-sm text-sm leading-relaxed">
                {t(
                  "pages.agent.mcp.catalog.no_results_hint",
                  "Try a different keyword or category.",
                )}
              </p>
            </div>
          ) : (
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
              {entries.map((entry) => (
                <CatalogCard
                  key={entry.name}
                  entry={entry}
                  installed={installedNames.has(entry.name)}
                  pending={pendingName === entry.name}
                  disabled={installMutation.isPending}
                  onInstall={() => handleInstall(entry)}
                />
              ))}
            </div>
          )}
        </div>
      </div>

      {pendingEntry && (
        <InstallCatalogDialog
          key={pendingEntry.name}
          entry={pendingEntry}
          installing={pendingName === pendingEntry.name}
          onClose={() => {
            if (!installMutation.isPending) setPendingEntry(null)
          }}
          onInstall={(values) =>
            installMutation.mutate({ entry: pendingEntry, values })
          }
        />
      )}
    </>
  )
}

function CatalogCard({
  entry,
  installed,
  pending,
  disabled,
  onInstall,
}: {
  entry: MCPCatalogEntry
  installed: boolean
  pending: boolean
  disabled: boolean
  onInstall: () => void
}) {
  const { t } = useTranslation()
  const CategoryIcon = CATEGORY_ICONS[entry.category]

  return (
    <Card className="border-border/40 hover:border-border/80 gap-0 py-0 shadow-none transition-all duration-300 hover:-translate-y-[2px] hover:shadow-[0_4px_20px_-4px_rgba(0,0,0,0.05)] sm:rounded-2xl dark:hover:shadow-[0_4px_20px_-4px_rgba(255,255,255,0.02)]">
      <CardContent className="flex h-full flex-col gap-4 px-5 py-5">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className="bg-muted/60 flex size-10 shrink-0 items-center justify-center rounded-xl">
              <CategoryIcon className="text-muted-foreground size-5" />
            </div>
            <div className="min-w-0">
              <h3 className="text-foreground/90 truncate font-semibold tracking-tight">
                {entry.displayName}
              </h3>
              <Badge
                variant="outline"
                className="text-muted-foreground mt-1 uppercase"
              >
                {t(
                  `pages.agent.mcp.catalog.categories.${entry.category}`,
                  entry.category,
                )}
              </Badge>
            </div>
          </div>
          {installed && (
            <Badge
              variant="secondary"
              className="shrink-0 border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
            >
              <IconCheck />
              {t("pages.agent.mcp.catalog.installed", "Installed")}
            </Badge>
          )}
        </div>

        <p className="text-muted-foreground/80 flex-1 text-[14px] leading-relaxed">
          {entry.description}
        </p>

        {entry.requires && (
          <p className="text-muted-foreground/70 text-[12px] leading-relaxed">
            {t("pages.agent.mcp.catalog.requires", {
              defaultValue: "Requires: {{value}}",
              value: entry.requires,
            })}
          </p>
        )}

        <div className="flex items-center justify-between gap-2 pt-1">
          <a
            href={entry.docsUrl}
            target="_blank"
            rel="noreferrer"
            className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs transition-colors"
          >
            {t("pages.agent.mcp.catalog.docs", "Documentation")}
            <IconExternalLink className="size-3.5" />
          </a>
          <Button
            type="button"
            size="sm"
            variant={installed ? "outline" : "default"}
            disabled={installed || disabled}
            onClick={onInstall}
          >
            {pending ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : installed ? (
              <IconCheck className="size-4" />
            ) : (
              <IconPlus className="size-4" />
            )}
            {installed
              ? t("pages.agent.mcp.catalog.installed", "Installed")
              : t("pages.agent.mcp.catalog.install", "Install")}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function InstallCatalogDialog({
  entry,
  installing,
  onClose,
  onInstall,
}: {
  entry: MCPCatalogEntry
  installing: boolean
  onClose: () => void
  onInstall: (values: Record<string, string>) => void
}) {
  const { t } = useTranslation()
  const [values, setValues] = useState<Record<string, string>>({})
  const fields = entry.fields ?? []
  const canInstall = fields.every(
    (field) => !field.required || Boolean(values[field.key]?.trim()),
  )

  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!canInstall) return
    onInstall(values)
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !installing && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>
            {t("pages.agent.mcp.catalog.install_title", {
              defaultValue: "Install {{name}}",
              name: entry.displayName,
            })}
          </DialogTitle>
          <DialogDescription>
            {t(
              "pages.agent.mcp.catalog.install_description",
              "Fill in the credentials required by this MCP server. You can update them later in the configuration.",
            )}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={submit}>
          {fields.map((field) => (
            <label
              key={field.key}
              className="grid gap-2 text-sm font-medium"
              htmlFor={`mcp-catalog-${field.key}`}
            >
              {field.label}
              {field.required ? (
                <span className="text-muted-foreground text-xs font-normal">
                  {t("pages.agent.mcp.catalog.required", "Required")}
                </span>
              ) : null}
              <Input
                id={`mcp-catalog-${field.key}`}
                type={field.secret ? "password" : "text"}
                value={values[field.key] ?? ""}
                placeholder={field.placeholder}
                autoComplete="off"
                disabled={installing}
                autoFocus={field === fields[0]}
                onChange={(event) =>
                  setValues((current) => ({
                    ...current,
                    [field.key]: event.target.value,
                  }))
                }
              />
            </label>
          ))}
          <p className="text-muted-foreground text-xs leading-relaxed">
            {t(
              "pages.agent.mcp.catalog.reload_hint",
              "After installing, reload the gateway to connect the new server.",
            )}
          </p>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={installing}
            >
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={installing || !canInstall}>
              {installing && <IconLoader2 className="size-4 animate-spin" />}
              {t("pages.agent.mcp.catalog.install", "Install")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
