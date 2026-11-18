import { createFileRoute } from "@tanstack/react-router"

import { CronPage } from "@/components/agent/cron/cron-page"

export const Route = createFileRoute("/agent/cron")({
  component: CronPage,
})
