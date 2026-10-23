import {
  IconArrowUp,
  IconFileText,
  IconMicrophone,
  IconPlayerStop,
  IconPlus,
  IconX,
} from "@tabler/icons-react"
import {
  type ClipboardEvent as ReactClipboardEvent,
  type DragEvent as ReactDragEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useTranslation } from "react-i18next"
import TextareaAutosize from "react-textarea-autosize"
import { toast } from "sonner"

import type { SlashCommand } from "@/api/commands"
import { ContextUsageRing } from "@/components/chat/context-usage-ring"
import { SlashCommandPalette } from "@/components/chat/slash-command-palette"
import { Button } from "@/components/ui/button"
import {
  AudioAttachmentTooLargeError,
  MAX_AUDIO_ATTACHMENT_BYTES,
  MAX_AUDIO_RECORDING_MS,
  audioRecordingErrorKind,
  audioRecordingNeedsSecureContext,
  buildAudioAttachment,
  formatRecordingDuration,
  isAudioRecordingSupported,
} from "@/features/chat/audio-recorder"
import {
  type SlashPaletteEntry,
  type SlashPaletteItem,
  filterSlashPaletteItems,
  nextSlashRecents,
  readSlashRecents,
  slashCommandFallbackTitle,
  slashCommandI18nKey,
  slashCommandInsertion,
  slashQueryFromInput,
  storeSlashRecents,
} from "@/features/chat/slash-commands"
import { useAudioRecorder } from "@/hooks/use-audio-recorder"
import { cn } from "@/lib/utils"
import type { ChatAttachment, ContextUsage } from "@/store/chat"

export type ChatInputDisabledReason =
  | "gatewayUnknown"
  | "gatewayStarting"
  | "gatewayRestarting"
  | "gatewayStopping"
  | "gatewayStopped"
  | "gatewayError"
  | "websocketConnecting"
  | "websocketDisconnected"
  | "websocketError"
  | "noDefaultModel"

interface ChatComposerProps {
  input: string
  attachments: ChatAttachment[]
  onInputChange: (value: string) => void
  onAddFiles: () => void
  onPaste: (event: ReactClipboardEvent<HTMLTextAreaElement>) => void
  onDragEnter: (event: ReactDragEvent<HTMLDivElement>) => void
  onDragLeave: (event: ReactDragEvent<HTMLDivElement>) => void
  onDragOver: (event: ReactDragEvent<HTMLDivElement>) => void
  onDrop: (event: ReactDragEvent<HTMLDivElement>) => void
  onRemoveAttachment: (index: number) => void
  onSend: () => void
  onSendAudio: (attachment: ChatAttachment) => boolean
  modelSelector?: ReactNode
  onContextDetail?: () => void
  inputDisabledReason: ChatInputDisabledReason | null
  canSend: boolean
  isDragActive: boolean
  contextUsage?: ContextUsage
  slashCommands?: SlashCommand[]
}

const AUDIO_RECORDING_ERROR_KEYS = {
  permissionDenied: "chat.micPermissionDenied",
  unavailable: "chat.microphoneUnavailable",
  unsupported: "chat.recordingUnsupported",
  failed: "chat.recordingFailed",
} as const

