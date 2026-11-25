// Package a2a holds the wire types of the A2A protocol v1.0 (JSON-RPC binding)
// and a small client. Only the subset PicoClaw needs is modeled: text parts,
// blocking and streaming message send, task get/list/cancel/subscribe, and
// push notification configs.
package a2a

import (
	"encoding/json"
	"strings"
)

const (
	Version        = "1.0" // Major.Minor, as the spec requires for A2A-Version and protocolVersion (release 1.0.0)
	VersionHeader  = "A2A-Version"
	WellKnownPath  = "/.well-known/agent-card.json"
	RPCPath        = "/a2a"
	BindingJSONRPC = "JSONRPC"
)

// Task states, spelled as ProtoJSON enum names (the 1.0 wire format).
const (
	StateSubmitted     = "TASK_STATE_SUBMITTED"
	StateWorking       = "TASK_STATE_WORKING"
	StateCompleted     = "TASK_STATE_COMPLETED"
	StateFailed        = "TASK_STATE_FAILED"
	StateCanceled      = "TASK_STATE_CANCELED"
	StateInputRequired = "TASK_STATE_INPUT_REQUIRED"
	StateRejected      = "TASK_STATE_REJECTED"
	StateAuthRequired  = "TASK_STATE_AUTH_REQUIRED"
)

const (
	RoleUser  = "ROLE_USER"
	RoleAgent = "ROLE_AGENT"
)

// JSON-RPC and A2A error codes.
const (
	CodeParse             = -32700
	CodeInvalidRequest    = -32600
	CodeMethodNotFound    = -32601
	CodeInvalidParams     = -32602
	CodeInternal          = -32603
	CodeTaskNotFound      = -32001
	CodeTaskNotCancelable = -32002
	CodePushUnsupported   = -32003
	CodeUnsupportedOp     = -32004
	CodeContentType       = -32005
	CodeNoExtendedCard    = -32007
	CodeVersion           = -32009
)

// IsTerminal reports whether no further transition can happen.
func IsTerminal(state string) bool {
	switch state {
	case StateCompleted, StateFailed, StateCanceled, StateRejected:
		return true
	}
	return false
}

// IsSettled reports that a waiting client has its answer: terminal, or the
// agent needs the client's input.
func IsSettled(state string) bool { return IsTerminal(state) || IsInterrupted(state) }

// IsInterrupted reports states that wait for the client (the task is not running).
func IsInterrupted(state string) bool {
	return state == StateInputRequired || state == StateAuthRequired
}

// Part is a oneof in the spec: exactly one of Text/Raw/URL/Data is set.
// Text is a pointer so an explicit empty string still serializes.
type Part struct {
	Text      *string         `json:"text,omitempty"`
	Raw       string          `json:"raw,omitempty"`
	URL       string          `json:"url,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Filename  string          `json:"filename,omitempty"`
	MediaType string          `json:"mediaType,omitempty"`
}

// TextPart builds a text part.
func TextPart(s string) Part { return Part{Text: &s} }

type Message struct {
	MessageID string `json:"messageId"`
	ContextID string `json:"contextId,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	Role      string `json:"role"`
	Parts     []Part `json:"parts"`
}

// Text joins the text parts with newlines.
func (m *Message) Text() string {
	if m == nil {
		return ""
	}
	return joinText(m.Parts)
}

type TaskStatus struct {
	State     string   `json:"state"`
	Message   *Message `json:"message,omitempty"`
	Timestamp string   `json:"timestamp,omitempty"`
}

type Artifact struct {
	ArtifactID string `json:"artifactId"`
	Name       string `json:"name,omitempty"`
	Parts      []Part `json:"parts"`
}

