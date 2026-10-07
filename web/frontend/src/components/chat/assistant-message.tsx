import {
  IconBrain,
  IconCheck,
  IconChevronDown,
  IconClockHour4,
  IconCode,
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
import { memo, useState } from "react"
import { useTranslation } from "react-i18next"
import ReactMarkdown from "react-markdown"
import rehypeHighlight from "rehype-highlight"
import rehypeRaw from "rehype-raw"
import rehypeSanitize from "rehype-sanitize"
import remarkGfm from "remark-gfm"

import {
  MarkdownCodeBlock,
  MessageCodeBlock,
} from "@/components/chat/message-code-block"
import { Button } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { formatMessageTime } from "@/hooks/use-pico-chat"
import { cn } from "@/lib/utils"
import {
  type AssistantMessageKind,
  type ChatAttachment,
  type ChatToolCall,
} from "@/store/chat"

interface AssistantMessageProps {
  content: string
  attachments?: ChatAttachment[]
  kind?: AssistantMessageKind
  modelName?: string
  toolCalls?: ChatToolCall[]
  timestamp?: string | number
}

type ToolDisplay = {
  label: string
  icon: typeof IconTool
  fields?: string[]
}

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
  exec: { label: "Run command", icon: IconTerminal2 },
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

  const value = display.fields
    ?.map((field) => args[field])
    .find(
      (field): field is string => typeof field === "string" && !!field.trim(),
    )
    ?.replace(/\s+/g, " ")
    .trim()

  return {
    ...display,
    label,
    detail: value ? (value.length > 88 ? `${value.slice(0, 84)}…` : value) : "",
  }
}

