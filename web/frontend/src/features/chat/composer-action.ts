export type ComposerPrimaryAction = "send" | "stop" | "none"

interface ResolveComposerPrimaryActionInput {
  isTyping: boolean
  canInput: boolean
  canSend: boolean
}

// While the agent is running the primary button is Stop, unless the user has
// something ready to send. In that case Send takes over so the message can be
// queued as a steering follow-up.
export function resolveComposerPrimaryAction({
  isTyping,
  canInput,
  canSend,
}: ResolveComposerPrimaryActionInput): ComposerPrimaryAction {
  if (isTyping && (!canInput || !canSend)) {
    return "stop"
  }
  if (canInput) {
    return "send"
  }
  return "none"
}
