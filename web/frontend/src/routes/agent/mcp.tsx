import { createFileRoute } from "@tanstack/react-router"

import { MCPPage } from "@/components/agent/mcp/mcp-page"

export const Route = createFileRoute("/agent/mcp")({
  component: MCPPage,
})
