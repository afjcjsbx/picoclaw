const ANSI_ESCAPE_PATTERN = new RegExp(
  String.raw`\u001B\[[0-?]*[ -/]*[@-~]`,
  "g",
)

export function stripAnsi(input: string): string {
  return input.replace(ANSI_ESCAPE_PATTERN, "")
}

export function filterLogLines(lines: string[], query: string): string[] {
  const needle = query.trim().toLowerCase()

  if (!needle) {
    return lines
  }

  return lines.filter((line) => stripAnsi(line).toLowerCase().includes(needle))
}

export function buildLogText(lines: string[]): string {
  if (lines.length === 0) {
    return ""
  }

  return `${lines.map(stripAnsi).join("\n")}\n`
}

export function buildLogFilename(now: Date = new Date()): string {
  const stamp = now.toISOString().replaceAll(":", "-").replaceAll(".", "-")

  return `picoclaw-logs-${stamp}.txt`
}

export function downloadLogText(lines: string[], filename: string) {
  const blob = new Blob([buildLogText(lines)], {
    type: "text/plain;charset=utf-8",
  })
  const url = URL.createObjectURL(blob)
  const link = document.createElement("a")

  link.href = url
  link.download = filename
  document.body.append(link)
  link.click()
  link.remove()

  // Revoking immediately can cancel the download in some browsers.
  window.setTimeout(() => URL.revokeObjectURL(url), 0)
}
