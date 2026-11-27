import {
  IconBrain,
  IconCheck,
  IconChevronDown,
  IconClockHour4,
  IconCode,
  IconCoins,
  IconCopy,
  IconDownload,
  IconFileText,
  IconFolder,
  IconFolderSearch,
  IconList,
  IconMessageCircle,
  IconPhoto,
  IconPhotoPlus,
  IconPuzzle,
  IconSearch,
  IconTerminal2,
  IconTool,
  IconUsers,
  IconVolume,
  IconWorld,
} from "@tabler/icons-react"
import { type ReactNode, memo } from "react"
import { useTranslation } from "react-i18next"
import ReactMarkdown from "react-markdown"
import rehypeHighlight from "rehype-highlight"
import rehypeRaw from "rehype-raw"
import rehypeSanitize from "rehype-sanitize"
import remarkGfm from "remark-gfm"

import { MarkdownCodeBlock } from "@/components/chat/message-code-block"
import { TurnStats } from "@/components/chat/turn-stats"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import {
  estimateUsageCostUsd,
  formatTokenCount,
  formatTokensPerSecond,
  formatUsageCostUsd,
  tokensPerSecond,
} from "@/features/chat/usage"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { formatMessageTime } from "@/hooks/use-pico-chat"
import { cn } from "@/lib/utils"
import {
  type AssistantMessageKind,
  type ChatAttachment,
  type ChatToolCall,
  type TokenUsage,
} from "@/store/chat"

interface AssistantMessageProps {
  content: string
  attachments?: ChatAttachment[]
  kind?: AssistantMessageKind
  modelName?: string
  isDefaultModel?: boolean
  onFork?: () => void
  onOpenFile?: (path: string) => void
  filePathAliases?: Record<string, string>
  toolCalls?: ChatToolCall[]
  timestamp?: string | number
  usage?: TokenUsage
}

function ForkArrowIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <path d="M16 3h5v5" />
      <path d="M8 3H3v5" />
      <path d="m21 3-7.536 7.536A5 5 0 0 0 12 14.07V21" />
      <path d="m3 3 7.536 7.536A5 5 0 0 1 12 14.07V15" />
    </svg>
  )
}

type ToolDisplay = {
  label: string
  icon: typeof IconTool
  fields?: string[]
}

const FILE_TOOLS = new Set([
  "read_file",
  "read_file_lines",
  "write_file",
  "edit_file",
  "append_file",
])

const TOOL_DISPLAYS: Record<string, ToolDisplay> = {
  read_file: { label: "Read file", icon: IconFileText, fields: ["path"] },
  read_file_lines: { label: "Read file", icon: IconFileText, fields: ["path"] },
  write_file: { label: "Write file", icon: IconFileText, fields: ["path"] },
  edit_file: { label: "Edit file", icon: IconFileText, fields: ["path"] },
  append_file: {
    label: "Append to file",
    icon: IconFileText,
    fields: ["path"],
  },
  send_file: { label: "Send file", icon: IconFileText, fields: ["path"] },
  list_dir: { label: "List directory", icon: IconFolder, fields: ["path"] },
  search_files: {
    label: "Find files",
    icon: IconFolderSearch,
    fields: ["glob", "query", "pattern", "path"],
  },
  regex_search: {
    label: "Search tools",
    icon: IconSearch,
    fields: ["pattern"],
  },
  bm25_search: { label: "Search tools", icon: IconSearch, fields: ["query"] },
  web_search: { label: "Search web", icon: IconWorld, fields: ["query"] },
  web_fetch: { label: "Read webpage", icon: IconWorld, fields: ["url"] },
  exec: { label: "Run command", icon: IconTerminal2, fields: ["command"] },
  spawn: { label: "Delegate task", icon: IconUsers, fields: ["label", "task"] },
  delegate: {
    label: "Delegate task",
    icon: IconUsers,
    fields: ["agent_id", "task"],
  },
  subagent: {
    label: "Run subagent",
    icon: IconUsers,
    fields: ["label", "task"],
  },
  cron: {
    label: "Manage schedule",
    icon: IconClockHour4,
    fields: ["name", "action"],
  },
  image_generate: { label: "Generate image", icon: IconPhotoPlus },
  load_image: { label: "Read image", icon: IconPhoto, fields: ["path"] },
  send_tts: { label: "Generate audio", icon: IconVolume },
  message: {
    label: "Send message",
    icon: IconMessageCircle,
    fields: ["channel"],
  },
  reaction: {
    label: "Add reaction",
    icon: IconMessageCircle,
    fields: ["channel"],
  },
  todo: { label: "Update task list", icon: IconList, fields: ["action"] },
  find_skills: { label: "Find skills", icon: IconPuzzle, fields: ["query"] },
  install_skill: {
    label: "Install skill",
    icon: IconPuzzle,
    fields: ["slug", "name"],
  },
  i2c: { label: "Use I²C", icon: IconCode, fields: ["action"] },
  spi: { label: "Use SPI", icon: IconCode, fields: ["action"] },
  serial: { label: "Use serial port", icon: IconCode, fields: ["action"] },
}

