import type { MCPServerConfigInput } from "@/api/mcp"

import catalogData from "./catalog.json"

export type MCPCatalogCategory =
  | "browser"
  | "web"
  | "docs"
  | "code"
  | "database"
  | "productivity"
  | "research"
  | "cloud"
  | "design"
  | "utility"

export type MCPCatalogFieldTarget =
  | { kind: "env"; name: string }
  | { kind: "header"; name: string; prefix?: string }
  | { kind: "arg"; flag?: string }
  | { kind: "url"; name: string }

export interface MCPCatalogField {
  key: string
  label: string
  placeholder?: string
  required?: boolean
  secret?: boolean
  target: MCPCatalogFieldTarget
}

export interface MCPCatalogEntry {
  // name is also the key used in tools.mcp.servers
  name: string
  displayName: string
  description: string
  category: MCPCatalogCategory
  docsUrl: string
  requires?: string
  transport: "stdio" | "http"
  command?: string
  args?: string[]
  env?: Record<string, string>
  url?: string
  headers?: Record<string, string>
  fields?: MCPCatalogField[]
}

// Server presets live in catalog.json so contributors can add entries without
// editing TypeScript. Keep that file and catalog.schema.json in sync with the
// types above.
export const MCP_CATALOG = catalogData.servers as MCPCatalogEntry[]

function appendQueryParam(url: string, name: string, value: string): string {
  try {
    const parsed = new URL(url)
    parsed.searchParams.set(name, value)
    return parsed.toString()
  } catch {
    const separator = url.includes("?") ? "&" : "?"
    return `${url}${separator}${encodeURIComponent(name)}=${encodeURIComponent(value)}`
  }
}

export function buildMCPCatalogServer(
  entry: MCPCatalogEntry,
  values: Record<string, string>,
): MCPServerConfigInput {
  const args = [...(entry.args ?? [])]
  const env = { ...(entry.env ?? {}) }
  const headers = { ...(entry.headers ?? {}) }
  let url = entry.url ?? ""

  for (const field of entry.fields ?? []) {
    const raw = values[field.key]?.trim()
    if (!raw) continue

    switch (field.target.kind) {
      case "env":
        env[field.target.name] = raw
        break
      case "header":
        headers[field.target.name] = `${field.target.prefix ?? ""}${raw}`
        break
      case "arg":
        if (field.target.flag) args.push(field.target.flag)
        args.push(raw)
        break
      case "url":
        url = appendQueryParam(url, field.target.name, raw)
        break
    }
  }

  if (entry.transport === "http") {
    return {
      enabled: true,
      type: "http",
      url,
      ...(Object.keys(headers).length > 0 ? { headers } : {}),
    }
  }

  return {
    enabled: true,
    type: "stdio",
    command: entry.command ?? "",
    args,
    ...(Object.keys(env).length > 0 ? { env } : {}),
  }
}
