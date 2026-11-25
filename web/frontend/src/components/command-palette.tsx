import {
  IconAtom,
  IconKey,
  IconListDetails,
  IconMessageCircle,
  IconPlugConnected,
  IconPlus,
  IconSearch,
  IconSettings,
  IconSparkles,
  IconTerminal2,
  IconTools,
  IconUsers,
  type TablerIcon,
} from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import { useSetAtom, useStore } from "jotai"
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { type SlashCommand, listSlashCommands } from "@/api/commands"
import { type ModelInfo, getModels, setDefaultModel } from "@/api/models"
import { type SessionSummary, getSessions } from "@/api/sessions"
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandShortcut,
} from "@/components/ui/command"
import {
  type SlashPaletteEntry,
  nextSlashRecents,
  readSlashRecents,
  slashCommandFallbackTitle,
  slashCommandI18nKey,
  slashCommandInsertion,
  storeSlashRecents,
} from "@/features/chat/slash-commands"
import { usePicoChat } from "@/hooks/use-pico-chat"
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { composerInsertionAtom } from "@/store/chat"
import { gatewayAtom } from "@/store/gateway"
import { modelsRevisionAtom } from "@/store/models"

const SESSION_LIMIT = 50

type PagePath =
  | "/"
  | "/models"
  | "/credentials"
  | "/agent/hub"
  | "/agent/skills"
  | "/agent/tools"
  | "/agent/subagents"
  | "/agent/mcp"
  | "/config"
  | "/logs"

interface PalettePage {
  labelKey: string
  to: PagePath
  icon: TablerIcon
}

const PALETTE_PAGES: PalettePage[] = [
  { labelKey: "navigation.chat", to: "/", icon: IconMessageCircle },
  { labelKey: "navigation.models", to: "/models", icon: IconAtom },
  { labelKey: "navigation.credentials", to: "/credentials", icon: IconKey },
  { labelKey: "navigation.hub", to: "/agent/hub", icon: IconSearch },
  { labelKey: "navigation.skills", to: "/agent/skills", icon: IconSparkles },
  { labelKey: "navigation.tools", to: "/agent/tools", icon: IconTools },
  {
    labelKey: "navigation.subagents",
    to: "/agent/subagents",
    icon: IconUsers,
  },
  { labelKey: "navigation.mcp", to: "/agent/mcp", icon: IconPlugConnected },
  { labelKey: "navigation.config", to: "/config", icon: IconSettings },
  { labelKey: "navigation.logs", to: "/logs", icon: IconListDetails },
]

function selectableModels(models: ModelInfo[]): ModelInfo[] {
  const seen = new Set<string>()
  const result: ModelInfo[] = []
  for (const model of models) {
    if (model.default_model_allowed === false || model.is_virtual) continue
    if (model.status === "unconfigured") continue
    if (seen.has(model.model_name)) continue
    seen.add(model.model_name)
    result.push(model)
  }
  return result
}

