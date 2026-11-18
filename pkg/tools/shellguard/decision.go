package shellguard

// Action is the policy outcome for one command. The caller must never infer
// allowance from an empty reason: only ActionAllow starts a process.
type Action int

const (
	// ActionAllow runs the command.
	ActionAllow Action = iota
	// ActionReview asks the configured approver for a decision.
	ActionReview
	// ActionBlock rejects the command without review.
	ActionBlock
)

// String returns the lower-case action name for logs and tests.
func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "allow"
	case ActionReview:
		return "review"
	case ActionBlock:
		return "block"
	default:
		return "unknown"
	}
}

// Decision is the single result of a policy evaluation. Rule is a stable
// identifier ("hardline.rm-protected-root", "risk.threshold",
// "operator.deny[2]", "workspace.path", ...) so tests and audit logs do not
// depend on user-facing wording.
type Decision struct {
	Action Action
	Rule   string
	Risk   RiskLevel
	Reason string
}

func AllowDecision(rule string) Decision {
	return Decision{Action: ActionAllow, Rule: rule}
}

func ReviewDecision(rule string, risk RiskLevel, reason string) Decision {
	return Decision{Action: ActionReview, Rule: rule, Risk: risk, Reason: reason}
}

func BlockDecision(rule string, risk RiskLevel, reason string) Decision {
	return Decision{Action: ActionBlock, Rule: rule, Risk: risk, Reason: reason}
}

// Allowed reports whether the decision authorizes the command.
func (d Decision) Allowed() bool { return d.Action == ActionAllow }

// NeedsReview reports whether the decision requires an approver.
func (d Decision) NeedsReview() bool { return d.Action == ActionReview }

// Blocked reports whether the decision rejects the command deterministically.
func (d Decision) Blocked() bool { return d.Action == ActionBlock }
