import {
  IconActivity,
  IconGauge,
  IconGitFork,
  IconRefresh,
  IconUsers,
} from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import type { ComponentType } from "react"
import { useTranslation } from "react-i18next"

import { getModels } from "@/api/models"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Card, CardContent } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

import { SubagentActiveCard } from "./active-subagents-card"
import { SubagentAgentCard } from "./subagent-agent-card"
import { SubagentLimitsCard, SubagentToolsCard } from "./subagent-settings"
import { useSubagentsPage } from "./use-subagents-page"

export function SubagentsPage() {
  const { t } = useTranslation()
  const {
    agents,
    configured,
    limits,
    tools,
    activeTasks,
    runtimeAvailable,
    isActiveLoading,
    hasActiveError,
    isDirty,
    isLoading,
    isFetching,
    isSaving,
    hasError,
    refetch,
    refetchActive,
    resetDraft,
    save,
    updateAgent,
    updateLimits,
    updateTools,
  } = useSubagentsPage()
  const modelsQuery = useQuery({
    queryKey: ["models"],
    queryFn: getModels,
    staleTime: 60_000,
  })

  const models = Array.from(
    new Set(
      (modelsQuery.data?.models ?? [])
        .map((model) => model.model_name)
        .filter((name) => name.trim() !== ""),
    ),
  )
  const allowedLinks = agents.reduce((total, agent) => {
    if (agent.allow_agents.includes("*")) {
      return total + Math.max(0, agents.length - 1)
    }
    return total + agent.allow_agents.length
  }, 0)

  return (
    <div className="bg-background flex h-full flex-col">
      <PageHeader title={t("navigation.subagents", "Subagents")}>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!isDirty || isSaving}
          onClick={resetDraft}
        >
          {t("pages.agent.subagents.reset", "Reset")}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={isFetching}
          onClick={() => void refetch()}
        >
          <IconRefresh className={cn("size-4", isFetching && "animate-spin")} />
          {t("pages.agent.subagents.refresh", "Refresh")}
        </Button>
        <Button
          type="button"
          size="sm"
          disabled={!isDirty || isSaving}
          onClick={save}
        >
          {isSaving
            ? t("pages.agent.subagents.saving", "Saving...")
            : t("pages.agent.subagents.save", "Save changes")}
        </Button>
      </PageHeader>

      <div className="flex-1 overflow-auto px-6 py-6 pb-20">
        <div className="animate-in fade-in mx-auto w-full max-w-6xl space-y-8 pt-2 duration-300">
          <div className="max-w-2xl space-y-2">
            <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
              {t("pages.agent.subagents.title", "Subagents")}
            </h1>
            <p className="text-muted-foreground/80 text-[14px] leading-relaxed">
              {t(
                "pages.agent.subagents.description",
                "Inspect the agent roster, delegate permissions, and the runtime limits shared by every sub-turn.",
              )}
            </p>
          </div>

          {hasError ? (
            <Card className="shadow-none">
              <CardContent className="flex flex-col items-center gap-4 py-14 text-center">
                <p className="text-destructive font-medium">
                  {t(
                    "pages.agent.subagents.load_error",
                    "Failed to load subagent configuration.",
                  )}
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => void refetch()}
                >
                  {t("pages.agent.subagents.retry", "Retry")}
                </Button>
              </CardContent>
            </Card>
          ) : isLoading || !limits || !tools ? (
            <SubagentsLoadingState />
          ) : (
            <>
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
                <StatCard
                  icon={IconUsers}
                  label={t("pages.agent.subagents.stats.agents", "Agents")}
                  value={agents.length}
                />
                <StatCard
                  icon={IconGitFork}
                  label={t(
                    "pages.agent.subagents.stats.delegations",
                    "Allowed delegations",
                  )}
                  value={allowedLinks}
                />
                <StatCard
                  icon={IconActivity}
                  label={t(
                    "pages.agent.subagents.stats.active_subagents",
                    "Active subagents",
                  )}
                  value={activeTasks.length}
                />
                <StatCard
                  icon={IconGauge}
                  label={t(
                    "pages.agent.subagents.stats.concurrency",
                    "Depth / concurrency",
                  )}
                  value={`${limits.max_depth || 3} / ${limits.max_concurrent || 3}`}
                />
              </div>

              {!configured && (
                <div className="border-border/50 bg-muted/30 flex flex-col gap-3 rounded-2xl border px-4 py-3 text-[13px] sm:flex-row sm:items-center sm:justify-between">
                  <p className="text-muted-foreground">
                    {t(
                      "pages.agent.subagents.agents.only_agent_hint",
                      "PicoClaw is using an implicit main agent. Add agents.list to configure peer agents.",
                    )}
                  </p>
                  <Button asChild variant="outline" size="sm">
                    <Link to="/config/raw">
                      {t(
                        "pages.agent.subagents.agents.open_config",
                        "Open raw config",
                      )}
                    </Link>
                  </Button>
                </div>
              )}

              <SubagentActiveCard
                tasks={activeTasks}
                runtimeAvailable={runtimeAvailable}
                isLoading={isActiveLoading}
                hasError={hasActiveError}
                onRefresh={() => void refetchActive()}
              />

              <div className="grid gap-6 xl:grid-cols-2">
                <SubagentToolsCard tools={tools} onChange={updateTools} />
                <SubagentLimitsCard limits={limits} onChange={updateLimits} />
              </div>

              <section className="space-y-4">
                <div className="space-y-1">
                  <h2 className="text-lg font-semibold tracking-tight">
                    {t("pages.agent.subagents.agents.title", "Agent roster")}
                  </h2>
                  <p className="text-muted-foreground text-[13px]">
                    {t(
                      "pages.agent.subagents.agents.description",
                      "Configure which peers each agent can spawn or delegate to.",
                    )}
                  </p>
                </div>

                <div className="grid gap-5">
                  {agents.map((agent) => (
                    <SubagentAgentCard
                      key={agent.id}
                      agent={agent}
                      agents={agents}
                      models={models}
                      configured={configured}
                      onChange={(updater) => updateAgent(agent.id, updater)}
                    />
                  ))}
                </div>
              </section>
            </>
          )}
        </div>
      </div>
    </div>
  )
}

function StatCard({
  icon: Icon,
  label,
  value,
}: {
  icon: ComponentType<{ className?: string }>
  label: string
  value: string | number
}) {
  return (
    <Card className="shadow-none">
      <CardContent className="flex items-center gap-3 py-4">
        <div className="bg-muted/60 text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-xl">
          <Icon className="size-4.5" />
        </div>
        <div className="min-w-0">
          <p className="text-muted-foreground truncate text-[12px]">{label}</p>
          <p className="text-foreground/90 text-lg font-semibold tracking-tight">
            {value}
          </p>
        </div>
      </CardContent>
    </Card>
  )
}

function SubagentsLoadingState() {
  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {[1, 2, 3, 4].map((item) => (
          <Skeleton key={item} className="h-20 rounded-xl" />
        ))}
      </div>
      <div className="grid gap-6 xl:grid-cols-2">
        <Skeleton className="h-72 rounded-xl" />
        <Skeleton className="h-72 rounded-xl" />
      </div>
      <Skeleton className="h-64 rounded-xl" />
    </div>
  )
}
