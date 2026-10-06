package agentctx

import (
	"encoding/json"
	"strings"
)

// AgentDescriptor is the structured discovery payload injected into each
// agent's system prompt so the LLM can choose a peer by identity.
type AgentDescriptor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func formatAgentDiscoverySection(agents []AgentDescriptor) string {
	if len(agents) == 0 {
		return ""
	}

	payload := struct {
		Agents []AgentDescriptor `json:"agents"`
	}{
		Agents: agents,
	}

	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return ""
	}

	var header strings.Builder
	header.WriteString("# Agent Discovery\n\n")
	header.WriteString("This registry lists the peer agents this agent is permitted to spawn.\n")
	header.WriteString(
		"Choose a peer based on its description. Use only agent IDs listed here when calling spawn.\n\n",
	)
	header.WriteString("```json\n")
	header.Write(encoded)
	header.WriteString("\n```")

	return header.String()
}