function getToolDisplay(
  toolName: string,
  rawArguments: string,
): ToolDisplay & {
  detail: string
  filePath?: string
  href?: string
  faviconUrl?: string
} {
  const display = TOOL_DISPLAYS[toolName] ?? {
    label: toolName
      .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
      .replace(/[._-]+/g, " ")
      .replace(/\s+/g, " ")
      .trim(),
    icon: IconTool,
    fields: ["path", "query", "url", "action", "name"],
  }
  let args: Record<string, unknown> = {}

  try {
    const parsed: unknown = JSON.parse(rawArguments)
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      args = parsed as Record<string, unknown>
    }
  } catch {
    // Keep the tool label useful when the arguments are incomplete JSON.
  }

  const action =
    typeof args.action === "string" ? args.action.toLowerCase() : ""
  const label =
    toolName === "cron"
      ? action === "add"
        ? "Schedule task"
        : action === "remove"
          ? "Remove scheduled task"
          : "Manage schedule"
      : toolName === "exec"
        ? ({
            list: "Check command sessions",
            poll: "Check command",
            read: "Read command output",
            write: "Send command input",
            kill: "Stop command",
            "send-keys": "Send command keys",
          }[action] ?? display.label)
        : toolName === "todo" && action === "read"
          ? "Read task list"
          : display.label

  const rawValue = display.fields
    ?.map((field) => args[field])
    .find(
      (field): field is string => typeof field === "string" && !!field.trim(),
    )
  const value = rawValue
    ? FILE_TOOLS.has(toolName)
      ? rawValue.trim()
      : rawValue.replace(/\s+/g, " ").trim()
    : undefined
  const webUrl = toolName === "web_fetch" ? safeWebUrl(args.url) : undefined

  return {
    ...display,
    label,
    detail: value ?? "",
    filePath:
      FILE_TOOLS.has(toolName) && typeof args.path === "string"
        ? args.path
        : undefined,
    href: webUrl?.href,
    faviconUrl: webUrl
      ? `https://www.google.com/s2/favicons?domain=${encodeURIComponent(webUrl.hostname)}&sz=32`
      : undefined,
  }
}

function safeWebUrl(value: unknown): URL | undefined {
  if (typeof value !== "string") return undefined
  try {
    const url = new URL(value)
    return url.protocol === "http:" || url.protocol === "https:"
      ? url
      : undefined
  } catch {
    return undefined
  }
}

const FILE_REFERENCE_PATTERN =
  /[A-Za-z]:\\[A-Za-z0-9._@+-]+(?:\\[A-Za-z0-9._@+-]+)*|(?:~\/|\/|\.{1,2}\/)[A-Za-z0-9._@+-]+(?:\/[A-Za-z0-9._@+-]+)*|(?:[A-Za-z0-9._@+-]+\/)+[A-Za-z0-9._@+-]+|[A-Za-z0-9_@+-]+\.(?:txt|md|yaml|yml|json|go|py|ts|tsx|js|jsx|css|html|toml|sh|pdf|csv|xml|sql|log|conf|mod|sum|env|lock|ini|cfg|rs|c|h|cpp|java)\b/gi

function filePathMatches(value: string) {
  const matches: Array<{ start: number; end: number; path: string }> = []
  const pattern = new RegExp(FILE_REFERENCE_PATTERN.source, "gi")
  for (const match of value.matchAll(pattern)) {
    const start = match.index ?? 0
    const before = value[start - 1]
    if (before === ":" || before === "/") continue
    const path = match[0].replace(/[.,;:!?]+$/, "")
    if (path) matches.push({ start, end: start + path.length, path })
  }
  return matches
}