export const AssistantMessage = memo(function AssistantMessage({
  content,
  attachments = [],
  kind = "normal",
  modelName,
  toolCalls = [],
  timestamp = "",
}: AssistantMessageProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const isThought = kind === "thought"
  const isToolCalls = kind === "tool_calls"
  const isToolFeedback = kind === "tool_feedback"
  const isCollapsedBlock = isThought || isToolCalls
  const hasText = content.trim().length > 0
  const hasToolCalls = toolCalls.length > 0
  const imageAttachments = attachments.filter(
    (attachment) => attachment.type === "image",
  )
  const fileAttachments = attachments.filter(
    (attachment) => attachment.type !== "image",
  )
  const [isExpanded, setIsExpanded] = useState(true)
  const formattedTimestamp =
    timestamp !== "" ? formatMessageTime(timestamp) : ""
  const collapsedLabel = isThought
    ? t("chat.reasoningLabel")
    : t("chat.toolCallsLabel")
  const copyMessageLabel = isCopied
    ? t("chat.copiedLabel")
    : t("chat.copyMessage")
  const trimmedModelName = modelName?.trim() ?? ""
  const toolFeedbackSummary = content
    .trim()
    .replace(/```(?:json)?|`/gi, "")
    .replace(/\s+/g, " ")
    .replace(/^🔧\s*/, "")
    .trim()

  return (
    <div className="group flex w-full flex-col gap-1.5">
      {!isCollapsedBlock && !isToolFeedback && (
        <div className="text-muted-foreground/60 flex items-center justify-between gap-2 px-1 text-xs opacity-70">
          <div className="flex items-center gap-2">
            <span>PicoClaw</span>
            {trimmedModelName && (
              <>
                <span className="opacity-50">•</span>
                <span>{trimmedModelName}</span>
              </>
            )}
            {formattedTimestamp && (
              <>
                <span className="opacity-50">•</span>
                <span>{formattedTimestamp}</span>
              </>
            )}
          </div>
        </div>
      )}

      {(hasText || isCollapsedBlock || hasToolCalls) && (
        <div
          className={cn(
            "relative overflow-hidden rounded-xl border",
            isCollapsedBlock
              ? "border-border/30 bg-muted/20 text-muted-foreground dark:border-border/20 dark:bg-muted/10"
              : "text-card-foreground border-transparent bg-transparent",
          )}
        >
          {isCollapsedBlock && (
            <div
              className="text-muted-foreground/60 hover:text-muted-foreground/80 flex cursor-pointer items-center justify-between px-3 py-2 text-[12px] font-medium transition-colors select-none"
              onClick={() => setIsExpanded(!isExpanded)}
            >
              <div className="flex items-center gap-1.5">
                {isThought ? (
                  <IconBrain className="size-3.5" />
                ) : (
                  <IconTool className="size-3.5" />
                )}
                <span>{collapsedLabel}</span>
                {trimmedModelName && (
                  <span className="text-muted-foreground/45">
                    {trimmedModelName}
                  </span>
                )}
              </div>
              <div className="flex items-center gap-2">
                {formattedTimestamp && (
                  <span className="opacity-50">{formattedTimestamp}</span>
                )}
                <IconChevronDown
                  className={cn(
                    "size-3.5 opacity-0 transition-all duration-200 group-hover:opacity-100",
                    isExpanded ? "rotate-180" : "",
                  )}
                />
              </div>
            </div>
          )}
          {(!isCollapsedBlock || isExpanded) && isToolCalls && hasToolCalls && (
            <div className="space-y-3 px-3 pt-0 pb-3">
              {toolCalls.map((toolCall, index) => {
                const explanation =
                  toolCall.extraContent?.toolFeedbackExplanation?.trim() ?? ""
                const toolName = toolCall.function?.name?.trim() ?? ""
                const toolArguments = toolCall.function?.arguments?.trim() ?? ""
                const toolDisplay = getToolDisplay(toolName, toolArguments)

                if (!explanation && !toolName && !toolArguments) {
                  return null
                }

                return (
                  <div
                    key={toolCall.id ?? `${toolName}-${index}`}
                    className="flex items-start gap-2.5"
                  >
                    <toolDisplay.icon className="text-muted-foreground mt-0.5 size-4 shrink-0" />
                    <div className="min-w-0 flex-1">
                      {toolName && (
                        <div className="text-foreground/80 text-[13px] font-medium">
                          {toolDisplay.label}
                        </div>
                      )}
                      {toolDisplay.detail && (
                        <div
                          className="text-muted-foreground mt-0.5 truncate text-xs"
                          title={toolDisplay.detail}
                        >
                          {toolDisplay.detail}
                        </div>
                      )}
                      {explanation && (
                        <div className="prose dark:prose-invert prose-p:my-1 prose-p:whitespace-pre-wrap max-w-none text-[13px] leading-relaxed [overflow-wrap:anywhere] break-words">
                          <ReactMarkdown
                            remarkPlugins={[remarkGfm]}
                            rehypePlugins={[
                              rehypeRaw,
                              rehypeSanitize,
                              rehypeHighlight,
                            ]}
                            components={{
                              pre: MarkdownCodeBlock,
                            }}
                          >
                            {explanation}
                          </ReactMarkdown>
                        </div>
                      )}
                      {toolArguments && (
                        <details className="text-muted-foreground mt-1 text-xs">
                          <summary className="hover:text-foreground w-fit cursor-pointer select-none">
                            {t("chat.toolCallArgumentsLabel")}
                          </summary>
                          <MessageCodeBlock
                            code={toolArguments}
                            language="json"
                            label={toolName || t("chat.toolCallArgumentsLabel")}
                            className="mt-2 mb-0 shadow-none"
                            bodyClassName="px-3 py-2 text-[12px] leading-relaxed"
                          />
                        </details>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
          {isToolFeedback && hasText && (
            <div className="flex min-w-0 items-center gap-2 py-0.5 text-[13px] leading-5">
              <IconTool
                aria-hidden="true"
                className="text-muted-foreground size-3.5 shrink-0"
              />
              <span
                tabIndex={0}
                className="text-muted-foreground min-w-0 flex-1 truncate whitespace-nowrap"
                title={content}
              >
                {toolFeedbackSummary}
              </span>
            </div>
          )}
          {(!isCollapsedBlock || isExpanded) &&
            !isToolCalls &&
            !isToolFeedback &&
            hasText && (
              <div
                className={cn(
                  "prose dark:prose-invert prose-pre:my-2 prose-pre:overflow-x-auto prose-pre:rounded-lg prose-pre:border prose-pre:bg-zinc-100 prose-pre:p-0 prose-pre:text-zinc-900 dark:prose-pre:bg-zinc-950 dark:prose-pre:text-zinc-100 max-w-none [overflow-wrap:anywhere] break-words",
                  isThought
                    ? "prose-p:my-1.5 prose-p:whitespace-pre-wrap px-3 pt-0 pb-3 text-[13px] leading-relaxed opacity-70"
                    : "prose-p:my-2 prose-p:whitespace-pre-wrap py-1 text-[15px] leading-relaxed",
                )}
              >
                <ReactMarkdown
                  remarkPlugins={[remarkGfm]}
                  rehypePlugins={[rehypeRaw, rehypeSanitize, rehypeHighlight]}
                  components={{
                    pre: MarkdownCodeBlock,
                  }}
                >
                  {content}
                </ReactMarkdown>
              </div>
            )}

          {!isCollapsedBlock && hasText && (
            <Button
              variant="ghost"
              size="icon"
              className={cn(
                "bg-background/50 hover:bg-background/80 absolute top-2 right-2 h-7 w-7 opacity-0 transition-opacity group-hover:opacity-100",
              )}
              onClick={() => void copy(content)}
              aria-label={copyMessageLabel}
              title={copyMessageLabel}
            >
              {isCopied ? (
                <IconCheck className="h-4 w-4 text-green-500" />
              ) : (
                <IconCopy className="text-muted-foreground h-4 w-4" />
              )}
            </Button>
          )}
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
              className="group/file border-border/60 bg-card flex w-fit max-w-sm min-w-[220px] items-center gap-3.5 rounded-xl border px-4 py-3 transition-all duration-300 hover:-translate-y-0.5 hover:border-violet-500/30 hover:shadow-sm dark:hover:border-violet-500/40"
            >
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-violet-400 ring-1 ring-violet-500/10 dark:bg-violet-500/10 dark:text-violet-400 dark:ring-violet-500/30">
                <IconFileText className="h-5 w-5" />
              </div>
              <div className="flex min-w-0 flex-1 flex-col pr-1">
                <span className="text-foreground/90 truncate text-[14px] leading-tight font-medium transition-colors group-hover/file:text-violet-600 dark:group-hover/file:text-violet-400">
                  {attachment.filename || "Download file"}
                </span>
                <span className="text-muted-foreground/70 mt-1 text-[12px] font-medium">
                  {attachment.filename?.split(".").pop()?.toUpperCase() ||
                    "FILE"}
                </span>
              </div>
              <div className="bg-muted/60 text-muted-foreground/50 dark:bg-muted/20 flex h-8 w-8 shrink-0 items-center justify-center rounded-full transition-all duration-300 group-hover/file:bg-violet-400 group-hover/file:text-white group-hover/file:shadow-sm dark:group-hover/file:bg-violet-400">
                <IconDownload className="h-4 w-4 transition-transform duration-300 group-hover/file:-translate-y-[1px]" />
              </div>
            </a>
          ))}
        </div>
      )}
    </div>
  )
})
