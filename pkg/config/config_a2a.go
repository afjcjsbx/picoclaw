package config

import "gopkg.in/yaml.v3"

// A2ASettings configures the inbound A2A channel (channel type "a2a").
// Zero values fall back to defaults in pkg/channels/a2a.
type A2ASettings struct {
	// Host defaults to 127.0.0.1. A non-loopback host requires Tokens.
	Host string `json:"host,omitempty" yaml:"-"`
	// Port defaults to 9900.
	Port int `json:"port,omitempty" yaml:"-"`
	// URL is the public base URL advertised in the Agent Card (no trailing /a2a).
	// Defaults to the Host header of the card request.
	URL string `json:"url,omitempty" yaml:"-"`
	// Name and Description are shown in the Agent Card.
	Name        string `json:"name,omitempty"        yaml:"-"`
	Description string `json:"description,omitempty" yaml:"-"`
	// Streaming enables token deltas on SendStreamingMessage; it also needs
	// streaming enabled on the model. Without it the artifact arrives in one chunk.
	Streaming StreamingConfig `json:"streaming,omitzero" yaml:"-"`
	// Tokens are the accepted bearer tokens; each one is a separate principal
	// that can only see its own tasks.
	Tokens SecureStrings `json:"tokens,omitzero" yaml:"tokens,omitempty"`
	// MaxBodyBytes caps a request body (default 1 MiB).
	MaxBodyBytes int `json:"max_body_bytes,omitempty" yaml:"-"`
	// RateLimitPerMinute is the per-IP request budget (default 60).
	RateLimitPerMinute int `json:"rate_limit_per_minute,omitempty" yaml:"-"`
	// MaxTasks caps tasks kept in RAM (default 256).
	MaxTasks int `json:"max_tasks,omitempty" yaml:"-"`
	// MaxConcurrent caps tasks running at once across all tokens (default 4).
	MaxConcurrent int `json:"max_concurrent,omitempty" yaml:"-"`
	// TaskTimeoutSeconds is the deadline of a single task (default 300).
	TaskTimeoutSeconds int `json:"task_timeout_seconds,omitempty" yaml:"-"`
}

// A2AAgentConfig describes a remote A2A agent PicoClaw can call.
type A2AAgentConfig struct {
	URL          string        `json:"url"                    yaml:"-"`
	Auth         A2AAuthConfig `json:"auth,omitzero"          yaml:"auth,omitempty"`
	Timeout      int           `json:"timeout,omitempty"      yaml:"-"` // seconds, default 120
	Capabilities []string      `json:"capabilities,omitempty" yaml:"-"`
}

// A2AAuthConfig holds the credentials for a remote agent. Only "bearer" exists.
type A2AAuthConfig struct {
	Type  string       `json:"type,omitempty" yaml:"-"`
	Token SecureString `json:"token,omitzero" yaml:"token,omitempty"`
}

// A2AAgents maps agent name to config. Only the token lives in security.yml,
// so YAML decoding merges into entries already loaded from config.json.
type A2AAgents map[string]*A2AAgentConfig

func (m *A2AAgents) UnmarshalYAML(n *yaml.Node) error {
	var raw map[string]yaml.Node
	if err := n.Decode(&raw); err != nil {
		return err
	}
	if *m == nil {
		*m = A2AAgents{}
	}
	for name, node := range raw {
		a := (*m)[name]
		if a == nil {
			a = &A2AAgentConfig{}
			(*m)[name] = a
		}
		if err := node.Decode(a); err != nil {
			return err
		}
	}
	return nil
}

func (m A2AAgents) MarshalYAML() (any, error) {
	type secret struct {
		Auth A2AAuthConfig `yaml:"auth,omitempty"`
	}
	out := make(map[string]secret, len(m))
	for name, a := range m {
		if a != nil && a.Auth.Token.String() != "" {
			out[name] = secret{Auth: a.Auth}
		}
	}
	return out, nil
}
