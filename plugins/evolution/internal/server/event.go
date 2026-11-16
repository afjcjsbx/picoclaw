package server

// runtimeEvent mirrors the subset of pkg/events.Event and TurnEndPayload that
// the evolution plugin needs. TurnEndPayload fields intentionally use their
// exported Go names because the host marshals that struct without JSON tags.
type runtimeEvent struct {
	Kind  string `json:"kind"`
	Scope struct {
		AgentID    string `json:"agent_id"`
		SessionKey string `json:"session_key"`
		TurnID     string `json:"turn_id"`
	} `json:"scope"`
	Payload struct {
		Status                string   `json:"Status"`
		Workspace             string   `json:"Workspace"`
		UserMessage           string   `json:"UserMessage"`
		FinalContent          string   `json:"FinalContent"`
		ActiveSkills          []string `json:"ActiveSkills"`
		AttemptedSkills       []string `json:"AttemptedSkills"`
		FinalSuccessfulPath   []string `json:"FinalSuccessfulPath"`
		SkillContextSnapshots []struct {
			Sequence   int      `json:"sequence"`
			Trigger    string   `json:"trigger"`
			SkillNames []string `json:"skill_names"`
		} `json:"SkillContextSnapshots"`
		ToolExecutions []struct {
			Name         string   `json:"name"`
			Success      bool     `json:"success"`
			ErrorSummary string   `json:"error_summary"`
			SkillNames   []string `json:"skill_names"`
		} `json:"ToolExecutions"`
	} `json:"payload"`
}
