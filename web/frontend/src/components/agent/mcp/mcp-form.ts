import type { MCPDiscoverySettings, MCPServerTransport } from "@/api/mcp"
import {
  parseIntField,
  parseJSONObjectField,
  parseMultilineList,
} from "@/components/config/form-model"

export interface MCPServerForm {
  name: string
  enabled: boolean
  deferred: boolean | null
  type: MCPServerTransport
  url: string
  command: string
  argsText: string
  envText: string
  envFile: string
  inheritEnv: boolean
  headersText: string
}

export const EMPTY_MCP_SERVER_FORM: MCPServerForm = {
  name: "",
  enabled: true,
  deferred: null,
  type: "stdio",
  url: "",
  command: "",
  argsText: "",
  envText: "{}",
  envFile: "",
  inheritEnv: false,
  headersText: "{}",
}

export interface MCPDiscoveryForm {
  enabled: boolean
  ttl: string
  maxResults: string
  useBM25: boolean
  useRegex: boolean
}

export interface MCPConfigForm {
  enabled: boolean
  discovery: MCPDiscoveryForm
  servers: Record<string, MCPServerForm>
}

export const EMPTY_MCP_CONFIG_FORM: MCPConfigForm = {
  enabled: false,
  discovery: {
    enabled: false,
    ttl: "5",
    maxResults: "5",
    useBM25: true,
    useRegex: false,
  },
  servers: {},
}

function asRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  return {}
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function asBool(value: unknown, fallback: boolean): boolean {
  return typeof value === "boolean" ? value : fallback
}

function asOptionalBool(value: unknown): boolean | null {
  return typeof value === "boolean" ? value : null
}

function asNumberString(value: unknown, fallback: string): string {
  if (typeof value === "number" && Number.isFinite(value)) {
    return String(value)
  }
  if (typeof value === "string" && value.trim() !== "") {
    return value
  }
  return fallback
}

function toMCPServerType(value: unknown, url: string): MCPServerTransport {
  if (value === "http" || value === "streamable-http") {
    return "http"
  }
  if (value === "sse") {
    return "sse"
  }
  if (value === "stdio") {
    return "stdio"
  }
  return url !== "" ? "sse" : "stdio"
}

function mapMCPServer(name: string, value: unknown): MCPServerForm {
  const cfg = asRecord(value)
  const argsList = Array.isArray(cfg.args)
    ? cfg.args.filter((item): item is string => typeof item === "string")
    : []
  const url = asString(cfg.url)
  const env = asRecord(cfg.env)
  const headers = asRecord(cfg.headers)

  return {
    name,
    enabled: cfg.enabled !== false,
    deferred: asOptionalBool(cfg.deferred),
    type: toMCPServerType(cfg.type, url),
    url,
    command: asString(cfg.command),
    argsText: argsList.join("\n"),
    envText: JSON.stringify(env, null, 2),
    envFile: asString(cfg.env_file),
    inheritEnv: cfg.inherit_env === true,
    headersText: JSON.stringify(headers, null, 2),
  }
}

export function buildMCPConfigForm(config: unknown): MCPConfigForm {
  const root = asRecord(config)
  const tools = asRecord(root.tools)
  const mcp = asRecord(tools.mcp)
  const discovery = asRecord(mcp.discovery)
  const servers = asRecord(mcp.servers)

  return {
    enabled: asBool(mcp.enabled, EMPTY_MCP_CONFIG_FORM.enabled),
    discovery: {
      enabled: asBool(
        discovery.enabled,
        EMPTY_MCP_CONFIG_FORM.discovery.enabled,
      ),
      ttl: asNumberString(discovery.ttl, EMPTY_MCP_CONFIG_FORM.discovery.ttl),
      maxResults: asNumberString(
        discovery.max_search_results,
        EMPTY_MCP_CONFIG_FORM.discovery.maxResults,
      ),
      useBM25: asBool(
        discovery.use_bm25,
        EMPTY_MCP_CONFIG_FORM.discovery.useBM25,
      ),
      useRegex: asBool(
        discovery.use_regex,
        EMPTY_MCP_CONFIG_FORM.discovery.useRegex,
      ),
    },
    servers: Object.fromEntries(
      Object.entries(servers).map(([name, server]) => [
        name,
        mapMCPServer(name, server),
      ]),
    ),
  }
}

export function buildStringMapMergePatch(
  next: Record<string, string>,
  previous: Record<string, string>,
): Record<string, string | null> {
  const patch: Record<string, string | null> = { ...next }

  for (const key of Object.keys(previous)) {
    if (!(key in next)) {
      patch[key] = null
    }
  }

  return patch
}

export function buildMCPDiscoveryPatch(
  discovery: MCPDiscoveryForm,
): MCPDiscoverySettings {
  if (discovery.enabled && !discovery.useBM25 && !discovery.useRegex) {
    throw new Error(
      "MCP discovery requires at least one search method (BM25 or regex).",
    )
  }

  const patch: MCPDiscoverySettings = {
    enabled: discovery.enabled,
    use_bm25: discovery.useBM25,
    use_regex: discovery.useRegex,
  }

  if (discovery.enabled) {
    patch.ttl = parseIntField(discovery.ttl, "MCP discovery ttl", { min: 1 })
    patch.max_search_results = parseIntField(
      discovery.maxResults,
      "MCP discovery max search results",
      { min: 1 },
    )
  }

  return patch
}

export function buildMCPServerPatch(
  name: string,
  form: MCPServerForm,
  baseline?: MCPServerForm,
): Record<string, unknown> {
  const common = {
    enabled: form.enabled,
    deferred: form.deferred,
  }

  if (form.type === "stdio") {
    const command = form.command.trim()
    if (form.enabled && command === "") {
      throw new Error(`MCP server ${name} requires a command.`)
    }

    const env = parseJSONObjectField(form.envText, `MCP server ${name} env`)
    const baselineEnv = baseline
      ? parseJSONObjectField(baseline.envText, `Saved MCP server ${name} env`)
      : {}

    return {
      ...common,
      type: "stdio",
      command,
      args: parseMultilineList(form.argsText),
      env: buildStringMapMergePatch(env, baselineEnv),
      env_file: form.envFile.trim() === "" ? null : form.envFile.trim(),
      inherit_env: form.inheritEnv,
      url: null,
      headers: null,
    }
  }

  const url = form.url.trim()
  if (form.enabled) {
    if (url === "") {
      throw new Error(`MCP server ${name} requires a URL.`)
    }

    try {
      const parsedURL = new URL(url)
      if (parsedURL.protocol !== "http:" && parsedURL.protocol !== "https:") {
        throw new Error("invalid protocol")
      }
    } catch {
      throw new Error(`MCP server ${name} requires a valid HTTP(S) URL.`)
    }
  }

  const headers = parseJSONObjectField(
    form.headersText,
    `MCP server ${name} headers`,
  )
  const baselineHeaders = baseline
    ? parseJSONObjectField(
        baseline.headersText,
        `Saved MCP server ${name} headers`,
      )
    : {}

  return {
    ...common,
    type: form.type,
    url,
    headers: buildStringMapMergePatch(headers, baselineHeaders),
    command: null,
    args: null,
    env: null,
    env_file: null,
    inherit_env: null,
  }
}
