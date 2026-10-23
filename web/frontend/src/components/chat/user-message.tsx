import { IconCheck, IconCopy, IconFileText } from "@tabler/icons-react"
import { memo } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-copy-to-clipboard"
import { formatMessageTime } from "@/hooks/use-pico-chat"
import { cn } from "@/lib/utils"
import type { ChatAttachment } from "@/store/chat"

interface UserMessageProps {
  content: string
  attachments?: ChatAttachment[]
  timestamp?: string | number
}

export const UserMessage = memo(function UserMessage({
  content,
  attachments = [],
  timestamp = "",
}: UserMessageProps) {
  const { t } = useTranslation()
  const { copy, isCopied } = useCopyToClipboard()
  const hasText = content.trim().length > 0
  const isCommand = content.trim().startsWith("/")
  const imageAttachments = attachments.filter(
    (attachment) => attachment.type === "image",
  )
  const copyMessageLabel = isCopied
    ? t("chat.copiedLabel")
    : t("chat.copyMessage")
  const formattedTimestamp =
    timestamp !== "" ? formatMessageTime(timestamp) : ""

  return (
    <div className="group flex w-full flex-col items-end gap-1.5">
      {imageAttachments.length > 0 && (
        <div className="flex max-w-[70%] flex-wrap justify-end gap-2">
          {imageAttachments.map((attachment, index) => (
            <img
              key={`${attachment.url}-${index}`}
              src={attachment.url}
              alt={attachment.filename || t("chat.uploadedImage")}
              className="max-h-72 max-w-full object-cover"
            />
          ))}
        </div>
      )}

      {hasText && (
        <div className="max-w-[70%]">
          <div
            className={cn(
              "wrap-break-word whitespace-pre-wrap",
              isCommand
                ? "rounded-xl border border-zinc-200 bg-transparent px-4 py-3 font-mono text-[14px] text-zinc-800 dark:border-zinc-800/60 dark:bg-[#121212] dark:text-zinc-200 dark:shadow-sm"
                : "bg-muted text-foreground rounded-2xl rounded-tr-sm px-5 py-3 text-[16px] leading-[1.75]",
            )}
          >
            {isCommand ? (
              <div className="flex items-start gap-2.5">
                <span className="font-bold text-emerald-600 select-none dark:text-emerald-400">
                  ❯
                </span>
                <span className="mt-[1px]">{content}</span>
              </div>
            ) : (
              content
            )}
          </div>
        </div>
      )}

      {attachments.some((attachment) => attachment.type !== "image") && (
        <div className="flex max-w-[70%] flex-wrap justify-end gap-2">
          {attachments
            .filter((attachment) => attachment.type !== "image")
            .map((attachment, index) => {
              const card = (
                <>
                  <span className="bg-background/70 text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
                    <IconFileText className="size-4" />
                  </span>
                  <span className="min-w-0">
                    <span className="block max-w-48 truncate text-sm font-medium">
                      {attachment.filename || t("chat.uploadedFile")}
                    </span>
                    <span className="text-muted-foreground text-[11px]">
                      {attachment.filename?.split(".").pop()?.toUpperCase() ||
                        "FILE"}
                    </span>
                  </span>
                </>
              )
              const className =
                "bg-muted/50 border-border/60 flex w-fit max-w-full items-center gap-2 rounded-xl border px-3 py-2"

              return attachment.url ? (
                <a
                  key={`${attachment.url}-${index}`}
                  href={attachment.url}
                  download={attachment.filename}
                  className={`${className} hover:border-violet-500/40`}
                >
                  {card}
                </a>
              ) : (
                <div
                  key={`${attachment.filename}-${index}`}
                  className={className}
                >
                  {card}
                </div>
              )
            })}
        </div>
      )}

      {(formattedTimestamp || hasText || attachments.length > 0) && (
        <div className="flex items-center gap-1 px-1 text-[12px] text-zinc-400">
          {formattedTimestamp && <span>{formattedTimestamp}</span>}
          {hasText && (
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className={cn(
                "size-6",
                isCommand
                  ? "text-zinc-700 dark:text-zinc-200"
                  : "text-foreground",
              )}
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
          )}
        </div>
      )}
    </div>
  )
})
