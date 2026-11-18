import { Fragment, createElement, isValidElement } from "react"
import { describe, expect, it } from "vitest"

import {
  type MarkdownNode,
  extractCodeBlockFromPreNode,
  extractCodeBlockLanguage,
  extractCodeBlockRenderState,
  extractTextFromMarkdownNode,
  splitCodeIntoLines,
  splitHighlightedHtmlIntoLines,
  splitRenderedCodeContentIntoLines,
  stripSingleTrailingLineBreak,
  toClassNameTokens,
  trimTrailingEmptyRenderedCodeLine,
  trimTrailingEmptyStringLine,
} from "./message-code-block.utils"

describe("toClassNameTokens", () => {
  it("splits class strings and filters empty tokens", () => {
    expect(toClassNameTokens("hljs  language-ts")).toEqual([
      "hljs",
      "language-ts",
    ])
  })

  it("keeps only string entries from class arrays", () => {
    expect(toClassNameTokens(["hljs", 7, "", "language-ts"])).toEqual([
      "hljs",
      "language-ts",
    ])
  })

  it("returns an empty array for unsupported values", () => {
    expect(toClassNameTokens(undefined)).toEqual([])
    expect(toClassNameTokens({})).toEqual([])
  })
})

describe("extractTextFromMarkdownNode", () => {
  it("concatenates descendant text nodes", () => {
    const node: MarkdownNode = {
      children: [
        { type: "text", value: "hello " },
        { children: [{ type: "text", value: "world" }] },
      ],
    }

    expect(extractTextFromMarkdownNode(node)).toBe("hello world")
  })

  it("returns an empty string for missing or text-less nodes", () => {
    expect(extractTextFromMarkdownNode(undefined)).toBe("")
    expect(extractTextFromMarkdownNode({ type: "text" })).toBe("")
    expect(extractTextFromMarkdownNode({ tagName: "code" })).toBe("")
  })
})

describe("extractCodeBlockLanguage", () => {
  it("reads the language token", () => {
    expect(extractCodeBlockLanguage("hljs language-json extra")).toBe("json")
  })

  it("ignores empty language tokens", () => {
    expect(extractCodeBlockLanguage("language-")).toBeNull()
    expect(extractCodeBlockLanguage(undefined)).toBeNull()
  })
})

describe("stripSingleTrailingLineBreak", () => {
  it("removes one trailing newline", () => {
    expect(stripSingleTrailingLineBreak("a\n")).toBe("a")
    expect(stripSingleTrailingLineBreak("a\r\n")).toBe("a")
    expect(stripSingleTrailingLineBreak("a\n\n")).toBe("a\n")
    expect(stripSingleTrailingLineBreak("a")).toBe("a")
  })
})

describe("extractCodeBlockFromPreNode", () => {
  it("reads code and language from a nested code element", () => {
    const node: MarkdownNode = {
      tagName: "pre",
      children: [
        {
          tagName: "code",
          properties: { className: "language-ts" },
          children: [{ type: "text", value: "const a = 1\n" }],
        },
      ],
    }

    expect(extractCodeBlockFromPreNode(node)).toEqual({
      code: "const a = 1",
      language: "ts",
    })
  })

  it("falls back to the pre text when no code element exists", () => {
    const node: MarkdownNode = {
      tagName: "pre",
      children: [{ type: "text", value: "plain\n" }],
    }

    expect(extractCodeBlockFromPreNode(node)).toEqual({
      code: "plain",
      language: null,
    })
  })

  it("handles missing nodes", () => {
    expect(extractCodeBlockFromPreNode(undefined)).toEqual({
      code: "",
      language: null,
    })
  })
})

describe("extractCodeBlockRenderState", () => {
  it("uses the code child content and class names", () => {
    const state = extractCodeBlockRenderState(
      createElement("code", { className: "hljs language-ts" }, "const a = 1"),
    )

    expect(state.renderedContent).toBe("const a = 1")
    expect(state.className).toBe("hljs language-ts")
  })

  it("passes through content without a code child", () => {
    expect(extractCodeBlockRenderState("plain")).toEqual({
      renderedContent: "plain",
      className: undefined,
    })
  })
})

describe("splitCodeIntoLines", () => {
  it("splits on newlines and keeps the trailing empty line", () => {
    expect(splitCodeIntoLines("a\nb\n")).toEqual(["a", "b", ""])
  })
})

describe("splitHighlightedHtmlIntoLines", () => {
  it("splits plain text nodes", () => {
    expect(splitHighlightedHtmlIntoLines("a\nb")).toEqual(["a", "b"])
  })

  it("clones wrapper elements for every line", () => {
    expect(
      splitHighlightedHtmlIntoLines('<span class="x">a\nb</span>'),
    ).toEqual(['<span class="x">a</span>', '<span class="x">b</span>'])
  })

  it("treats br elements as line breaks", () => {
    expect(splitHighlightedHtmlIntoLines("a<br>b")).toEqual(["a", "b"])
  })
})

describe("trimTrailingEmptyStringLine", () => {
  it("drops a single trailing empty line", () => {
    expect(trimTrailingEmptyStringLine(["a", "b", ""])).toEqual(["a", "b"])
  })

  it("keeps the only line even when empty", () => {
    expect(trimTrailingEmptyStringLine([""])).toEqual([""])
  })
})

describe("trimTrailingEmptyRenderedCodeLine", () => {
  it("drops a trailing line that only contains empty nodes", () => {
    expect(trimTrailingEmptyRenderedCodeLine([["a"], [""]])).toEqual([["a"]])
    expect(trimTrailingEmptyRenderedCodeLine([["a"], [null, false]])).toEqual([
      ["a"],
    ])
  })

  it("drops a trailing element whose children are empty", () => {
    expect(
      trimTrailingEmptyRenderedCodeLine([
        ["a"],
        [createElement("span", null, "")],
      ]),
    ).toEqual([["a"]])
  })

  it("keeps a single trailing empty line", () => {
    expect(trimTrailingEmptyRenderedCodeLine([[""]])).toEqual([[""]])
  })
})

describe("splitRenderedCodeContentIntoLines", () => {
  it("splits plain strings", () => {
    expect(splitRenderedCodeContentIntoLines("a\nb")).toEqual([["a"], ["b"]])
  })

  it("splits the children of an element and clones it per line", () => {
    const lines = splitRenderedCodeContentIntoLines(
      createElement("span", { className: "token" }, "a\nb"),
    )

    expect(lines).toHaveLength(2)
    expect(lines[0]).toHaveLength(1)
    expect(lines[1]).toHaveLength(1)

    const first = lines[0][0]
    const second = lines[1][0]
    expect(isValidElement(first)).toBe(true)
    expect(isValidElement(second)).toBe(true)
    if (isValidElement<{ children?: unknown; className?: string }>(first)) {
      expect(first.props.className).toBe("token")
      expect(first.props.children).toBe("a")
    }
    if (isValidElement<{ children?: unknown }>(second)) {
      expect(second.props.children).toBe("b")
    }
  })

  it("splits fragments and br elements", () => {
    expect(
      splitRenderedCodeContentIntoLines(
        createElement(Fragment, null, "a", createElement("br"), "b"),
      ),
    ).toEqual([["a"], ["b"]])
  })
})
