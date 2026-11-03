import {
  mergeSplitConversations,
  removeSplitConversation,
} from "./split-groups.ts"

function expectEqual(actual: unknown, expected: unknown) {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(
      `Expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`,
    )
  }
}

const pair = [["a", "b"]]
expectEqual(mergeSplitConversations(pair, "c", "a"), [["a", "b", "c"]])
expectEqual(
  mergeSplitConversations(
    [
      ["a", "b"],
      ["c", "d"],
    ],
    "c",
    "b",
  ),
  [["a", "b", "c", "d"]],
)
expectEqual(mergeSplitConversations([["a", "b", "c", "d"]], "e", "b"), [
  ["a", "b", "c", "d"],
])
expectEqual(removeSplitConversation([["a", "b", "c"]], "b"), [["a", "c"]])
expectEqual(removeSplitConversation([["a", "b"]], "b"), [])
