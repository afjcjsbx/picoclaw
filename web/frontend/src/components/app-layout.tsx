import { useState, type CSSProperties, type ReactNode } from "react"
import { Toaster } from "sonner"

import { AppSidebar } from "@/components/app-sidebar"
import { TourGuide } from "@/components/tour/tour-guide"
import { SidebarProvider } from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"

export function AppLayout({ children }: { children: ReactNode }) {
  const [sidebarWidth, setSidebarWidth] = useState(256)

  return (
    <TooltipProvider>
      <SidebarProvider
        className="flex h-dvh overflow-hidden"
        style={{ "--sidebar-width": `${sidebarWidth}px` } as CSSProperties}
      >
        <div className="flex min-h-0 flex-1 overflow-hidden">
          <AppSidebar
            sidebarWidth={sidebarWidth}
            onSidebarWidthChange={setSidebarWidth}
          />
          <div className="flex w-full flex-col overflow-hidden">
            <main className="flex min-h-0 w-full max-w-full flex-1 flex-col overflow-hidden">
              {children}
            </main>
          </div>
        </div>
        <Toaster position="bottom-center" />
        <TourGuide />
      </SidebarProvider>
    </TooltipProvider>
  )
}
