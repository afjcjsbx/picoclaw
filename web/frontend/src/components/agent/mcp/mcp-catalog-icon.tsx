import { useState } from "react"

import { cn } from "@/lib/utils"

import type { MCPCatalogEntryIcon } from "./catalog"

type CatalogIconComponent = React.ComponentType<{ className?: string }>

interface MCPCatalogIconProps {
  icon?: MCPCatalogEntryIcon
  label: string
  fallback: CatalogIconComponent
  className?: string
}

// Resolve the icon in the same way as provider logos: prefer the Simple Icons
// brand mark, then fall back to the domain favicon and finally to the category
// icon so catalog cards always render something meaningful.
export function MCPCatalogIcon({
  icon,
  label,
  fallback: Fallback,
  className,
}: MCPCatalogIconProps) {
  const [sourceIndex, setSourceIndex] = useState(0)
  const [failed, setFailed] = useState(false)

  const sources: string[] = []
  if (icon?.slug) {
    sources.push(`https://cdn.simpleicons.org/${icon.slug}`)
  }
  if (icon?.domain) {
    sources.push(
      `https://www.google.com/s2/favicons?domain=${encodeURIComponent(icon.domain)}&sz=64`,
    )
  }

  const source = sources[sourceIndex]

  if (!source || failed) {
    return (
      <div
        className={cn(
          "bg-muted/60 flex shrink-0 items-center justify-center rounded-xl",
          className,
        )}
      >
        <Fallback className="text-muted-foreground size-5" />
      </div>
    )
  }

  return (
    <div
      className={cn(
        "flex shrink-0 items-center justify-center overflow-hidden rounded-xl border border-black/5 bg-white p-1.5 shadow-xs dark:border-white/10",
        className,
      )}
    >
      <img
        src={source}
        alt={`${label} logo`}
        className="size-full object-contain"
        loading="lazy"
        referrerPolicy="no-referrer"
        draggable={false}
        onError={() => {
          if (sourceIndex < sources.length - 1) {
            setSourceIndex((index) => index + 1)
            return
          }
          setFailed(true)
        }}
      />
    </div>
  )
}
