package toolshared

type ExecResponse struct {
	SessionID string        `json:"sessionId,omitempty"`
	Status    string        `json:"status,omitempty"`
	ExitCode  int           `json:"exitCode,omitempty"`
	Output    string        `json:"output,omitempty"`
	Error     string        `json:"error,omitempty"`
	Sessions  []SessionInfo `json:"sessions,omitempty"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"startedAt"`
}
