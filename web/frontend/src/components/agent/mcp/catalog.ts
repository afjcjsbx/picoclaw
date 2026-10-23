import type { MCPServerConfigInput } from "@/api/mcp"

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

export const MCP_CATALOG: MCPCatalogEntry[] = [
  {
    name: "playwright",
    displayName: "Playwright",
    description:
      "Drive a real browser: navigate pages, inspect the DOM, take screenshots and automate flows.",
    category: "browser",
    docsUrl: "https://playwright.dev/docs/getting-started-mcp",
    requires: "Node.js and npx",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@playwright/mcp@latest"],
  },
  {
    name: "chrome-devtools",
    displayName: "Chrome DevTools",
    description:
      "Inspect and control Chrome tabs, console, network and performance traces.",
    category: "browser",
    docsUrl: "https://github.com/ChromeDevTools/chrome-devtools-mcp#readme",
    requires: "Node.js, npx and Chrome",
    transport: "stdio",
    command: "npx",
    args: ["-y", "chrome-devtools-mcp@latest"],
  },
  {
    name: "context7",
    displayName: "Context7",
    description:
      "Fetch up-to-date library documentation and code examples while the agent works.",
    category: "docs",
    docsUrl: "https://context7.com/docs/resources/all-clients",
    requires: "Node.js and npx; API key optional",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@upstash/context7-mcp@latest"],
    fields: [
      {
        key: "apiKey",
        label: "Context7 API key",
        placeholder: "ctx7_...",
        secret: true,
        target: { kind: "arg", flag: "--api-key" },
      },
    ],
  },
  {
    name: "deepwiki",
    displayName: "DeepWiki",
    description:
      "Answer questions about public repositories using DeepWiki's documentation index.",
    category: "docs",
    docsUrl: "https://docs.devin.ai/work-with-devin/deepwiki-mcp",
    requires: "Network access",
    transport: "http",
    url: "https://mcp.deepwiki.com/mcp",
  },
  {
    name: "microsoft-learn",
    displayName: "Microsoft Learn",
    description:
      "Search and fetch official Microsoft Learn documentation without an API key.",
    category: "docs",
    docsUrl: "https://learn.microsoft.com/en-us/training/support/mcp",
    requires: "Network access",
    transport: "http",
    url: "https://learn.microsoft.com/api/mcp",
  },
  {
    name: "exa",
    displayName: "Exa",
    description:
      "Semantic web search and clean page extraction through Exa's hosted MCP server.",
    category: "web",
    docsUrl: "https://exa.ai/mcp",
    requires: "Network access",
    transport: "http",
    url: "https://mcp.exa.ai/mcp",
  },
  {
    name: "firecrawl",
    displayName: "Firecrawl",
    description:
      "Scrape, crawl and extract web pages through Firecrawl's keyless hosted endpoint.",
    category: "web",
    docsUrl: "https://docs.firecrawl.dev/mcp-server",
    requires: "Network access",
    transport: "http",
    url: "https://mcp.firecrawl.dev/v2/mcp",
  },
  {
    name: "brave-search",
    displayName: "Brave Search",
    description:
      "Web, news, image and local search backed by the Brave Search API.",
    category: "web",
    docsUrl: "https://github.com/brave/brave-search-mcp-server#readme",
    requires: "Node.js, npx and a Brave API key",
    transport: "stdio",
    command: "npx",
    args: [
      "-y",
      "@brave/brave-search-mcp-server@latest",
      "--transport",
      "stdio",
    ],
    fields: [
      {
        key: "apiKey",
        label: "Brave Search API key",
        placeholder: "BSA...",
        required: true,
        secret: true,
        target: { kind: "env", name: "BRAVE_API_KEY" },
      },
    ],
  },
  {
    name: "tavily",
    displayName: "Tavily",
    description:
      "Search, extract and crawl the web with the Tavily MCP server.",
    category: "web",
    docsUrl: "https://docs.tavily.com/documentation/mcp",
    requires: "Node.js, npx and a Tavily API key",
    transport: "stdio",
    command: "npx",
    args: ["-y", "tavily-mcp@latest"],
    fields: [
      {
        key: "apiKey",
        label: "Tavily API key",
        placeholder: "tvly-...",
        required: true,
        secret: true,
        target: { kind: "env", name: "TAVILY_API_KEY" },
      },
    ],
  },
  {
    name: "huggingface",
    displayName: "Hugging Face",
    description:
      "Search models, datasets and papers from the Hugging Face Hub.",
    category: "research",
    docsUrl: "https://huggingface.co/docs/hub/agents-mcp",
    requires: "Network access; token optional",
    transport: "http",
    url: "https://huggingface.co/mcp",
    fields: [
      {
        key: "token",
        label: "Hugging Face token (optional)",
        placeholder: "hf_...",
        secret: true,
        target: {
          kind: "header",
          name: "Authorization",
          prefix: "Bearer ",
        },
      },
    ],
  },
  {
    name: "wolfram",
    displayName: "Wolfram Alpha",
    description:
      "Compute precise answers with Wolfram Alpha and the Wolfram Language.",
    category: "research",
    docsUrl: "https://support.wolfram.com/75237",
    requires: "Network access",
    transport: "http",
    url: "https://agenttools.wolfram.com/mcp",
  },
  {
    name: "github",
    displayName: "GitHub",
    description:
      "Manage repositories, issues, pull requests and workflows with the remote GitHub MCP server.",
    category: "code",
    docsUrl:
      "https://github.com/github/github-mcp-server/blob/main/docs/remote-server.md",
    requires: "GitHub personal access token",
    transport: "http",
    url: "https://api.githubcopilot.com/mcp/",
    fields: [
      {
        key: "token",
        label: "GitHub personal access token",
        placeholder: "ghp_...",
        required: true,
        secret: true,
        target: {
          kind: "header",
          name: "Authorization",
          prefix: "Bearer ",
        },
      },
    ],
  },
  {
    name: "filesystem",
    displayName: "Filesystem",
    description:
      "Expose one directory tree to the agent through the reference filesystem server.",
    category: "code",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem",
    requires: "Node.js, npx and an allowed directory",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-filesystem"],
    fields: [
      {
        key: "root",
        label: "Allowed directory",
        placeholder: "/path/to/directory",
        required: true,
        target: { kind: "arg" },
      },
    ],
  },
  {
    name: "git",
    displayName: "Git",
    description:
      "Read history, diffs, branches and commits from a local repository.",
    category: "code",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/git",
    requires: "uv and a repository path",
    transport: "stdio",
    command: "uvx",
    args: ["mcp-server-git"],
    fields: [
      {
        key: "repository",
        label: "Repository path",
        placeholder: "/path/to/repository",
        required: true,
        target: { kind: "arg", flag: "--repository" },
      },
    ],
  },
  {
    name: "memory",
    displayName: "Memory",
    description:
      "Persistent knowledge graph memory that survives across conversations.",
    category: "utility",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/memory",
    requires: "Node.js and npx",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-memory"],
  },
  {
    name: "sequential-thinking",
    displayName: "Sequential Thinking",
    description:
      "Structured step-by-step reasoning with the reference thinking server.",
    category: "utility",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/sequentialthinking",
    requires: "Node.js and npx",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-sequential-thinking"],
  },
  {
    name: "postgres",
    displayName: "PostgreSQL",
    description:
      "Inspect schemas and run read-only SQL queries against PostgreSQL.",
    category: "database",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/postgres",
    requires: "Node.js, npx and a connection string",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-postgres"],
    fields: [
      {
        key: "connectionString",
        label: "PostgreSQL connection string",
        placeholder: "postgresql://user:password@host:5432/database",
        required: true,
        secret: true,
        target: { kind: "arg" },
      },
    ],
  },
  {
    name: "sqlite",
    displayName: "SQLite",
    description: "Query and inspect a local SQLite database file.",
    category: "database",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/sqlite",
    requires: "uv and a database path",
    transport: "stdio",
    command: "uvx",
    args: ["mcp-server-sqlite"],
    fields: [
      {
        key: "database",
        label: "SQLite database path",
        placeholder: "/path/to/database.db",
        required: true,
        target: { kind: "arg", flag: "--db-path" },
      },
    ],
  },
  {
    name: "mongodb",
    displayName: "MongoDB",
    description:
      "Inspect collections, schemas and run read-only queries on MongoDB.",
    category: "database",
    docsUrl:
      "https://www.mongodb.com/docs/mcp-server/local-mcp/security-best-practices/",
    requires: "Node.js, npx and a connection string",
    transport: "stdio",
    command: "npx",
    args: ["-y", "mongodb-mcp-server@latest", "--readOnly"],
    fields: [
      {
        key: "connectionString",
        label: "MongoDB connection string",
        placeholder: "mongodb+srv://user:password@cluster/database",
        required: true,
        secret: true,
        target: { kind: "env", name: "MDB_MCP_CONNECTION_STRING" },
      },
    ],
  },
  {
    name: "supabase",
    displayName: "Supabase",
    description:
      "Inspect and manage Supabase projects through the official MCP server.",
    category: "database",
    docsUrl: "https://supabase.com/docs/guides/getting-started/mcp",
    requires: "Node.js, npx and an access token",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@supabase/mcp-server-supabase@latest", "--read-only"],
    fields: [
      {
        key: "accessToken",
        label: "Supabase access token",
        placeholder: "sbp_...",
        required: true,
        secret: true,
        target: { kind: "env", name: "SUPABASE_ACCESS_TOKEN" },
      },
    ],
  },
  {
    name: "slack",
    displayName: "Slack",
    description:
      "Read channels and send messages through a Slack bot integration.",
    category: "productivity",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/slack",
    requires: "Node.js, npx, a bot token and a team ID",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-slack"],
    fields: [
      {
        key: "botToken",
        label: "Slack bot token",
        placeholder: "xoxb-...",
        required: true,
        secret: true,
        target: { kind: "env", name: "SLACK_BOT_TOKEN" },
      },
      {
        key: "teamId",
        label: "Slack team ID",
        placeholder: "T01234567",
        required: true,
        target: { kind: "env", name: "SLACK_TEAM_ID" },
      },
    ],
  },
  {
    name: "google-maps",
    displayName: "Google Maps",
    description:
      "Look up places, directions and geocoding with the Google Maps API.",
    category: "productivity",
    docsUrl:
      "https://github.com/modelcontextprotocol/servers/tree/main/src/google-maps",
    requires: "Node.js, npx and a Google Maps API key",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@modelcontextprotocol/server-google-maps"],
    fields: [
      {
        key: "apiKey",
        label: "Google Maps API key",
        placeholder: "AIza...",
        required: true,
        secret: true,
        target: { kind: "env", name: "GOOGLE_MAPS_API_KEY" },
      },
    ],
  },
  {
    name: "figma",
    displayName: "Figma",
    description:
      "Read designs and generate code from the local Figma desktop app.",
    category: "design",
    docsUrl: "https://help.figma.com/hc/en-us/articles/32132100833559",
    requires: "Figma desktop app with the MCP server enabled",
    transport: "http",
    url: "http://127.0.0.1:3845/mcp",
  },
  {
    name: "aws-docs",
    displayName: "AWS Documentation",
    description:
      "Search and read the official AWS documentation from the AWS Labs server.",
    category: "cloud",
    docsUrl:
      "https://github.com/awslabs/mcp/tree/main/src/aws-documentation-mcp-server",
    requires: "uv",
    transport: "stdio",
    command: "uvx",
    args: ["awslabs.aws-documentation-mcp-server@latest"],
    env: {
      FASTMCP_LOG_LEVEL: "ERROR",
      AWS_DOCUMENTATION_PARTITION: "aws",
    },
  },
]

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