export function CommandPalette() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const jotaiStore = useStore()
  const { activeSessionId, newChat, switchSession } = usePicoChat()
  const setComposerInsertion = useSetAtom(composerInsertionAtom)
  const bumpModelsRevision = useSetAtom(modelsRevisionAtom)
  const [open, setOpen] = useState(false)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [models, setModels] = useState<ModelInfo[]>([])
  const [defaultModelName, setDefaultModelName] = useState("")
  const [slashCommands, setSlashCommands] = useState<SlashCommand[]>([])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey) return
      if (event.key.toLowerCase() !== "k") return
      event.preventDefault()
      setOpen((current) => !current)
    }

    window.addEventListener("keydown", handleKeyDown)
    return () => window.removeEventListener("keydown", handleKeyDown)
  }, [])

  useEffect(() => {
    if (!open) return
    let cancelled = false

    void getSessions(0, SESSION_LIMIT)
      .then((data) => {
        if (!cancelled) setSessions(data)
      })
      .catch((error: unknown) => {
        console.error("Failed to load command palette sessions:", error)
      })

    void getModels()
      .then((data) => {
        if (cancelled) return
        setModels(data.models)
        setDefaultModelName(data.default_model)
      })
      .catch((error: unknown) => {
        console.error("Failed to load command palette models:", error)
      })

    void listSlashCommands()
      .then((data) => {
        if (!cancelled) setSlashCommands(data)
      })
      .catch((error: unknown) => {
        console.error("Failed to load command palette slash commands:", error)
      })

    return () => {
      cancelled = true
    }
  }, [open])

  const modelOptions = useMemo(() => selectableModels(models), [models])

  const handleNewChat = () => {
    setOpen(false)
    void newChat()
    void navigate({ to: "/" })
  }

  const handleSelectSession = (sessionId: string) => {
    setOpen(false)
    if (sessionId !== activeSessionId) void switchSession(sessionId)
    void navigate({ to: "/" })
  }

  const handleSelectModel = (modelName: string) => {
    setOpen(false)
    if (modelName === defaultModelName) return

    void (async () => {
      try {
        await setDefaultModel(modelName)
        setDefaultModelName(modelName)
        bumpModelsRevision((revision) => revision + 1)
        showSaveSuccessOrRestartToast(
          t,
          t("models.defaultChangeSuccess"),
          modelName,
          jotaiStore.get(gatewayAtom).restartRequired,
        )
      } catch (error) {
        console.error("Failed to set default model:", error)
        toast.error(
          error instanceof Error ? error.message : t("models.loadError"),
        )
      }
    })()
  }

  const handleSelectPage = (to: PagePath) => {
    setOpen(false)
    void navigate({ to })
  }

  const handleInsertCommand = (command: SlashCommand) => {
    const key = slashCommandI18nKey(command.command)
    const entry: SlashPaletteEntry = {
      command: command.command,
      title: t(`chat.slash.commands.${key}.title`, {
        defaultValue: slashCommandFallbackTitle(command.command),
      }),
      description: t(`chat.slash.commands.${key}.description`, {
        defaultValue: command.description,
      }),
      argHint: command.arg_hint ?? "",
    }

    storeSlashRecents(nextSlashRecents(readSlashRecents(), command.command))
    setComposerInsertion({ text: slashCommandInsertion(entry) })
    setOpen(false)
    void navigate({ to: "/" })
  }

  return (
    <CommandDialog
      open={open}
      onOpenChange={setOpen}
      title={t("commandPalette.title")}
      description={t("commandPalette.description")}
    >
      <CommandInput autoFocus placeholder={t("commandPalette.placeholder")} />
      <CommandList className="max-h-[min(60vh,26rem)]">
        <CommandEmpty>{t("commandPalette.empty")}</CommandEmpty>
        <CommandGroup heading={t("commandPalette.groups.actions")}>
          <CommandItem
            value={`new-chat ${t("commandPalette.newChat")}`}
            onSelect={handleNewChat}
          >
            <IconPlus />
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="truncate">{t("commandPalette.newChat")}</span>
              <span className="text-muted-foreground truncate text-xs">
                {t("commandPalette.newChatDescription")}
              </span>
            </div>
          </CommandItem>
        </CommandGroup>

        {sessions.length > 0 && (
          <CommandGroup heading={t("commandPalette.groups.sessions")}>
            {sessions.map((session) => (
              <CommandItem
                key={session.id}
                value={`${session.id} ${session.title}`}
                onSelect={() => handleSelectSession(session.id)}
              >
                <IconMessageCircle />
                <span className="min-w-0 flex-1 truncate">{session.title}</span>
                {session.id === activeSessionId && (
                  <CommandShortcut>
                    {t("commandPalette.current")}
                  </CommandShortcut>
                )}
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {modelOptions.length > 0 && (
          <CommandGroup heading={t("commandPalette.groups.models")}>
            {modelOptions.map((model) => (
              <CommandItem
                key={model.model_name}
                value={`${model.model_name} ${model.provider ?? ""}`}
                onSelect={() => handleSelectModel(model.model_name)}
              >
                <IconAtom />
                <span className="min-w-0 flex-1 truncate">
                  {model.model_name}
                </span>
                {model.model_name === defaultModelName && (
                  <CommandShortcut>
                    {t("commandPalette.current")}
                  </CommandShortcut>
                )}
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        <CommandGroup heading={t("commandPalette.groups.pages")}>
          {PALETTE_PAGES.map((page) => {
            const Icon = page.icon
            return (
              <CommandItem
                key={page.to}
                value={`${t(page.labelKey)} ${page.to}`}
                onSelect={() => handleSelectPage(page.to)}
              >
                <Icon />
                <span className="min-w-0 flex-1 truncate">
                  {t(page.labelKey)}
                </span>
                <CommandShortcut>{page.to}</CommandShortcut>
              </CommandItem>
            )
          })}
        </CommandGroup>

        {slashCommands.length > 0 && (
          <CommandGroup heading={t("commandPalette.groups.slashCommands")}>
            {slashCommands.map((command) => {
              const key = slashCommandI18nKey(command.command)
              const title = t(`chat.slash.commands.${key}.title`, {
                defaultValue: slashCommandFallbackTitle(command.command),
              })
              const description = t(`chat.slash.commands.${key}.description`, {
                defaultValue: command.description,
              })
              return (
                <CommandItem
                  key={command.command}
                  value={`${command.command} ${title} ${description}`}
                  onSelect={() => handleInsertCommand(command)}
                >
                  <IconTerminal2 />
                  <div className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate">{title}</span>
                    <span className="text-muted-foreground truncate text-xs">
                      {description}
                    </span>
                  </div>
                  <CommandShortcut>{command.command}</CommandShortcut>
                </CommandItem>
              )
            })}
          </CommandGroup>
        )}
      </CommandList>
    </CommandDialog>
  )
}
