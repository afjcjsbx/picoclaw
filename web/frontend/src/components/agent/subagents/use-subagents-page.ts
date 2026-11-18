import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  type SubagentAgent,
  type SubagentDashboard,
  type SubagentDashboardUpdate,
  getActiveSubagents,
  getSubagents,
  updateSubagents,
} from "@/api/subagents"
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { refreshGatewayState } from "@/store/gateway"

const SUBAGENTS_QUERY_KEY = ["agents", "subagents"] as const

export function useSubagentsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: SUBAGENTS_QUERY_KEY,
    queryFn: getSubagents,
  })
  const activeQuery = useQuery({
    queryKey: [...SUBAGENTS_QUERY_KEY, "active"],
    queryFn: getActiveSubagents,
    refetchInterval: 3000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
  })

  const [draft, setDraft] = useState<SubagentDashboard | null>(null)

  useEffect(() => {
    if (query.data) {
      setDraft(structuredClone(query.data))
    }
  }, [query.data])

  const isDirty = useMemo(() => {
    if (!draft || !query.data) return false
    return JSON.stringify(draft) !== JSON.stringify(query.data)
  }, [draft, query.data])

  const saveMutation = useMutation({
    mutationFn: updateSubagents,
    onSuccess: async (updated) => {
      queryClient.setQueryData(SUBAGENTS_QUERY_KEY, updated)
      setDraft(structuredClone(updated))
      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t(
          "pages.agent.subagents.save_success",
          "Subagent configuration saved.",
        ),
        t("navigation.subagents", "Subagents"),
        gateway?.restartRequired === true,
      )
      void queryClient.invalidateQueries({ queryKey: ["tools"] })
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t("pages.agent.subagents.save_error"),
      )
    },
  })

  const updateTools = (patch: Partial<SubagentDashboard["tools"]>) => {
    setDraft((current) => {
      if (!current) return current
      const tools = { ...current.tools, ...patch }
      if (patch.subagent === false) {
        tools.spawn = false
        tools.spawn_status = false
      }
      if (tools.spawn || tools.spawn_status) {
        tools.subagent = true
      }
      return { ...current, tools }
    })
  }

  const updateLimits = (patch: Partial<SubagentDashboard["limits"]>) => {
    setDraft((current) =>
      current
        ? { ...current, limits: { ...current.limits, ...patch } }
        : current,
    )
  }

  const updateAgent = (
    id: string,
    updater: (agent: SubagentAgent) => SubagentAgent,
  ) => {
    setDraft((current) =>
      current
        ? {
            ...current,
            agents: current.agents.map((agent) =>
              agent.id === id ? updater(agent) : agent,
            ),
          }
        : current,
    )
  }

  const resetDraft = () => {
    if (query.data) {
      setDraft(structuredClone(query.data))
    }
  }

  const save = () => {
    if (!draft) return
    saveMutation.mutate(toPayload(draft))
  }

  return {
    agents: draft?.agents ?? [],
    configured: draft?.configured ?? false,
    limits: draft?.limits ?? null,
    tools: draft?.tools ?? null,
    activeTasks: activeQuery.data?.active ?? [],
    runtimeAvailable: activeQuery.data?.runtime_available ?? false,
    isActiveLoading: activeQuery.isLoading,
    hasActiveError: activeQuery.error != null,
    isDirty,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    isSaving: saveMutation.isPending,
    hasError: query.error != null,
    refetch: query.refetch,
    refetchActive: activeQuery.refetch,
    resetDraft,
    save,
    updateAgent,
    updateLimits,
    updateTools,
  }
}

function toPayload(dashboard: SubagentDashboard): SubagentDashboardUpdate {
  return {
    tools: dashboard.tools,
    limits: dashboard.limits,
    agents: dashboard.agents.map((agent) => ({
      id: agent.id,
      allow_agents: agent.allow_agents,
      subagent_model: agent.subagent_model,
    })),
  }
}
