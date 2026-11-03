import { createFileRoute } from "@tanstack/react-router"

import { SubagentsPage } from "@/components/agent/subagents/subagents-page"

export const Route = createFileRoute("/agent/subagents")({
  component: SubagentsPage,
})
