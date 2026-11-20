const EXEC_APPROVAL_ACTION = /^\/?(?:approve|deny)$/i
const EXEC_APPROVAL_TOKEN = /^[0-9a-f]{32}$/i

// isExecApprovalReply mirrors the agent loop's approval-reply parser. It lets
// the composer send these messages immediately instead of queueing them behind
// the running turn that is waiting for the approval.
export function isExecApprovalReply(content: string): boolean {
  const parts = content.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) {
    return false
  }
  if (parts.length === 1) {
    return EXEC_APPROVAL_ACTION.test(parts[0])
  }

  return (
    EXEC_APPROVAL_ACTION.test(parts[parts.length - 2]) &&
    EXEC_APPROVAL_TOKEN.test(parts[parts.length - 1])
  )
}
