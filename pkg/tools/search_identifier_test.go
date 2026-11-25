package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// identifierTool describes a hidden MCP-style tool for discovery tests.
type identifierTool struct {
	server     string
	logical    string
	registered string
	desc       string
}

// setupIdentifierRegistry registers a mixed set of MCP-style hidden tools with
// hashed identifiers, server prefixes and descriptions that cross-reference
// other tool names. The tool names intentionally contain underscores, hyphens,
// slashes, colons and FNV-style hash suffixes.
func setupIdentifierRegistry() (*ToolRegistry, []identifierTool) {
	tools := []identifierTool{
		{
			server:     "acme",
			logical:    "ACME_SEARCH_TOOLS",
			registered: "mcp_acme_search_tools_ba2b8b72",
			desc:       "[MCP:acme] Search the catalog of available tools across connected apps.",
		},
		{
			server:     "acme",
			logical:    "ACME_GET_TOOL_SCHEMAS",
			registered: "mcp_acme_get_tool_schemas_e1e720b2",
			desc:       "[MCP:acme] Retrieve input schemas for a tool. Only pass slugs returned by ACME_SEARCH_TOOLS.",
		},
		{
			server:     "acme",
			logical:    "ACME_MANAGE_CONNECTIONS",
			registered: "mcp_acme_manage_connections_08299a6b",
			desc:       "[MCP:acme] Create or manage connections to user apps. First call ACME_SEARCH_TOOLS.",
		},
		{
			server:     "acme",
			logical:    "ACME_MULTI_EXECUTE_TOOL",
			registered: "mcp_acme_multi_execute_tool_d55b971a",
			desc:       "[MCP:acme] Fast parallel executor for tools discovered through ACME_SEARCH_TOOLS.",
		},
		{
			server:     "acme",
			logical:    "ACME_PROXY_EXECUTE",
			registered: "mcp_acme_proxy_execute_402b20da",
			desc:       "[MCP:acme] Send a raw HTTP request to a connected app API.",
		},
		{
			server:     "acme",
			logical:    "ACME_REMOTE_BASH_TOOL",
			registered: "mcp_acme_remote_bash_tool_a20b5876",
			desc:       "[MCP:acme] Execute bash commands in a remote sandbox.",
		},
		{
			server:     "acme",
			logical:    "ACME_REMOTE_WORKBENCH",
			registered: "mcp_acme_remote_workbench_2a6670c2",
			desc:       "[MCP:acme] Persistent Jupyter sandbox for scripted bulk executions.",
		},
		{
			server:     "acme",
			logical:    "ACME_SEARCH_SKILLS",
			registered: "mcp_acme_search_skills_9a6b2ab1",
			desc:       "[MCP:acme] Search the skills saved in the organization.",
		},
		{
			server:     "acme",
			logical:    "ACME_MANAGE_SKILL",
			registered: "mcp_acme_manage_skill_4e37fc67",
			desc:       "[MCP:acme] Create, update, or delete a skill.",
		},
		{
			server:     "acme",
			logical:    "ACME_USE_SKILL",
			registered: "mcp_acme_use_skill_0a0ad4fb",
			desc:       "[MCP:acme] Return the markdown of one skill by slug.",
		},
		{
			server:     "acme",
			logical:    "ACME_WAIT_FOR_CONNECTIONS",
			registered: "mcp_acme_wait_for_connections_dca17305",
			desc:       "[MCP:acme] Wait for user authentication to finish.",
		},
		{
			server:     "acme",
			logical:    "ACME_SUBMIT_FEEDBACK",
			registered: "mcp_acme_submit_feedback_0995c0c0",
			desc:       "[MCP:acme] Report a tool execution that returned incorrect results.",
		},
		{
			server:     "globex",
			logical:    "GLOBEX_CREATE_ISSUE",
			registered: "mcp_globex_create_issue_1a2b3c4d",
			desc:       "[MCP:globex] Create an issue in a repository.",
		},
		{
			server:     "globex",
			logical:    "GLOBEX_LIST_PULL_REQUESTS",
			registered: "mcp_globex_list_pull_requests_5e6f7a8b",
			desc:       "[MCP:globex] List pull requests in a repository.",
		},
	}

	reg := NewToolRegistry()
	for _, tool := range tools {
		reg.RegisterHidden(&mockSearchableTool{name: tool.registered, desc: tool.desc})
	}
	return reg, tools
}

