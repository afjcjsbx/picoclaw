import type { TFunction } from "i18next"
import { describe, expect, it, vi } from "vitest"

import type { SkillSupportItem } from "@/api/skills"

import {
  compareSkills,
  getOriginAccentClasses,
  getOriginBadgeClasses,
  getOriginLabel,
  getSkillOriginKind,
  sortOrigins,
} from "./origin-utils"

function skill(overrides: Partial<SkillSupportItem>): SkillSupportItem {
  return {
    name: "skill",
    path: "/skills/skill",
    source: "workspace",
    description: "",
    origin_kind: "builtin",
    ...overrides,
  }
}

const t = ((key: string) => key) as unknown as TFunction

describe("getSkillOriginKind", () => {
  it("maps the legacy global origin to builtin", () => {
    expect(getSkillOriginKind(skill({ origin_kind: "global" }))).toBe("builtin")
  })

  it("prefers origin_kind over source", () => {
    expect(
      getSkillOriginKind(skill({ origin_kind: "manual", source: "builtin" })),
    ).toBe("manual")
  })

  it("falls back to source when origin_kind is empty", () => {
    expect(
      getSkillOriginKind(skill({ origin_kind: "", source: "third_party" })),
    ).toBe("third_party")
  })
})

describe("sortOrigins", () => {
  it("orders known origins first and leaves unknown ones alphabetical", () => {
    expect(
      sortOrigins(["manual", "custom", "builtin", "third_party", "another"]),
    ).toEqual(["builtin", "third_party", "manual", "another", "custom"])
  })

  it("does not mutate the input", () => {
    const origins = ["manual", "builtin"]

    sortOrigins(origins)

    expect(origins).toEqual(["manual", "builtin"])
  })
})

describe("getOriginLabel", () => {
  it("translates known origins", () => {
    expect(getOriginLabel("builtin", t)).toBe(
      "pages.agent.skills.origin.builtin",
    )
    expect(getOriginLabel("all", t)).toBe("pages.agent.skills.origin.all")
  })

  it("passes unknown origins through", () => {
    const translate = vi.fn()

    expect(getOriginLabel("custom", translate as unknown as TFunction)).toBe(
      "custom",
    )
    expect(translate).not.toHaveBeenCalled()
  })
})

describe("origin classes", () => {
  it("maps each origin to its accent and badge classes", () => {
    for (const origin of ["builtin", "third_party", "manual"]) {
      expect(getOriginAccentClasses(origin)).toBe(getOriginBadgeClasses(origin))
      expect(getOriginAccentClasses(origin)).not.toBe(
        "bg-muted text-muted-foreground",
      )
    }
  })

  it("falls back to muted classes for unknown origins", () => {
    expect(getOriginAccentClasses("custom")).toBe(
      "bg-muted text-muted-foreground",
    )
    expect(getOriginBadgeClasses("custom")).toBe(
      "bg-muted text-muted-foreground",
    )
  })
})

describe("compareSkills", () => {
  const builtin = skill({ name: "builtin", origin_kind: "builtin" })
  const manual = skill({ name: "manual", origin_kind: "manual" })
  const thirdParty = skill({ name: "third", origin_kind: "third_party" })

  it("sorts by origin then name for the source order", () => {
    expect(
      [manual, thirdParty, builtin]
        .sort((a, b) => compareSkills(a, b, "source"))
        .map((item) => item.name),
    ).toEqual(["builtin", "third", "manual"])
  })

  it("sorts by name ascending and descending", () => {
    const a = skill({ name: "a" })
    const b = skill({ name: "b" })
    const c = skill({ name: "c" })

    expect(
      [c, a, b]
        .sort((left, right) => compareSkills(left, right, "name-asc"))
        .map((item) => item.name),
    ).toEqual(["a", "b", "c"])
    expect(
      [a, c, b]
        .sort((left, right) => compareSkills(left, right, "name-desc"))
        .map((item) => item.name),
    ).toEqual(["c", "b", "a"])
  })
})
