package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const (
	maxTodos        = 100
	maxTodoSessions = 256
)

// TodoItem is a planning step, not a scheduled job or proof of execution.
type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
}

type todoSession struct{ agent, session string }

// TodoTool keeps bounded, process-local plans isolated by agent and session.
// Registry clones may share this tool safely, including across sub-turns.
type TodoTool struct {
	mu    sync.Mutex
	lists map[todoSession][]TodoItem
}

func NewTodoTool() *TodoTool { return &TodoTool{lists: make(map[todoSession][]TodoItem)} }

// ClearSession discards the plan when the owning conversation is cleared.
func (t *TodoTool) ClearSession(agentID, sessionKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.lists, todoSession{agentID, sessionKey})
}

func (t *TodoTool) Name() string { return "todo" }
func (t *TodoTool) Description() string {
	return "Track a short plan for complex, multi-step work in the current session. Skip simple requests. " +
		"Read before resuming or updating an existing plan; write replaces the entire list, so preserve stable IDs and unfinished steps. " +
		"Mark steps in_progress when starting and completed only after verifying the work; use cancelled for abandoned steps. " +
		"Clear obsolete plans with write and todos: []. Plans are in memory only, lost on restart/reload; this tool does not execute or schedule tasks."
}
func (t *TodoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"read", "write"}},
			"todos": map[string]any{
				"type": "array", "maxItems": maxTodos,
				"description": "Required for write: the full replacement list. An empty array clears it.",
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"properties": map[string]any{
						"id":       map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
						"content":  map[string]any{"type": "string", "minLength": 1, "maxLength": 1024},
						"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}, "default": "pending"},
						"priority": map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}, "default": "medium"},
					},
					"required": []string{"id", "content"},
				},
			},
		},
		"required": []string{"action"},
	}
}

func (t *TodoTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if err := ctx.Err(); err != nil {
		return ErrorResult(err.Error())
	}
	key := todoSession{ToolAgentID(ctx), ToolSessionKey(ctx)}
	if key.agent == "" || key.session == "" {
		return ErrorResult("todo requires an agent and session context")
	}
	action, _ := args["action"].(string)
	if action != "read" && action != "write" {
		return ErrorResult("action must be read or write")
	}
	for name := range args {
		if name != "action" && name != "todos" {
			return ErrorResult("unknown argument: " + name)
		}
	}
	var todos []TodoItem
	if action == "read" {
		if _, exists := args["todos"]; exists {
			return ErrorResult("todos is only accepted for write")
		}
	} else {
		raw, err := json.Marshal(args["todos"])
		if err != nil || len(raw) == 0 || raw[0] != '[' {
			return ErrorResult("write requires a todos array; use [] to clear")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		// Decode each item with defaults, while rejecting explicit null fields.
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return ErrorResult("todos must contain objects")
		}
		if len(items) > maxTodos {
			return ErrorResult(fmt.Sprintf("cannot store more than %d todos", maxTodos))
		}
		if err := decoder.Decode(&todos); err != nil {
			return ErrorResult("invalid todos: " + err.Error())
		}
		ids := make(map[string]bool, len(todos))
		for i := range todos {
			item := &todos[i]
			for _, value := range items[i] {
				if string(value) == "null" {
					return ErrorResult("todo fields must not be null")
				}
			}
			if strings.TrimSpace(item.ID) == "" || len(item.ID) > 128 {
				return ErrorResult("todo id must contain 1-128 bytes of nonblank text")
			}
			if strings.TrimSpace(item.Content) == "" || len(item.Content) > 1024 {
				return ErrorResult("todo content must contain 1-1024 bytes of nonblank text")
			}
			if ids[item.ID] {
				return ErrorResult("todo IDs must be unique")
			}
			ids[item.ID] = true
			if _, exists := items[i]["status"]; !exists {
				item.Status = "pending"
			}
			if _, exists := items[i]["priority"]; !exists {
				item.Priority = "medium"
			}
			switch item.Status {
			case "pending", "in_progress", "completed", "cancelled":
			default:
				return ErrorResult("invalid todo status")
			}
			switch item.Priority {
			case "low", "medium", "high":
			default:
				return ErrorResult("invalid todo priority")
			}
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ErrorResult(err.Error())
	}
	verb := "Retrieved"
	if action == "write" {
		if len(todos) == 0 {
			delete(t.lists, key)
		} else {
			if _, exists := t.lists[key]; !exists && len(t.lists) >= maxTodoSessions {
				return ErrorResult("todo session capacity reached; clear an obsolete plan before creating another")
			}
			t.lists[key] = todos
		}
		verb = "Updated"
	}
	todos = t.lists[key]
	if todos == nil {
		todos = []TodoItem{}
	}
	data, _ := json.Marshal(struct {
		Todos      []TodoItem `json:"todos"`
		TotalCount int        `json:"total_count"`
		Message    string     `json:"message"`
	}{todos, len(todos), fmt.Sprintf("%s %d todos", verb, len(todos))})
	return SilentResult(string(data))
}
