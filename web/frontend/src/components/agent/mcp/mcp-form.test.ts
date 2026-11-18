import { describe, expect, it } from "vitest"

import {
  EMPTY_MCP_SERVER_FORM,
  buildMCPConfigForm,
  buildMCPDiscoveryPatch,
  buildMCPServerPatch,
  buildStringMapMergePatch,
} from "./mcp-form"

describe("buildMCPConfigForm", () => {
  it("parses global settings, discovery and servers", () => {
    const form = buildMCPConfigForm({
      tools: {
        mcp: {
          enabled: true,
          discovery: {
            enabled: true,
            ttl: 9,
            max_search_results: 7,
            use_bm25: false,
            use_regex: true,
          },
          servers: {
            local: {
              enabled: true,
              deferred: true,
              type: "stdio",
              command: "npx",
              args: ["-y", "server"],
              env: { TOKEN: "x" },
              env_file: ".env",
              inherit_env: true,
            },
            remote: {
              enabled: false,
              type: "streamable-http",
              url: "https://example.com/mcp",
              headers: { Authorization: "Bearer x" },
            },
          },
        },
      },
    })

    expect(form.enabled).toBe(true)
    expect(form.discovery).toEqual({
      enabled: true,
      ttl: "9",
      maxResults: "7",
      useBM25: false,
      useRegex: true,
    })
    expect(form.servers.local).toMatchObject({
      enabled: true,
      deferred: true,
      type: "stdio",
      command: "npx",
      envFile: ".env",
      inheritEnv: true,
    })
    expect(form.servers.local?.argsText).toBe("-y\nserver")
    expect(form.servers.remote).toMatchObject({
      enabled: false,
      deferred: null,
      type: "http",
      url: "https://example.com/mcp",
    })
  })
})

describe("buildStringMapMergePatch", () => {
  it("marks removed keys with null", () => {
    expect(
      buildStringMapMergePatch({ keep: "b" }, { keep: "a", drop: "x" }),
    ).toEqual({ keep: "b", drop: null })
  })
})

describe("buildMCPDiscoveryPatch", () => {
  it("requires at least one search method when discovery is enabled", () => {
    expect(() =>
      buildMCPDiscoveryPatch({
        enabled: true,
        ttl: "5",
        maxResults: "5",
        useBM25: false,
        useRegex: false,
      }),
    ).toThrow("at least one search method")
  })

  it("omits numeric fields when discovery is disabled", () => {
    expect(
      buildMCPDiscoveryPatch({
        enabled: false,
        ttl: "",
        maxResults: "",
        useBM25: true,
        useRegex: false,
      }),
    ).toEqual({
      enabled: false,
      use_bm25: true,
      use_regex: false,
    })
  })
})

describe("buildMCPServerPatch", () => {
  it("clears remote fields when saving a stdio server", () => {
    const patch = buildMCPServerPatch(
      "local",
      {
        ...EMPTY_MCP_SERVER_FORM,
        name: "local",
        command: " npx ",
        argsText: "-y\nserver",
        envText: '{"KEEP":"b"}',
        envFile: ".env",
      },
      {
        ...EMPTY_MCP_SERVER_FORM,
        envText: '{"KEEP":"a","DROP":"x"}',
      },
    )

    expect(patch).toMatchObject({
      type: "stdio",
      command: "npx",
      args: ["-y", "server"],
      env: { KEEP: "b", DROP: null },
      env_file: ".env",
      url: null,
      headers: null,
    })
  })

  it("validates remote URLs", () => {
    expect(() =>
      buildMCPServerPatch("remote", {
        ...EMPTY_MCP_SERVER_FORM,
        name: "remote",
        type: "http",
        url: "ftp://example.com",
      }),
    ).toThrow("valid HTTP(S) URL")
  })

  it("clears stdio fields when saving a remote server", () => {
    const patch = buildMCPServerPatch("remote", {
      ...EMPTY_MCP_SERVER_FORM,
      name: "remote",
      type: "sse",
      url: "https://example.com/mcp",
      headersText: '{"X-Test":"1"}',
    })

    expect(patch).toMatchObject({
      type: "sse",
      url: "https://example.com/mcp",
      headers: { "X-Test": "1" },
      command: null,
      args: null,
      env: null,
      env_file: null,
    })
  })
})