func resultRank(results []ToolSearchResult, name string) int {
	for i, result := range results {
		if result.Name == name {
			return i + 1
		}
	}
	return 0
}

func resultNames(results []ToolSearchResult) []string {
	names := make([]string, len(results))
	for i, result := range results {
		names[i] = result.Name
	}
	return names
}

func runIdentifierSearch(t *testing.T, reg *ToolRegistry, query string, maxResults int) []ToolSearchResult {
	t.Helper()
	search := NewBM25SearchTool(reg, 5, maxResults)
	res := search.Execute(context.Background(), map[string]any{"query": query})
	return discoveryResults(t, res)
}

func TestExpandSearchIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "mcp name with hash",
			input: "mcp_acme_search_tools_ba2b8b72",
			want:  "mcp acme search tools ba2b8b72",
		},
		{
			name:  "uppercase identifier",
			input: "ACME_SEARCH_TOOLS",
			want:  "acme search tools",
		},
		{
			name:  "hyphenated identifier",
			input: "mcp_acme_remote-bash_tool",
			want:  "mcp acme remote bash tool",
		},
		{
			name:  "mcp description prefix",
			input: "[MCP:acme] Search tools",
			want:  "mcp acme search tools",
		},
		{
			name:  "mixed separators",
			input: "a/b:c[d]e",
			want:  "a b c d e",
		},
		{
			name:  "repeated separators and dots",
			input: "multiple___underscores---and...dots",
			want:  "multiple underscores and dots",
		},
		{
			name:  "unicode letters",
			input: "café_tool",
			want:  "café tool",
		},
		{
			name:  "surrounding spaces",
			input: "  spaced   out  ",
			want:  "spaced out",
		},
		{
			name:  "digits are preserved",
			input: "tool_v2_2026",
			want:  "tool v2 2026",
		},
		{
			name:  "only separators",
			input: ".,;:!?___---",
			want:  "",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandSearchIdentifier(tt.input); got != tt.want {
				t.Errorf("expandSearchIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandSearchIdentifier_Idempotent(t *testing.T) {
	inputs := []string{
		"mcp_acme_search_tools_ba2b8b72",
		"[MCP:acme] Create an issue",
		"a/b:c[d]e",
		"café_tool",
		"",
		".,;:!?",
	}
	for _, input := range inputs {
		once := expandSearchIdentifier(input)
		twice := expandSearchIdentifier(once)
		if once != twice {
			t.Errorf("expansion of %q is not idempotent: %q != %q", input, once, twice)
		}
	}
}

func TestBM25SearchTool_ExactIdentifierQueries(t *testing.T) {
	reg, tools := setupIdentifierRegistry()
	search := NewBM25SearchTool(reg, 5, len(tools))
	ctx := context.Background()

	for _, tool := range tools {
		t.Run(tool.logical, func(t *testing.T) {
			queries := []string{
				tool.logical,
				strings.ToLower(tool.logical),
				tool.registered,
				strings.ReplaceAll(tool.logical, "_", "-"),
				strings.ReplaceAll(tool.logical, "_", "."),
				strings.ReplaceAll(tool.logical, "_", "/"),
				strings.ReplaceAll(tool.logical, "_", ":"),
				"MCP." + tool.logical + "/tool",
			}
			for _, query := range queries {
				res := search.Execute(ctx, map[string]any{"query": query})
				results := discoveryResults(t, res)
				if rank := resultRank(results, tool.registered); rank != 1 {
					t.Errorf(
						"query %q: %s ranked %d, want 1 (results: %v)",
						query, tool.registered, rank, resultNames(results),
					)
				}
			}
		})
	}
}

func TestBM25SearchTool_DescriptionSearchPreserved(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_workbench_c3d4e5f6",
		desc: "[MCP:acme] Process invoice batches and generate receipts.",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_remote_a1b2c3d4",
		desc: "[MCP:acme] Execute bash commands in a sandbox.",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_mailer_9f8e7d6c",
		desc: "[MCP:acme] Send email and manage calendar events.",
	})

	tests := []struct {
		query string
		want  string
	}{
		{"invoice", "mcp_acme_workbench_c3d4e5f6"},
		{"receipts", "mcp_acme_workbench_c3d4e5f6"},
		{"bash", "mcp_acme_remote_a1b2c3d4"},
		{"sandbox", "mcp_acme_remote_a1b2c3d4"},
		{"send email", "mcp_acme_mailer_9f8e7d6c"},
		{"calendar events", "mcp_acme_mailer_9f8e7d6c"},
		{"calendar, email.", "mcp_acme_mailer_9f8e7d6c"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			results := runIdentifierSearch(t, reg, tt.query, 10)
			if rank := resultRank(results, tt.want); rank != 1 {
				t.Errorf("query %q: %s ranked %d, want 1 (results: %v)",
					tt.query, tt.want, rank, resultNames(results))
			}
		})
	}
}

