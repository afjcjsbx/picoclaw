import { describe, expect, it } from "vitest"

import {
  mergeSplitConversations,
  removeSplitConversation,
} from "./split-groups"

describe("mergeSplitConversations", () => {
  it("appends the conversation to the target group", () => {
    expect(mergeSplitConversations([["a", "b"]], "c", "a")).toEqual([
      ["a", "b", "c"],
    ])
  })

  it("merges two groups when the target moves across groups", () => {
    expect(
      mergeSplitConversations(
        [
          ["a", "b"],
          ["c", "d"],
        ],
        "c",
        "b",
      ),
    ).toEqual([["a", "b", "c", "d"]])
  })

  it("leaves the layout unchanged when the pair is already grouped", () => {
    expect(mergeSplitConversations([["a", "b", "c", "d"]], "e", "b")).toEqual([
      ["a", "b", "c", "d"],
    ])
  })
})

describe("removeSplitConversation", () => {
  it("removes the conversation from its group", () => {
    expect(removeSplitConversation([["a", "b", "c"]], "b")).toEqual([
      ["a", "c"],
    ])
  })

  it("drops groups that become empty", () => {
    expect(removeSplitConversation([["a", "b"]], "b")).toEqual([])
  })
})