function isFilePath(value: string) {
  const trimmed = value.trim()
  const [match] = filePathMatches(trimmed)
  return !!match && match.start === 0 && match.end === trimmed.length
}

function resolveFilePath(path: string, aliases?: Record<string, string>) {
  return path.includes("/") || path.includes("\\")
    ? path
    : (aliases?.[path] ?? path)
}

function filePreviewLink(path: string, aliases?: Record<string, string>) {
  const label = path.replace(/[\\[\]]/g, "\\$&")
  const target = resolveFilePath(path, aliases)
  return `[${label}](/api/files/preview?path=${encodeURIComponent(target)})`
}

function linkifyFileText(value: string, aliases?: Record<string, string>) {
  let result = ""
  let offset = 0
  for (const { start, end, path } of filePathMatches(value)) {
    const prefix = value.slice(0, start)
    if (prefix.lastIndexOf("](") > prefix.lastIndexOf(")")) continue
    result += value.slice(offset, start) + filePreviewLink(path, aliases)
    offset = end
  }
  return offset > 0 ? result + value.slice(offset) : value
}

function linkifyMarkdownPaths(value: string, aliases?: Record<string, string>) {
  return value
    .split(/(```[\s\S]*?```|~~~[\s\S]*?~~~)/g)
    .map((block) => {
      if (block.startsWith("```") || block.startsWith("~~~")) return block
      return block
        .split(/(`+[^`\n]*`+)/g)
        .map((segment) => {
          if (!segment.startsWith("`")) return linkifyFileText(segment, aliases)
          const code = segment.match(/^(`+)([\s\S]*)\1$/)
          return code && isFilePath(code[2])
            ? filePreviewLink(code[2].trim(), aliases)
            : segment
        })
        .join("")
    })
    .join("")
}

function FilePathText({
  text,
  onOpenFile,
  filePath,
  filePathAliases,
}: {
  text: string
  onOpenFile?: (path: string) => void
  filePath?: string
  filePathAliases?: Record<string, string>
}) {
  if (filePath && onOpenFile) {
    const resolvedPath = resolveFilePath(filePath, filePathAliases)
    return (
      <button
        type="button"
        className="text-primary hover:text-primary/80 inline cursor-pointer bg-transparent p-0 align-baseline font-mono text-[inherit] underline decoration-current/40 underline-offset-2"
        title={resolvedPath}
        onClick={(event) => {
          event.stopPropagation()
          onOpenFile(resolvedPath)
        }}
      >
        {text}
      </button>
    )
  }
  const matches = filePathMatches(text)
  if (!onOpenFile || matches.length === 0) return text

  const parts: ReactNode[] = []
  let offset = 0
  for (const { start, end, path } of matches) {
    const resolvedPath = resolveFilePath(path, filePathAliases)
    parts.push(text.slice(offset, start))
    parts.push(
      <button
        key={`${start}-${path}`}
        type="button"
        className="text-primary hover:text-primary/80 inline cursor-pointer bg-transparent p-0 align-baseline font-mono text-[inherit] underline decoration-current/40 underline-offset-2"
        title={resolvedPath}
        onClick={(event) => {
          event.stopPropagation()
          onOpenFile(resolvedPath)
        }}
      >
        {path}
      </button>,
    )
    offset = end
  }
  parts.push(text.slice(offset))
  return parts
}

function resolveMarkdownFileLinkPath(
  href: string | undefined,
  onOpenFile: ((path: string) => void) | undefined,
  filePathAliases: Record<string, string> | undefined,
): string | null {
  if (!href || !onOpenFile) {
    return null
  }
  try {
    const url = new URL(href, window.location.origin)
    const requestedPath =
      url.pathname === "/api/files/preview"
        ? url.searchParams.get("path")
        : !/^(?:[a-z]+:|\/\/|#)/i.test(href) &&
            isFilePath(decodeURIComponent(href))
          ? decodeURIComponent(href)
          : null
    return requestedPath
      ? resolveFilePath(requestedPath, filePathAliases)
      : null
  } catch {
    // Keep malformed or external markdown links as ordinary links.
    return null
  }
}

function MarkdownFileLink({
  href,
  children,
  onOpenFile,
  filePathAliases,
}: {
  href?: string
  children: ReactNode
  onOpenFile?: (path: string) => void
  filePathAliases?: Record<string, string>
}) {
  const path = resolveMarkdownFileLinkPath(href, onOpenFile, filePathAliases)
  if (path && onOpenFile) {
    return (
      <button
        type="button"
        className="text-primary hover:text-primary/80 cursor-pointer bg-transparent p-0 underline decoration-current/40 underline-offset-2"
        onClick={() => onOpenFile(path)}
      >
        {children}
      </button>
    )
  }
  return (
    <a href={href} target="_blank" rel="noreferrer noopener">
      {children}
    </a>
  )
}

function WebFavicon({ src }: { src: string }) {
  return (
    <span className="relative size-3.5 shrink-0">
      <IconWorld className="text-muted-foreground absolute inset-0 size-3.5" />
      <img
        src={src}
        alt=""
        loading="lazy"
        referrerPolicy="no-referrer"
        className="bg-background absolute inset-0 size-full rounded-sm object-contain"
        onError={(event) => {
          event.currentTarget.hidden = true
        }}
      />
    </span>
  )
}

function formatToolInput(value: string) {
  if (!value.trim()) return "{}"
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function ReasoningItem({
  content,
  onOpenFile,
  filePathAliases,
}: {
  content: string
  onOpenFile?: (path: string) => void
  filePathAliases?: Record<string, string>
}) {
  const { t } = useTranslation()

  return (
    <Collapsible className="group/reasoning min-w-0">
      <CollapsibleTrigger className="hover:bg-muted/40 focus-visible:ring-ring flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] leading-5 focus-visible:ring-2 focus-visible:outline-none">
        <IconChevronDown className="text-muted-foreground size-3.5 shrink-0 transition-transform group-data-[state=closed]/reasoning:-rotate-90" />
        <IconBrain className="text-muted-foreground size-3.5 shrink-0" />
        <span className="text-foreground/80 text-[13px] leading-5 font-medium">
          {t("chat.reasoningLabel")}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="text-muted-foreground ml-5 max-h-96 overflow-auto border-l py-1 pl-3 text-[13px] leading-6 [overflow-wrap:anywhere] whitespace-pre-wrap">
          <FilePathText
            text={content}
            onOpenFile={onOpenFile}
            filePathAliases={filePathAliases}
          />
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

function ToolCallItem({
  toolCall,
  onOpenFile,
  filePathAliases,
}: {
  toolCall: ChatToolCall
  onOpenFile?: (path: string) => void
  filePathAliases?: Record<string, string>
}) {
  const { t } = useTranslation()
  const name = toolCall.function?.name?.trim() ?? ""
  const argumentsText = toolCall.function?.arguments?.trim() ?? ""
  const display = getToolDisplay(name, argumentsText)
  const Icon = display.icon
  const input = formatToolInput(toolCall.input ?? argumentsText)
  const output = toolCall.output ?? ""
  const triggerLabel = [display.label, display.detail].filter(Boolean).join(" ")

  return (
    <Collapsible className="group/tool min-w-0">
      <CollapsibleTrigger
        aria-label={triggerLabel}
        className="hover:bg-muted/40 focus-visible:ring-ring flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left focus-visible:ring-2 focus-visible:outline-none"
      >
        <IconChevronDown className="text-muted-foreground size-3.5 shrink-0 transition-transform group-data-[state=closed]/tool:-rotate-90" />
        {display.href && display.faviconUrl ? (
          <WebFavicon src={display.faviconUrl} />
        ) : (
          <Icon className="text-muted-foreground size-3.5 shrink-0" />
        )}
        <span className="text-foreground/80 shrink-0 text-[13px] leading-5 font-medium">
          {display.label}
        </span>
        {display.detail && (
          <span
            className={cn(
              "text-muted-foreground min-w-0 flex-1 truncate text-xs",
              name === "exec" && "font-mono",
            )}
          >
            {display.detail}
          </span>
        )}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="ml-5 min-w-0 space-y-3 border-l py-2 pl-3">
          {toolCall.extraContent?.toolFeedbackExplanation && (
            <div className="text-muted-foreground text-[13px] leading-6 [overflow-wrap:anywhere] whitespace-pre-wrap">
              <div className="text-foreground/70 mb-1 text-xs font-medium">
                {t("chat.toolCallExplanationLabel")}
              </div>
              <FilePathText
                text={toolCall.extraContent.toolFeedbackExplanation}
                onOpenFile={onOpenFile}
                filePathAliases={filePathAliases}
              />
            </div>
          )}
          <div className="min-w-0">
            <div className="text-foreground/70 mb-1 text-xs font-medium">
              {t("chat.toolInputLabel")}
            </div>
            <pre className="bg-muted/40 text-muted-foreground max-h-80 overflow-auto rounded-md border p-3 font-mono text-xs leading-5 [overflow-wrap:anywhere] whitespace-pre-wrap">
              {input}
            </pre>
          </div>
          <div className="min-w-0">
            <div className="text-foreground/70 mb-1 text-xs font-medium">
              {t("chat.toolOutputLabel")}
            </div>
            <pre className="bg-muted/40 text-muted-foreground max-h-80 overflow-auto rounded-md border p-3 font-mono text-xs leading-5 [overflow-wrap:anywhere] whitespace-pre-wrap">
              {output || t("chat.toolOutputEmpty")}
            </pre>
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

export const AssistantMessage = memo(function AssistantMessage({
  content,
  attachments = [],
  kind = "normal",
  modelName,
  isDefaultModel = false,
  onFork,
  onOpenFile,
  filePathAliases,
  toolCalls = [],
  timestamp = "",
  usage,
}: AssistantMessageProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const isThought = kind === "thought"
  const isToolCalls = kind === "tool_calls"
  const isToolFeedback = kind === "tool_feedback"
  const hasText = content.trim().length > 0
  const hasToolCalls = toolCalls.length > 0
  const imageAttachments = attachments.filter(
    (attachment) => attachment.type === "image",
  )
  const fileAttachments = attachments.filter(
    (attachment) => attachment.type !== "image",
  )
  const formattedTimestamp =
    timestamp !== "" ? formatMessageTime(timestamp) : ""
  const copyMessageLabel = isCopied
    ? t("chat.copiedLabel")
    : t("chat.copyMessage")
  const forkMessageLabel = t("chat.forkAtMessage")
  const hasUsage =
    usage !== undefined &&
    (usage.total_tokens > 0 || usage.cost_usd !== undefined)
  const usageInput = usage?.input_tokens
  const usageOutput = usage?.output_tokens
  const usageRequests = usage?.requests
  const usageContextTokens = usage?.context_tokens
  const usageTokensPerSecond = usage ? tokensPerSecond(usage) : undefined
  const usageCostUsd = usage
    ? estimateUsageCostUsd(usage, modelName)
    : undefined
  const usageCostIsEstimated =
    usage !== undefined &&
    usage.cost_usd === undefined &&
    usageCostUsd !== undefined

  return (
    <div
      className={cn(
        "group flex w-full flex-col",
        isThought || isToolCalls || isToolFeedback ? "gap-0" : "gap-1.5",
      )}
    >
      {((hasText && !isThought && !isToolFeedback) || hasToolCalls) && (
        <div
          className={cn(
            !isToolCalls && "relative overflow-hidden rounded-xl border",
            "text-card-foreground border-transparent bg-transparent",
          )}
        >
          {isToolCalls && hasToolCalls && (
            <div className="space-y-0.5">
              {toolCalls.map((toolCall, index) => (
                <ToolCallItem
                  key={
                    toolCall.id ??
                    `${toolCall.function?.name ?? "tool"}-${index}`
                  }
                  toolCall={toolCall}
                  onOpenFile={onOpenFile}
                  filePathAliases={filePathAliases}
                />
              ))}
            </div>
          )}
          {!isThought && !isToolCalls && !isToolFeedback && hasText && (
            <div
              className={cn(
                "prose dark:prose-invert prose-pre:my-2 prose-pre:overflow-x-auto prose-pre:rounded-lg prose-pre:border prose-pre:bg-zinc-100 prose-pre:p-0 prose-pre:text-zinc-900 dark:prose-pre:bg-zinc-950 dark:prose-pre:text-zinc-100 max-w-none [overflow-wrap:anywhere] break-words",
                "prose-p:my-2 prose-p:whitespace-pre-wrap py-1 text-[16px] leading-relaxed",
              )}
            >
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                rehypePlugins={[rehypeRaw, rehypeSanitize, rehypeHighlight]}
                components={{
                  pre: MarkdownCodeBlock,
                  a: ({ href, children }) => (
                    <MarkdownFileLink
                      href={href}
                      onOpenFile={onOpenFile}
                      filePathAliases={filePathAliases}
                    >
                      {children}
                    </MarkdownFileLink>
                  ),
                }}
              >
                {linkifyMarkdownPaths(content, filePathAliases)}
              </ReactMarkdown>
            </div>
          )}
        </div>
      )}

      {isThought && hasText && (
        <ReasoningItem
          content={content}
          onOpenFile={onOpenFile}
          filePathAliases={filePathAliases}
        />
      )}

      {isToolFeedback && hasText && (
        <div className="text-muted-foreground flex min-w-0 items-center gap-2 py-1 text-[13px] leading-5">
          <IconTool
            aria-hidden="true"
            className="text-muted-foreground size-3.5 shrink-0"
          />
          <span className="min-w-0 flex-1 truncate whitespace-nowrap">
            <FilePathText
              text={content.trim().replace(/^🔧\s*/, "")}
              onOpenFile={onOpenFile}
              filePathAliases={filePathAliases}
            />
          </span>
        </div>
      )}

      {imageAttachments.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-2">
          {imageAttachments.map((attachment, index) => (
            <a
              key={`${attachment.url}-${index}`}
              href={attachment.url}
              target="_blank"
              rel="noreferrer"
              className="group/img border-border/50 bg-muted/30 hover:border-border/80 relative overflow-hidden rounded-xl border shadow-sm transition-colors"
            >
              <img
                src={attachment.url}
                alt={attachment.filename || "Attached image"}
                className="max-h-80 max-w-[280px] object-contain transition-transform duration-300 group-hover/img:scale-[1.02]"
              />
              <div className="absolute inset-0 bg-black/0 transition-colors group-hover/img:bg-black/10 dark:group-hover/img:bg-black/20" />
            </a>
          ))}
        </div>
      )}

      {fileAttachments.length > 0 && (
        <div className="mt-1 flex flex-wrap gap-3">
          {fileAttachments.map((attachment, index) => (
            <a
              key={`${attachment.url}-${index}`}
              href={attachment.url}
              download={attachment.filename}
              className="group/file border-border/60 bg-card dark:hover:border-brand/40 flex w-fit max-w-sm min-w-[220px] items-center gap-3.5 rounded-xl border px-4 py-3 transition-all duration-300 hover:-translate-y-0.5 hover:border-violet-500/30 hover:shadow-sm"
            >
              <div className="dark:bg-brand/10 dark:text-brand dark:ring-brand/30 flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-violet-400 ring-1 ring-violet-500/10">
                <IconFileText className="h-5 w-5" />
              </div>
              <div className="flex min-w-0 flex-1 flex-col pr-1">
                <span className="text-foreground/90 truncate text-[14px] leading-tight font-medium transition-colors group-hover/file:text-violet-600 dark:group-hover/file:text-[#8fb39a]">
                  {attachment.filename || "Download file"}
                </span>
                <span className="text-muted-foreground/70 mt-1 text-[12px] font-medium">
                  {attachment.filename?.split(".").pop()?.toUpperCase() ||
                    "FILE"}
                </span>
              </div>
              <div className="bg-muted/60 text-muted-foreground/50 dark:bg-muted/20 dark:group-hover/file:bg-brand dark:group-hover/file:text-brand-foreground flex h-8 w-8 shrink-0 items-center justify-center rounded-full transition-all duration-300 group-hover/file:bg-violet-400 group-hover/file:text-white group-hover/file:shadow-sm">
                <IconDownload className="h-4 w-4 transition-transform duration-300 group-hover/file:-translate-y-[1px]" />
              </div>
            </a>
          ))}
        </div>
      )}

      {!isThought &&
        !isToolFeedback &&
        !isToolCalls &&
        (formattedTimestamp || hasText || hasUsage) && (
          <div className="text-muted-foreground/60 flex flex-wrap items-center gap-1 px-1 text-xs">
            {formattedTimestamp && <span>{formattedTimestamp}</span>}
            {hasText && (
              <>
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-6"
                  onClick={() => void copy(content)}
                  aria-label={copyMessageLabel}
                  title={copyMessageLabel}
                >
                  {isCopied ? (
                    <IconCheck className="size-3.5 text-green-500" />
                  ) : (
                    <IconCopy className="size-3.5" />
                  )}
                </Button>
                {onFork && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-6"
                    onClick={onFork}
                    aria-label={forkMessageLabel}
                    title={forkMessageLabel}
                  >
                    <ForkArrowIcon className="size-3.5" />
                  </Button>
                )}
                {hasUsage && usage && (
                  <TurnStats
                    usage={usage}
                    modelName={modelName}
                    isDefaultModel={isDefaultModel}
                  />
                )}
              </>
            )}
            {hasUsage && usage && (
              <TooltipProvider delayDuration={120} skipDelayDuration={0}>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span
                      tabIndex={0}
                      className="text-muted-foreground/70 flex cursor-default items-center gap-1 tabular-nums"
                    >
                      <IconCoins
                        className="size-3.5 shrink-0"
                        aria-hidden="true"
                      />
                      <span>
                        {t("chat.usage.summary", {
                          input: formatTokenCount(usageInput ?? 0),
                          output: formatTokenCount(usageOutput ?? 0),
                        })}
                      </span>
                      {usageTokensPerSecond !== undefined && (
                        <>
                          <span aria-hidden="true">·</span>
                          <span>
                            {t("chat.usage.throughputValue", {
                              rate: formatTokensPerSecond(usageTokensPerSecond),
                            })}
                          </span>
                        </>
                      )}
                      {usageCostUsd !== undefined && (
                        <>
                          <span aria-hidden="true">·</span>
                          <span>
                            {t(
                              usageCostIsEstimated
                                ? "chat.usage.estimatedCostValue"
                                : "chat.usage.costValue",
                              {
                                cost: formatUsageCostUsd(usageCostUsd),
                              },
                            )}
                          </span>
                        </>
                      )}
                    </span>
                  </TooltipTrigger>
                  <TooltipContent side="top" className="max-w-[260px]">
                    <div className="grid grid-cols-[auto_auto] gap-x-5 gap-y-0.5">
                      {usageInput !== undefined && (
                        <>
                          <span className="opacity-70">
                            {t(
                              usageRequests !== undefined && usageRequests > 1
                                ? "chat.usage.inputTurnTotal"
                                : "chat.usage.input",
                            )}
                          </span>
                          <span className="text-right tabular-nums">
                            {formatTokenCount(usageInput)}
                          </span>
                        </>
                      )}
                      {usageOutput !== undefined && (
                        <>
                          <span className="opacity-70">
                            {t("chat.usage.output")}
                          </span>
                          <span className="text-right tabular-nums">
                            {formatTokenCount(usageOutput)}
                          </span>
                        </>
                      )}
                      {usageTokensPerSecond !== undefined && (
                        <>
                          <span className="opacity-70">
                            {t("chat.usage.throughput")}
                          </span>
                          <span className="text-right tabular-nums">
                            {t("chat.usage.throughputValue", {
                              rate: formatTokensPerSecond(usageTokensPerSecond),
                            })}
                          </span>
                        </>
                      )}
                      {usageRequests !== undefined && (
                        <>
                          <span className="opacity-70">
                            {t("chat.usage.requests")}
                          </span>
                          <span className="text-right tabular-nums">
                            {formatTokenCount(usageRequests)}
                          </span>
                        </>
                      )}
                      {usageContextTokens !== undefined && (
                        <>
                          <span className="opacity-70">
                            {t("chat.usage.contextTokens")}
                          </span>
                          <span className="text-right tabular-nums">
                            {formatTokenCount(usageContextTokens)}
                          </span>
                        </>
                      )}
                      <span className="opacity-70">
                        {t("chat.usage.total")}
                      </span>
                      <span className="text-right tabular-nums">
                        {formatTokenCount(usage.total_tokens)}
                      </span>
                      {usageCostUsd !== undefined && (
                        <>
                          <span className="opacity-70">
                            {t(
                              usageCostIsEstimated
                                ? "chat.usage.estimatedCost"
                                : "chat.usage.cost",
                            )}
                          </span>
                          <span className="text-right tabular-nums">
                            {formatUsageCostUsd(usageCostUsd)}
                          </span>
                        </>
                      )}
                    </div>
                    {usageCostIsEstimated && (
                      <p className="mt-1.5 text-[11px] leading-4 opacity-70">
                        {t("chat.usage.estimated")}
                      </p>
                    )}
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )}
          </div>
        )}
    </div>
  )
})