func TestBM25SearchTool_IdentifierReferencesDoNotPolluteDescriptions(t *testing.T) {
	reg := NewToolRegistry()
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_search_tools_ba2b8b72",
		desc: "[MCP:acme] Search the catalog of available tools.",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_get_tool_schemas_e1e720b2",
		desc: "[MCP:acme] Retrieve schemas. Only pass slugs returned by ACME_SEARCH_TOOLS.",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_manage_connections_08299a6b",
		desc: "[MCP:acme] Manage connections. First call ACME_SEARCH_TOOLS.",
	})
	reg.RegisterHidden(&mockSearchableTool{
		name: "mcp_acme_multi_execute_tool_d55b971a",
		desc: "[MCP:acme] Execute tools discovered through ACME_SEARCH_TOOLS.",
	})

	queries := []string{
		"ACME_SEARCH_TOOLS",
		"acme_search_tools",
		"ACME.SEARCH-TOOLS",
		"search tools",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			results := runIdentifierSearch(t, reg, query, 10)
			if rank := resultRank(results, "mcp_acme_search_tools_ba2b8b72"); rank != 1 {
				t.Errorf("query %q: search tool ranked %d, want 1 (results: %v)",
					query, rank, resultNames(results))
			}
		})
	}
}

func TestBM25SearchTool_ServerNameQueries(t *testing.T) {
	reg, tools := setupIdentifierRegistry()

	t.Run("acme returns only acme tools", func(t *testing.T) {
		results := runIdentifierSearch(t, reg, "acme", len(tools))
		if len(results) != 12 {
			t.Fatalf("got %d results, want 12 (results: %v)", len(results), resultNames(results))
		}
		for _, result := range results {
			if !strings.HasPrefix(result.Name, "mcp_acme_") {
				t.Errorf("query %q returned a non-acme tool: %s", "acme", result.Name)
			}
		}
	})

	t.Run("globex returns only globex tools", func(t *testing.T) {
		results := runIdentifierSearch(t, reg, "globex", len(tools))
		if len(results) != 2 {
			t.Fatalf("got %d results, want 2 (results: %v)", len(results), resultNames(results))
		}
		for _, result := range results {
			if !strings.HasPrefix(result.Name, "mcp_globex_") {
				t.Errorf("query %q returned a non-globex tool: %s", "globex", result.Name)
			}
		}
	})

	t.Run("server name with punctuation", func(t *testing.T) {
		for _, query := range []string{"ACME", "acme-tools", "acme.search/tools"} {
			results := runIdentifierSearch(t, reg, query, len(tools))
			if len(results) != 12 {
				t.Errorf("query %q: got %d results, want 12 (results: %v)",
					query, len(results), resultNames(results))
				continue
			}
			for _, result := range results {
				if !strings.HasPrefix(result.Name, "mcp_acme_") {
					t.Errorf("query %q returned a non-acme tool: %s", query, result.Name)
				}
			}
		}
	})
}

