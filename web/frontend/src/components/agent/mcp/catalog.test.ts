import { describe, expect, it } from "vitest"

import { MCP_CATALOG } from "./catalog"

describe("MCP_CATALOG", () => {
  it("defines an icon source for every server", () => {
    for (const entry of MCP_CATALOG) {
      expect(
        Boolean(entry.icon?.slug ?? entry.icon?.domain),
        `${entry.name} is missing an icon`,
      ).toBe(true)
    }
  })
})