export function ChatComposer({
  input,
  attachments,
  onInputChange,
  onAddFiles,
  onPaste,
  onDragEnter,
  onDragLeave,
  onDragOver,
  onDrop,
  onRemoveAttachment,
  onSend,
  onSendAudio,
  modelSelector,
  onContextDetail,
  inputDisabledReason,
  canSend,
  isDragActive,
  contextUsage,
  slashCommands = [],
}: ChatComposerProps) {
  const { t } = useTranslation()
  const canInput = inputDisabledReason === null
  const composingRef = useRef(false)
  const surfaceRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const [paletteDismissed, setPaletteDismissed] = useState(false)
  const [selectedCommandIndex, setSelectedCommandIndex] = useState(0)
  const [recentCommands, setRecentCommands] = useState<string[]>(() =>
    readSlashRecents(),
  )
  const {
    isRecording,
    isStarting: isStartingRecording,
    elapsedMs: recordingElapsedMs,
    start: startRecording,
    stop: stopRecording,
    cancel: cancelRecording,
  } = useAudioRecorder()
  const [isFinishingRecording, setIsFinishingRecording] = useState(false)
  const finishingRecordingRef = useRef(false)
  const audioRecordingSupported = useMemo(() => isAudioRecordingSupported(), [])
  const audioRecordingNeedsHttps = useMemo(
    () => audioRecordingNeedsSecureContext(),
    [],
  )
  const hasInput = input.trim().length > 0
  const disabledMessage =
    inputDisabledReason === null
      ? null
      : t(`chat.disabledPlaceholder.${inputDisabledReason}`)
  const placeholder = disabledMessage ?? t("chat.placeholder")

  const paletteEntries = useMemo<SlashPaletteEntry[]>(
    () =>
      slashCommands.map((command) => {
        const key = slashCommandI18nKey(command.command)
        return {
          command: command.command,
          title: t(`chat.slash.commands.${key}.title`, {
            defaultValue: slashCommandFallbackTitle(command.command),
          }),
          description: t(`chat.slash.commands.${key}.description`, {
            defaultValue: command.description,
          }),
          argHint: command.arg_hint ?? "",
        }
      }),
    [slashCommands, t],
  )

  const slashQuery = useMemo(
    () => (canInput && !paletteDismissed ? slashQueryFromInput(input) : null),
    [canInput, input, paletteDismissed],
  )
  const paletteItems = useMemo<SlashPaletteItem[]>(
    () =>
      slashQuery === null
        ? []
        : filterSlashPaletteItems(paletteEntries, slashQuery, recentCommands),
    [paletteEntries, recentCommands, slashQuery],
  )
  const showPalette = paletteItems.length > 0
  const activeCommandIndex = Math.min(
    selectedCommandIndex,
    Math.max(paletteItems.length - 1, 0),
  )

  useEffect(() => {
    if (!showPalette) return

    // Close the palette on pointer input outside the composer, like the composer's other popovers.
    const dismissOnPointerDown = (event: PointerEvent) => {
      const target = event.target
      if (target instanceof Node && surfaceRef.current?.contains(target)) return
      setPaletteDismissed(true)
    }

    document.addEventListener("pointerdown", dismissOnPointerDown, true)
    return () => {
      document.removeEventListener("pointerdown", dismissOnPointerDown, true)
    }
  }, [showPalette])

  const handleInputChange = (value: string) => {
    setPaletteDismissed(false)
    setSelectedCommandIndex(0)
    onInputChange(value)
  }

  const chooseSlashCommand = (item: SlashPaletteItem) => {
    const nextRecents = nextSlashRecents(recentCommands, item.command)
    setRecentCommands(nextRecents)
    storeSlashRecents(nextRecents)
    setPaletteDismissed(true)
    setSelectedCommandIndex(0)
    onInputChange(slashCommandInsertion(item))
    textareaRef.current?.focus()
  }

  const finishRecording = useCallback(async () => {
    if (finishingRecordingRef.current) return
    finishingRecordingRef.current = true
    setIsFinishingRecording(true)
    try {
      const recorded = await stopRecording()
      if (!recorded) return
      const attachment = await buildAudioAttachment(recorded)
      if (!onSendAudio(attachment)) {
        toast.error(t("chat.audioSendFailed"))
      }
    } catch (error) {
      if (error instanceof AudioAttachmentTooLargeError) {
        const size = `${Math.round(MAX_AUDIO_ATTACHMENT_BYTES / (1024 * 1024))} MB`
        toast.error(t("chat.audioTooLarge", { size }))
      } else {
        toast.error(t("chat.recordingFailed"))
      }
    } finally {
      finishingRecordingRef.current = false
      setIsFinishingRecording(false)
    }
  }, [onSendAudio, stopRecording, t])

  useEffect(() => {
    if (!isRecording || recordingElapsedMs < MAX_AUDIO_RECORDING_MS) return
    void finishRecording()
  }, [finishRecording, isRecording, recordingElapsedMs])

  useEffect(() => {
    if (canInput || (!isRecording && !isStartingRecording)) return
    // The gateway or socket went away, so the recording could never be sent.
    cancelRecording()
  }, [canInput, cancelRecording, isRecording, isStartingRecording])

  const handleMicrophoneClick = async () => {
    if (!canInput) return
    if (!audioRecordingSupported) {
      toast.error(t("chat.recordingNeedsHttps"))
      return
    }
    if (isRecording) {
      await finishRecording()
      return
    }

    try {
      await startRecording()
    } catch (error) {
      toast.error(t(AUDIO_RECORDING_ERROR_KEYS[audioRecordingErrorKind(error)]))
    }
  }

  const handleKeyDown = (e: ReactKeyboardEvent<HTMLTextAreaElement>) => {
    const nativeEvent = e.nativeEvent as Event & {
      isComposing?: boolean
      keyCode?: number
    }
    if (
      composingRef.current ||
      nativeEvent.isComposing ||
      nativeEvent.keyCode === 229
    ) {
      return
    }
    if (isRecording) {
      return
    }
    if (showPalette) {
      const count = paletteItems.length
      if (e.key === "ArrowDown") {
        e.preventDefault()
        setSelectedCommandIndex((activeCommandIndex + 1) % count)
        return
      }
      if (e.key === "ArrowUp") {
        e.preventDefault()
        setSelectedCommandIndex((activeCommandIndex - 1 + count) % count)
        return
      }
      if (e.key === "Escape") {
        e.preventDefault()
        setPaletteDismissed(true)
        return
      }
      // A bare command that is already fully typed (e.g. "/clear") is sent
      // directly; Enter on any other partial match completes it first.
      const isExactBareCommand = slashCommands.some(
        (command) => command.command === input && !command.arg_hint,
      )
      if (
        e.key === "Tab" ||
        (e.key === "Enter" && !e.shiftKey && !isExactBareCommand)
      ) {
        e.preventDefault()
        chooseSlashCommand(paletteItems[activeCommandIndex])
        return
      }
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      onSend()
    }
  }

  return (
    <div className="pointer-events-none relative z-10 mt-0 shrink-0 bg-[var(--conversation-background)] px-4 pb-[calc(0.5rem+env(safe-area-inset-bottom))] md:px-8 md:pb-4 lg:px-24 xl:px-48">
      <div className="pointer-events-auto mx-auto flex max-w-[49.5rem] flex-col items-end">
        <div
          ref={surfaceRef}
          className={cn(
            "bg-muted/60 relative flex w-full flex-col rounded-3xl border border-transparent p-3 shadow-none transition-colors",
            isDragActive && "border-violet-400/70 bg-violet-500/10",
          )}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDragOver={onDragOver}
          onDrop={onDrop}
        >
          {isDragActive && (
            <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-2xl border-2 border-dashed border-violet-400/70 bg-violet-500/10">
              <div className="bg-background/95 text-foreground rounded-full px-4 py-2 text-sm font-medium shadow-sm">
                {t("chat.dropFilesActive")}
              </div>
            </div>
          )}

          {attachments.length > 0 && (
            <div className="mb-3 flex flex-wrap gap-2 px-2">
              {attachments.map((attachment, index) => (
                <div
                  key={`${attachment.url}-${attachment.filename}-${index}`}
                  className={cn(
                    "bg-background relative overflow-hidden rounded-xl border",
                    attachment.type === "image"
                      ? "h-20 w-20"
                      : "flex h-14 max-w-56 min-w-36 items-center gap-2 px-3 pr-8",
                  )}
                >
                  {attachment.type === "image" ? (
                    <img
                      src={attachment.url}
                      alt={attachment.filename || t("chat.uploadedImage")}
                      className="h-full w-full object-cover"
                    />
                  ) : (
                    <>
                      <IconFileText className="text-muted-foreground size-4 shrink-0" />
                      <span className="truncate text-xs">
                        {attachment.filename || t("chat.uploadedFile")}
                      </span>
                    </>
                  )}
                  <button
                    type="button"
                    onClick={() => onRemoveAttachment(index)}
                    className="bg-background/85 text-foreground absolute top-1 right-1 inline-flex h-6 w-6 items-center justify-center rounded-full border shadow-sm transition hover:bg-white"
                    aria-label={t("chat.removeAttachment")}
                    title={t("chat.removeAttachment")}
                  >
                    <IconX className="h-3.5 w-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}

          {showPalette && (
            <SlashCommandPalette
              items={paletteItems}
              selectedIndex={activeCommandIndex}
              onHover={setSelectedCommandIndex}
              onChoose={chooseSlashCommand}
            />
          )}

          <TextareaAutosize
            ref={textareaRef}
            value={input}
            onChange={(e) => handleInputChange(e.target.value)}
            onCompositionStart={() => {
              composingRef.current = true
            }}
            onCompositionEnd={() => {
              composingRef.current = false
            }}
            onPaste={onPaste}
            onKeyDown={handleKeyDown}
            placeholder={placeholder}
            disabled={!canInput}
            title={disabledMessage || undefined}
            className={cn(
              "placeholder:text-muted-foreground/65 max-h-[160px] min-h-[40px] resize-none border-0 bg-transparent px-1 py-1.5 text-[15px] shadow-none transition-colors focus-visible:ring-0 focus-visible:outline-none dark:bg-transparent",
              !canInput && "cursor-not-allowed",
            )}
            minRows={1}
            maxRows={6}
          />

          <div className="mt-2 flex items-center justify-between px-1">
            <div className="flex min-w-0 items-center gap-1">
              {isRecording ? (
                <div className="flex items-center gap-2 rounded-full border border-red-500/30 bg-red-500/10 px-2.5 py-1 text-xs text-red-600 dark:text-red-400">
                  <span
                    aria-hidden="true"
                    className="size-2 animate-pulse rounded-full bg-red-500"
                  />
                  <span className="tabular-nums">
                    {formatRecordingDuration(recordingElapsedMs)}
                  </span>
                  <span className="hidden sm:inline">
                    {t("chat.recording")}
                  </span>
                  <button
                    type="button"
                    onClick={cancelRecording}
                    disabled={isFinishingRecording}
                    className="text-muted-foreground hover:text-foreground inline-flex size-4 items-center justify-center rounded-full"
                    aria-label={t("chat.cancelRecording")}
                    title={t("chat.cancelRecording")}
                  >
                    <IconX className="size-3.5" />
                  </button>
                </div>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="text-muted-foreground hover:text-foreground h-8 w-8 rounded-full"
                  onClick={onAddFiles}
                  disabled={!canInput}
                  aria-label={t("chat.attachFiles")}
                  title={t("chat.attachFiles")}
                >
                  <IconPlus className="size-4" />
                </Button>
              )}
            </div>

            <div className="flex items-center gap-1.5">
              {contextUsage && (
                <ContextUsageRing
                  usage={contextUsage}
                  onDetailClick={onContextDetail}
                />
              )}
              {modelSelector}
              {canInput &&
              (audioRecordingSupported || audioRecordingNeedsHttps) ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={cn(
                    "h-8 w-8 rounded-full",
                    isRecording
                      ? "bg-red-500 text-white hover:bg-red-600"
                      : "text-muted-foreground hover:text-foreground",
                  )}
                  onClick={() => void handleMicrophoneClick()}
                  disabled={isStartingRecording || isFinishingRecording}
                  aria-label={
                    isRecording
                      ? t("chat.stopRecording")
                      : t("chat.recordVoiceMessage")
                  }
                  title={
                    isRecording
                      ? t("chat.stopRecording")
                      : audioRecordingNeedsHttps
                        ? t("chat.recordingNeedsHttps")
                        : t("chat.recordVoiceMessage")
                  }
                >
                  {isRecording ? (
                    <IconPlayerStop className="size-4" />
                  ) : (
                    <IconMicrophone className="size-4" />
                  )}
                </Button>
              ) : null}
              {canInput ? (
                <span tabIndex={!canSend ? 0 : undefined}>
                  <Button
                    type="button"
                    size="icon"
                    className="size-8 rounded-full bg-violet-500 text-white transition-transform hover:bg-violet-600 active:scale-95"
                    onClick={onSend}
                    disabled={!canSend || isRecording}
                    aria-label={t("chat.sendMessage")}
                  >
                    <IconArrowUp className="size-4" />
                  </Button>
                </span>
              ) : null}
            </div>
          </div>
        </div>

        {hasInput && (
          <div className="border-border/50 bg-muted/55 text-muted-foreground dark:bg-muted/45 mt-2 inline-flex items-center rounded-md border px-3 py-1 text-[11px] shadow-sm">
            {t("chat.composeHint")}
          </div>
        )}
      </div>
    </div>
  )
}