func TestBM25SearchTool_IdentifierOnlyPunctuationQuery(t *testing.T) {
	reg, _ := setupIdentifierRegistry()

	for _, query := range []string{"___", ":::", "...", "-_-", "[]/\\"} {
		t.Run(query, func(t *testing.T) {
			search := NewBM25SearchTool(reg, 5, 10)
			res := search.Execute(context.Background(), map[string]any{"query": query})
			if res.IsError {
				t.Fatalf("unexpected error: %s", res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, "No tools found matching the query") {
				t.Errorf("query %q: expected no-match response, got %q", query, res.ForLLM)
			}
		})
	}
}

func TestBM25SearchTool_IdentifierQueryPromotesTool(t *testing.T) {
	reg := NewToolRegistry()
	name := "mcp_acme_search_tools_ba2b8b72"
	reg.RegisterHidden(&mockSearchableTool{
		name: name,
		desc: "[MCP:acme] Search the catalog of available tools.",
	})

	if _, ok := reg.Get(name); ok {
		t.Fatal("hidden tool should not be callable before discovery")
	}

	results := runIdentifierSearch(t, reg, "ACME_SEARCH_TOOLS", 10)
	if rank := resultRank(results, name); rank != 1 {
		t.Fatalf("tool ranked %d, want 1 (results: %v)", rank, resultNames(results))
	}
	if _, ok := reg.Get(name); !ok {
		t.Error("hidden tool should be callable after identifier discovery")
	}
}

func TestBM25SearchTool_IdentifierCacheInvalidation(t *testing.T) {
	reg := NewToolRegistry()
	search := NewBM25SearchTool(reg, 5, 10)
	ctx := context.Background()

	alpha := "mcp_acme_alpha_tool_11111111"
	reg.RegisterHidden(&mockSearchableTool{name: alpha, desc: "[MCP:acme] Alpha tool."})

	res := search.Execute(ctx, map[string]any{"query": "ACME_ALPHA_TOOL"})
	results := discoveryResults(t, res)
	if rank := resultRank(results, alpha); rank != 1 {
		t.Fatalf("alpha ranked %d, want 1 (results: %v)", rank, resultNames(results))
	}

	beta := "mcp_acme_beta_tool_22222222"
	reg.RegisterHidden(&mockSearchableTool{name: beta, desc: "[MCP:acme] Beta tool."})

	res = search.Execute(ctx, map[string]any{"query": "ACME_BETA_TOOL"})
	results = discoveryResults(t, res)
	if rank := resultRank(results, beta); rank != 1 {
		t.Fatalf("beta ranked %d, want 1 (results: %v)", rank, resultNames(results))
	}
}

func TestBM25SearchTool_IdentifierResultLimit(t *testing.T) {
	reg, _ := setupIdentifierRegistry()

	for limit := 1; limit <= 5; limit++ {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			results := runIdentifierSearch(t, reg, "acme", limit)
			if len(results) != limit {
				t.Fatalf("got %d results, want %d (results: %v)",
					len(results), limit, resultNames(results))
			}
			for _, result := range results {
				if !strings.HasPrefix(result.Name, "mcp_acme_") {
					t.Errorf("query %q returned a non-acme tool: %s", "acme", result.Name)
				}
			}
		})
	}
}

func TestRegexSearchTool_IdentifierNamesStillMatch(t *testing.T) {
	reg, _ := setupIdentifierRegistry()
	tool := NewRegexSearchTool(reg, 5, 10)

	res := tool.Execute(context.Background(), map[string]any{"pattern": "^mcp_acme_search_tools_"})
	results := discoveryResults(t, res)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (results: %v)", len(results), resultNames(results))
	}
	if results[0].Name != "mcp_acme_search_tools_ba2b8b72" {
		t.Errorf("got %q, want %q", results[0].Name, "mcp_acme_search_tools_ba2b8b72")
	}
}

func TestBM25SearchTool_DescriptionOnlyToolIsFound(t *testing.T) {
	reg := NewToolRegistry()
	name := "mcp_acme_notes_c0ffee00"
	reg.RegisterHidden(&mockSearchableTool{
		name: name,
		desc: "[MCP:acme] Manage project notebooks and meeting notes.",
	})

	for _, query := range []string{"notebooks", "meeting notes", "project notebook"} {
		t.Run(query, func(t *testing.T) {
			results := runIdentifierSearch(t, reg, query, 10)
			if rank := resultRank(results, name); rank != 1 {
				t.Errorf("query %q: %s ranked %d, want 1 (results: %v)",
					query, name, rank, resultNames(results))
			}
		})
	}
}
