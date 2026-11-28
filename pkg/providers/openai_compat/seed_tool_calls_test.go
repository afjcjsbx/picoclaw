package openai_compat

import (
	"strings"
	"testing"
)

const seedToolCallSample = `<seed:tool_call>
<function name="get_weather">
<parameter name="city" string="true">Beijing</parameter>
</function>
</seed:tool_call>`

func TestParseSeedToolCallsFromContent_BasicExtraction(t *testing.T) {
	content := "I'll check the weather for you.\n" + seedToolCallSample
	toolCalls, cleaned := parseSeedToolCallsFromContent(content)

	if len(toolCalls) != 1 {
		t.Fatalf("len(toolCalls) = %d, want 1", len(toolCalls))
	}
	tc := toolCalls[0]
	if tc.Name != "get_weather" {
		t.Fatalf("Name = %q, want get_weather", tc.Name)
	}
	if tc.Arguments["city"] != "Beijing" {
		t.Fatalf("Arguments[city] = %v, want Beijing", tc.Arguments["city"])
	}
	if tc.ID != "seed_tc_0" {
		t.Fatalf("ID = %q, want seed_tc_0", tc.ID)
	}
	if strings.Contains(cleaned, seedOpenTag) {
		t.Fatalf("cleaned content still contains seed block: %q", cleaned)
	}
	if !strings.Contains(cleaned, "check the weather") {
		t.Fatalf("cleaned content lost visible text: %q", cleaned)
	}
}

func TestParseSeedToolCallsFromContent_MultipleToolCalls(t *testing.T) {
	content := `<seed:tool_call>
<function name="get_weather">
<parameter name="city">Shanghai</parameter>
</function>
</seed:tool_call>
<seed:tool_call>
<function name="get_time">
<parameter name="timezone">Asia/Shanghai</parameter>
</function>
</seed:tool_call>`
	toolCalls, cleaned := parseSeedToolCallsFromContent(content)

	if len(toolCalls) != 2 {
		t.Fatalf("len(toolCalls) = %d, want 2", len(toolCalls))
	}
	if toolCalls[0].Name != "get_weather" || toolCalls[1].Name != "get_time" {
		t.Fatalf("tool names = %q, %q", toolCalls[0].Name, toolCalls[1].Name)
	}
	if toolCalls[0].ID != "seed_tc_0" || toolCalls[1].ID != "seed_tc_1" {
		t.Fatalf("tool IDs = %q, %q", toolCalls[0].ID, toolCalls[1].ID)
	}
	if cleaned != "" {
		t.Fatalf("cleaned = %q, want empty", cleaned)
	}
}

func TestParseSeedToolCallsFromContent_MalformedKeptAsContent(t *testing.T) {
	malformed := "<seed:tool_call><not-valid-xml</seed:tool_call>"
	toolCalls, cleaned := parseSeedToolCallsFromContent(malformed)

	if len(toolCalls) != 0 {
		t.Fatalf("len(toolCalls) = %d, want 0", len(toolCalls))
	}
	if !strings.Contains(cleaned, seedOpenTag) {
		t.Fatalf("malformed block should be kept, got %q", cleaned)
	}
}

func TestParseSeedToolCallsFromContent_MissingCloseTagKeptAsContent(t *testing.T) {
	content := `text <seed:tool_call><function name="f"></function>`
	toolCalls, cleaned := parseSeedToolCallsFromContent(content)

	if len(toolCalls) != 0 {
		t.Fatalf("len(toolCalls) = %d, want 0", len(toolCalls))
	}
	if !strings.Contains(cleaned, seedOpenTag) {
		t.Fatalf("content with missing close tag should be kept, got %q", cleaned)
	}
}

func TestExtractSeedToolCalls_IgnoresStandardToolCalls(t *testing.T) {
	resp := &LLMResponse{
		Content: seedToolCallSample,
		ToolCalls: []ToolCall{{
			ID:   "native-call",
			Name: "native_tool",
		}},
	}
	got := extractSeedToolCalls(resp)
	if got != resp {
		t.Fatal("response with native tool calls should be returned unchanged")
	}
}

func TestExtractSeedToolCalls_ConvertsContent(t *testing.T) {
	resp := &LLMResponse{
		Content:      "checking\n" + seedToolCallSample,
		FinishReason: "stop",
	}
	got := extractSeedToolCalls(resp)

	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls = %#v", got.ToolCalls)
	}
	if got.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want tool_calls", got.FinishReason)
	}
	if strings.Contains(got.Content, seedOpenTag) {
		t.Fatalf("Content still contains seed block: %q", got.Content)
	}
	if resp.Content == got.Content {
		t.Fatal("original response should not be mutated")
	}
}

func TestStripSeedToolCallStart(t *testing.T) {
	visible, started := stripSeedToolCallStart("checking the weather <seed:tool_call>")
	if !started || visible != "checking the weather" {
		t.Fatalf("stripSeedToolCallStart() = (%q, %v)", visible, started)
	}

	visible, started = stripSeedToolCallStart("plain content")
	if started || visible != "plain content" {
		t.Fatalf("stripSeedToolCallStart() = (%q, %v)", visible, started)
	}
}