type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId,omitempty"`
	Status    TaskStatus `json:"status"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	History   []Message  `json:"history,omitempty"`
}

// Text returns the artifact text, or the status message when there is none.
func (t *Task) Text() string {
	var b strings.Builder
	for i := range t.Artifacts {
		if s := joinText(t.Artifacts[i].Parts); s != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(s)
		}
	}
	if b.Len() == 0 {
		return t.Status.Message.Text()
	}
	return b.String()
}

func joinText(parts []Part) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Text == nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(*p.Text)
	}
	return b.String()
}

type StatusUpdate struct {
	TaskID    string     `json:"taskId"`
	ContextID string     `json:"contextId"`
	Status    TaskStatus `json:"status"`
}

type ArtifactUpdate struct {
	TaskID    string   `json:"taskId"`
	ContextID string   `json:"contextId"`
	Artifact  Artifact `json:"artifact"`
	Append    bool     `json:"append,omitempty"`
	LastChunk bool     `json:"lastChunk,omitempty"`
}

// State is the task state a frame reports ("" for artifact updates).
func (r *StreamResult) State() string {
	switch {
	case r.Task != nil:
		return r.Task.Status.State
	case r.Message != nil:
		return StateCompleted // a message-only reply settles the exchange
	case r.StatusUpdate != nil:
		return r.StatusUpdate.Status.State
	}
	return ""
}

// Caps on what a remote peer can make a client hold in RAM while folding a stream.
const (
	maxFoldArtifacts = 16
	maxFoldParts     = 64
	maxFoldText      = 256 << 10
)

// Apply folds one stream frame into t. Artifact chunks are merged by
// artifactId (append=true extends, otherwise replaces); adjacent text parts are
// joined so token deltas do not cost one allocation each.
func (t *Task) Apply(r *StreamResult) {
	switch {
	case r.Task != nil:
		*t = *r.Task
	case r.Message != nil:
		t.Status = TaskStatus{State: StateCompleted, Message: r.Message}
	case r.StatusUpdate != nil:
		t.Status = r.StatusUpdate.Status
	case r.ArtifactUpdate != nil:
		u := r.ArtifactUpdate
		i := 0
		for i < len(t.Artifacts) && t.Artifacts[i].ArtifactID != u.Artifact.ArtifactID {
			i++
		}
		if i == len(t.Artifacts) {
			if i >= maxFoldArtifacts {
				return
			}
			t.Artifacts = append(t.Artifacts, Artifact{ArtifactID: u.Artifact.ArtifactID, Name: u.Artifact.Name})
		}
		a := &t.Artifacts[i]
		if !u.Append {
			a.Parts = nil
		}
		for _, p := range u.Artifact.Parts {
			n := len(a.Parts)
			switch {
			case p.Text != nil && n > 0 && a.Parts[n-1].Text != nil:
				if len(*a.Parts[n-1].Text) < maxFoldText {
					s := *a.Parts[n-1].Text + *p.Text
					a.Parts[n-1].Text = &s
				}
			case n < maxFoldParts:
				a.Parts = append(a.Parts, p)
			}
		}
	}
}

// SendResult is the SendMessage result (oneof task|message).
type SendResult struct {
	Task    *Task    `json:"task,omitempty"`
	Message *Message `json:"message,omitempty"`
}

// StreamResult is one SSE frame payload (oneof).
type StreamResult struct {
	Task           *Task           `json:"task,omitempty"`
	Message        *Message        `json:"message,omitempty"`
	StatusUpdate   *StatusUpdate   `json:"statusUpdate,omitempty"`
	ArtifactUpdate *ArtifactUpdate `json:"artifactUpdate,omitempty"`
}

type SendConfig struct {
	AcceptedOutputModes []string    `json:"acceptedOutputModes,omitempty"`
	HistoryLength       *int        `json:"historyLength,omitempty"`
	ReturnImmediately   bool        `json:"returnImmediately,omitempty"`
	PushConfig          *PushConfig `json:"taskPushNotificationConfig,omitempty"`
}

// AuthInfo is how the agent authenticates to a webhook: it is sent as the
// Authorization header "<scheme> <credentials>".
type AuthInfo struct {
	Scheme      string `json:"scheme"`
	Credentials string `json:"credentials,omitempty"`
}

// PushConfig is the spec's TaskPushNotificationConfig: a webhook for one task.
// It is also the flat params object of the four push config methods.
type PushConfig struct {
	ID             string    `json:"id,omitempty"`
	TaskID         string    `json:"taskId,omitempty"`
	URL            string    `json:"url"`
	Token          string    `json:"token,omitempty"`
	Authentication *AuthInfo `json:"authentication,omitempty"`
}

type SendParams struct {
	Message       Message     `json:"message"`
	Configuration *SendConfig `json:"configuration,omitempty"`
}

type Interface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

type Capabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
	ExtendedAgentCard bool `json:"extendedAgentCard"`
}

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type HTTPAuthScheme struct {
	Scheme string `json:"scheme"`
}

type SecurityScheme struct {
	HTTPAuth *HTTPAuthScheme `json:"httpAuthSecurityScheme,omitempty"`
}

type StringList struct {
	List []string `json:"list"`
}

type SecurityRequirement struct {
	Schemes map[string]StringList `json:"schemes"`
}

type AgentCard struct {
	Name                 string                    `json:"name"`
	Description          string                    `json:"description"`
	Version              string                    `json:"version"`
	SupportedInterfaces  []Interface               `json:"supportedInterfaces"`
	Capabilities         Capabilities              `json:"capabilities"`
	SecuritySchemes      map[string]SecurityScheme `json:"securitySchemes,omitempty"`
	SecurityRequirements []SecurityRequirement     `json:"securityRequirements,omitempty"`
	DefaultInputModes    []string                  `json:"defaultInputModes"`
	DefaultOutputModes   []string                  `json:"defaultOutputModes"`
	Skills               []Skill                   `json:"skills"`
}

// RPCError is a JSON-RPC error object; it also implements error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }
