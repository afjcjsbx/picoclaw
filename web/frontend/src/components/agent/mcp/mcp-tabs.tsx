import { useTranslation } from "react-i18next"

import { cn } from "@/lib/utils"

export type MCPPageTab = "servers" | "catalog"

interface MCPTabsProps {
  activeTab: MCPPageTab
  onChange: (tab: MCPPageTab) => void
}

const tabs: Array<{
  defaultLabel: string
  key: MCPPageTab
  translationKey: string
}> = [
  {
    key: "servers",
    translationKey: "pages.agent.mcp.tabs.servers",
    defaultLabel: "Servers",
  },
  {
    key: "catalog",
    translationKey: "pages.agent.mcp.tabs.catalog",
    defaultLabel: "Catalog",
  },
]

export function MCPTabs({ activeTab, onChange }: MCPTabsProps) {
  const { t } = useTranslation()

  return (
    <div className="border-border/60 border-b px-6 pt-2">
      <div className="flex gap-8">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            type="button"
            onClick={() => onChange(tab.key)}
            className={cn(
              "hover:text-foreground relative cursor-pointer pb-4 text-[14px] font-medium transition-colors outline-none",
              activeTab === tab.key
                ? "text-foreground"
                : "text-muted-foreground",
            )}
          >
            {t(tab.translationKey, tab.defaultLabel)}
            {activeTab === tab.key && (
              <span className="bg-primary absolute inset-x-0 bottom-0 h-[2px] rounded-t-full" />
            )}
          </button>
        ))}
      </div>
    </div>
  )
}
