package integrationtools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/skills"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	skillsSearchMaxQuery  = 1000
	skillsSearchMaxLimit  = 20
	skillsSearchDefLimit  = 5
	skillsSearchMaxDesc   = 512
	skillsSearchMaxOutput = 16000
)

// SkillsSearchTool looks up installed skills in the catalog. It replaces the
// skill list in the system prompt: the model asks for what it needs.
type SkillsSearchTool struct{ catalog *skills.Catalog }

func NewSkillsSearchTool(catalog *skills.Catalog) *SkillsSearchTool {
	return &SkillsSearchTool{catalog: catalog}
}

func (t *SkillsSearchTool) Name() string { return "skills_search" }

func (t *SkillsSearchTool) Description() string {
	return "Search installed skills by natural language or exact skill name. Returns name, description and " +
		"location only; load the instructions with skills_read."
}

func (t *SkillsSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What you need to do, in natural language, or an exact skill name",
				"maxLength":   skillsSearchMaxQuery,
			},
			"limit": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Maximum results (1-%d, default %d)",
					skillsSearchMaxLimit,
					skillsSearchDefLimit,
				),
				"minimum": 1,
				"maximum": skillsSearchMaxLimit,
			},
		},
		"required": []string{"query"},
	}
}

type skillHit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Location    string `json:"location"`
}

type skillsSearchOutput struct {
	Skills  []skillHit `json:"skills"`
	HasMore bool       `json:"hasMore"`
}

func (t *SkillsSearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return ErrorResult("query is required and must be a non-empty string")
	}
	if utf8.RuneCountInString(query) > skillsSearchMaxQuery {
		return ErrorResult(fmt.Sprintf("query is too long: max %d characters", skillsSearchMaxQuery))
	}
	limit, err := toolshared.Int64Arg(args, "limit", skillsSearchDefLimit)
	if err != nil {
		return ErrorResult(err.Error())
	}
	limit = min(max(limit, 1), skillsSearchMaxLimit)

	hits, more, err := t.catalog.Search(ctx, query, int(limit))
	if err != nil {
		return ErrorResult("skills_search: " + err.Error())
	}

	// Bound the payload before it reaches the model: each description is cut,
	// and results are dropped (hasMore) once the JSON would pass the budget.
	out := skillsSearchOutput{Skills: []skillHit{}, HasMore: more}
	size := len(`{"skills":[],"hasMore":false}`)
	for _, h := range hits {
		item := skillHit{
			Name:        h.Name,
			Description: toolshared.RunePrefix(h.Description, skillsSearchMaxDesc),
			Location:    h.Location(),
		}
		b := marshalPlain(item)
		if size+utf8.RuneCount(b)+1 > skillsSearchMaxOutput {
			out.HasMore = true
			break
		}
		size += utf8.RuneCount(b) + 1
		out.Skills = append(out.Skills, item)
	}
	return SilentResult(string(marshalPlain(out)))
}

// marshalPlain is json.Marshal without HTML escaping, so "<" and "&" in
// descriptions cost one character instead of six.
func marshalPlain(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // plain structs of strings cannot fail
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// SkillsReadTool loads the complete instructions of one installed skill.
type SkillsReadTool struct{ catalog *skills.Catalog }

func NewSkillsReadTool(catalog *skills.Catalog) *SkillsReadTool {
	return &SkillsReadTool{catalog: catalog}
}

func (t *SkillsReadTool) Name() string { return "skills_read" }

func (t *SkillsReadTool) Description() string {
	return fmt.Sprintf(
		"Read the complete instructions of an installed skill (SKILL.md), or a supporting file inside the "+
			"skill directory via relativePath. Files over %d KiB are refused, not truncated.",
		skills.MaxReadBytes>>10,
	)
}

func (t *SkillsReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Exact skill name, as returned by skills_search",
			},
			"relativePath": map[string]any{
				"type":        "string",
				"description": "Optional supporting file inside the skill directory (for example references/api.md)",
			},
		},
		"required": []string{"name"},
	}
}

func (t *SkillsReadTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return ErrorResult("name is required and must be a non-empty string")
	}
	rel, _ := args["relativePath"].(string)

	body, err := t.catalog.Read(ctx, name, strings.TrimSpace(rel))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ErrorResult("skills_read aborted: " + err.Error())
		}
		return ErrorResult("skills_read: " + err.Error())
	}
	res := SilentResult(body)
	// Already bounded by skills.MaxReadBytes. Without this the generic
	// inline-result spill would reduce a long skill to a head/tail preview.
	res.LargeResultSpilled = true
	return res
}

// ParallelSafety marks the call safe: it only reads, so it can run beside any
// other safe call.
func (t *SkillsSearchTool) ParallelSafety(map[string]any) toolshared.ParallelSafety {
	return toolshared.ParallelSafety{Safe: true}
}

func (t *SkillsReadTool) ParallelSafety(map[string]any) toolshared.ParallelSafety {
	return toolshared.ParallelSafety{Safe: true}
}
