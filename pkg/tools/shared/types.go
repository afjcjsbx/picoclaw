package toolshared

type ExecResponse struct {
	SessionID    string        `json:"sessionId,omitempty"`
	Status       string        `json:"status,omitempty"`
	ExitCode     int           `json:"exitCode,omitempty"`
	Output       string        `json:"output,omitempty"`
	Error        string        `json:"error,omitempty"`
	Note         string        `json:"note,omitempty"`
	TotalBytes   int64         `json:"totalBytes,omitempty"`
	Offset       int64         `json:"offset,omitempty"`
	NextOffset   int64         `json:"nextOffset,omitempty"`
	HasMore      bool          `json:"hasMore,omitempty"`
	DroppedBytes int64         `json:"droppedBytes,omitempty"`
	SkippedBytes int64         `json:"skippedBytes,omitempty"`
	Sessions     []SessionInfo `json:"sessions,omitempty"`
}

type SessionInfo struct {
	ID        string `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"startedAt"`
	ExitCode  int    `json:"exitCode"`
	ExitedAt  int64  `json:"exitedAt,omitempty"`
}
