import { Fragment, useMemo } from "react"

import { parseAnsiSegments, wrapLogLine } from "@/lib/ansi-log"

type AnsiLogLineProps = {
  line: string
  wrapColumns: number
  logOffset: number
}

export function AnsiLogLine({
  line,
  wrapColumns,
  logOffset,
}: AnsiLogLineProps) {
  const segments = useMemo(() => {
    return parseAnsiSegments(wrapLogLine(line, wrapColumns))
  }, [line, wrapColumns])

  return (
    <div
      data-log-offset={logOffset}
      className="break-normal whitespace-pre-wrap"
    >
      {segments.map((segment, index) => (
        <Fragment key={`${index}-${segment.text.length}`}>
          <span style={segment.style}>{segment.text}</span>
        </Fragment>
      ))}
    </div>
  )
}
