import { afterEach, describe, expect, it, vi } from "vitest"

import { getMCPDashboard, redactMCPText } from "./mcp"

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("redactMCPText", () => {
  it.each([
    [undefined, ""],
    ["", ""],
    ["ordinary command output", "ordinary command output"],
    ["api_key=sk-test-value", "api_key=********"],
    ["token: abc123", "token: ********"],
    ["password=hunter2", "password=********"],
    ["Authorization: Bearer abcdefghijklmnop", "Authorization: ********"],
    ["--api-key secret-value", "--api-key ********"],
    ["--token=secret-value", "--token=********"],
    ["sk-abcdefghijklmnop", "********"],
    ["ghp_abcdefghijklmnop", "********"],
    ["postgresql://user:pass@db.example/db", "********"],
    ["redis://cache.example:6379/0", "********"],
  ])("redacts %j", (input, expected) => {
    expect(redactMCPText(input)).toBe(expected)
  })
})

describe("getMCPDashboard", () => {
  it("redacts secrets in server, tool, and parameter metadata", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            enabled: true,
            runtime_available: true,
            servers: [
              {
                name: "github",
                enabled: true,
                configured_enabled: true,
                deferred: false,
                transport: "token=transport-secret",
                status: "error",
                command: "api_key=command-secret",
                launch_command: "--token launch-secret",
                args: ["--password", "arg-secret", "ordinary"],
                error: "Authorization: Bearer abcdefghijklmnop",
                tool_count: 1,
                tools: [
                  {
                    name: "token=tool-secret",
                    description: "secret=description-secret",
                    parameters: [
                      {
                        name: "api_key=parameter-secret",
                        type: "token=type-secret",
                        required: true,
                        description: "password=parameter-description-secret",
                      },
                    ],
                  },
                ],
              },
            ],
          }),
          {
            status: 200,
            headers: { "content-type": "application/json" },
          },
        ),
      ),
    )

    const dashboard = await getMCPDashboard()
    const server = dashboard.servers[0]
    expect(server.transport).toBe("token=********")
    expect(server.command).toBe("api_key=********")
    expect(server.launch_command).toBe("--token ********")
    expect(server.args).toEqual(["--password", "********", "ordinary"])
    expect(server.error).toBe("Authorization: ********")
    expect(server.tools[0].name).toBe("token=********")
    expect(server.tools[0].description).toBe("secret=********")
    expect(server.tools[0].parameters[0]).toEqual({
      name: "api_key=********",
      type: "token=********",
      required: true,
      description: "password=********",
    })
  })

  it("normalizes missing server collections", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            enabled: false,
            runtime_available: false,
            servers: [
              {
                name: "empty",
                enabled: false,
                configured_enabled: false,
                deferred: false,
                transport: "stdio",
                status: "disabled",
                tool_count: 0,
              },
            ],
          }),
          {
            status: 200,
            headers: { "content-type": "application/json" },
          },
        ),
      ),
    )

    const dashboard = await getMCPDashboard()
    expect(dashboard.servers[0].args).toEqual([])
    expect(dashboard.servers[0].tools).toEqual([])
  })

  it("throws on API errors", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response("unavailable", {
          status: 503,
          statusText: "Service Unavailable",
        }),
      ),
    )

    await expect(getMCPDashboard()).rejects.toThrow(
      "API error: 503 Service Unavailable",
    )
  })
})
