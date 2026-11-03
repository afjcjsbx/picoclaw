import { IconMenu2 } from "@tabler/icons-react"
import type { ReactNode } from "react"

import { SidebarTrigger } from "@/components/ui/sidebar"
import { cn } from "@/lib/utils"

interface PageHeaderProps {
  title: string
  titleExtra?: ReactNode
  children?: ReactNode
  className?: string
}

export function PageHeader({
  title,
  titleExtra,
  children,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "z-40 flex h-14 shrink-0 items-center gap-2 px-3 pt-2 sm:px-6",
        className,
      )}
    >
      <div className="flex min-w-0 shrink items-center gap-3">
        <SidebarTrigger
          aria-label="Toggle sidebar"
          className="text-muted-foreground hover:bg-accent hover:text-foreground -ml-2 flex size-9 shrink-0 items-center justify-center rounded-lg md:hidden [&>svg]:size-5"
        >
          <IconMenu2 />
        </SidebarTrigger>
        {title && (
          <h2 className="text-foreground/90 min-w-0 truncate text-xl font-medium tracking-tight">
            {title}
          </h2>
        )}
        {titleExtra}
      </div>
      {children && (
        <div className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto">
          <div className="ml-auto flex shrink-0 items-center gap-2">
            {children}
          </div>
        </div>
      )}
    </div>
  )
}
