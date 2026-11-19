import { IconBook } from "@tabler/icons-react"
import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { MCPCatalogIcon } from "./mcp-catalog-icon"

const ICON = { slug: "github", domain: "github.com" }

describe("MCPCatalogIcon", () => {
  it("prefers the Simple Icons brand logo", () => {
    render(<MCPCatalogIcon icon={ICON} label="GitHub" fallback={IconBook} />)

    expect(screen.getByRole("img", { name: "GitHub logo" })).toHaveAttribute(
      "src",
      "https://cdn.simpleicons.org/github",
    )
  })

  it("falls back to the domain favicon when the brand logo fails", () => {
    render(<MCPCatalogIcon icon={ICON} label="GitHub" fallback={IconBook} />)

    fireEvent.error(screen.getByRole("img", { name: "GitHub logo" }))

    expect(screen.getByRole("img", { name: "GitHub logo" })).toHaveAttribute(
      "src",
      "https://www.google.com/s2/favicons?domain=github.com&sz=64",
    )
  })

  it("renders the fallback icon when every source fails", () => {
    const { container } = render(
      <MCPCatalogIcon icon={ICON} label="GitHub" fallback={IconBook} />,
    )

    fireEvent.error(screen.getByRole("img", { name: "GitHub logo" }))
    fireEvent.error(screen.getByRole("img", { name: "GitHub logo" }))

    expect(
      screen.queryByRole("img", { name: "GitHub logo" }),
    ).not.toBeInTheDocument()
    expect(container.querySelector("svg")).toBeInTheDocument()
  })

  it("renders the fallback icon when the entry has no icon", () => {
    const { container } = render(
      <MCPCatalogIcon label="Filesystem" fallback={IconBook} />,
    )

    expect(
      screen.queryByRole("img", { name: "Filesystem logo" }),
    ).not.toBeInTheDocument()
    expect(container.querySelector("svg")).toBeInTheDocument()
  })
})
