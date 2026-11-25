import type { ChatMessage } from "@/store/chat"

/** User prompts ordered from newest to oldest for composer navigation. */
export function getPromptHistory(messages: readonly ChatMessage[]): string[] {
  const prompts: string[] = []

  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index]
    if (message.role !== "user" || !message.content.trim()) continue

    // Avoid showing the same prompt twice when it was sent consecutively.
    if (prompts.at(-1) !== message.content) {
      prompts.push(message.content)
    }
  }

  return prompts
}
