export function mergeSplitConversations(
  groups: string[][],
  sourceId: string,
  targetId: string,
): string[][] {
  if (sourceId === targetId) return groups

  const source = groups.find((group) => group.includes(sourceId)) ?? [sourceId]
  const target = groups.find((group) => group.includes(targetId)) ?? [targetId]
  if (source === target) return groups

  const merged = [...new Set([...target, ...source])]
  if (merged.length > 4) return groups

  return [
    ...groups.filter(
      (group) => !group.includes(sourceId) && !group.includes(targetId),
    ),
    merged,
  ]
}

export function removeSplitConversation(
  groups: string[][],
  sessionId: string,
): string[][] {
  return groups
    .map((group) => group.filter((id) => id !== sessionId))
    .filter((group) => group.length > 1)
}
