package evolution

import "testing"

func TestDecodeLLMObject(t *testing.T) {
	type shape struct {
		Success *bool  `json:"success"`
		Reason  string `json:"reason"`
	}
	hasSuccess := func(s shape) bool { return s.Success != nil }

	tests := []struct {
		name    string
		content string
		wantOK  bool
		reason  string
	}{
		{"bare object", `{"success":true,"reason":"a"}`, true, "a"},
		{"fenced", "```json\n{\"success\":true,\"reason\":\"a\"}\n```", true, "a"},
		{"prose around a fence", "Here you go:\n```json\n{\"success\":true,\"reason\":\"a\"}\n```\nHope that helps.", true, "a"},
		{"prose around a bare object", `The answer is {"success":false,"reason":"a"} as asked.`, true, "a"},
		{"brace in prose first", `Using {name} here: {"success":true,"reason":"a"}`, true, "a"},
		{"wrong shape first", `For example {"foo":1}, so: {"success":true,"reason":"a"}`, true, "a"},
		{"first of two valid wins", `{"success":true,"reason":"a"} {"success":false,"reason":"b"}`, true, "a"},
		{"wrong-shape object's nested object is not tried", `{"example":{"success":true,"reason":"a"}}`, false, ""},
		{"wrong field type, then valid", `{"success":"yes"} {"success":true,"reason":"a"}`, true, "a"},
		{"braces inside a string", `{"success":true,"reason":"use {x}"}`, true, "use {x}"},
		{"multi-byte prose before the object", `Résultat : {"success":true,"reason":"é"} ok`, true, "é"},
		{"empty", "  \n", false, ""},
		{"no object", "I could not do that.", false, ""},
		{"truncated", `{"success":true,"reason":"a`, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeLLMObject("test", tt.content, hasSuccess)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if ok && got.Reason != tt.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tt.reason)
			}
		})
	}
}

func TestParseLLMDraftReadsPastProse(t *testing.T) {
	content := "Sure, here is the draft:\n```json\n" +
		`{"target_skill_name":"weather-lookup","draft_type":"workflow","change_kind":"create",` +
		`"human_summary":"Look up the weather.","body_or_patch":"# Weather\n\nUse the weather tool."}` +
		"\n```\nLet me know."
	draft, ok := parseLLMDraft(content)
	if !ok {
		t.Fatal("draft not read")
	}
	if draft.TargetSkillName != "weather-lookup" {
		t.Fatalf("target = %q", draft.TargetSkillName)
	}
}

func TestParseLLMDraftSkipsAnInvalidExample(t *testing.T) {
	if _, ok := parseLLMDraft(`Shape: {"target_skill_name":""}`); ok {
		t.Fatal("an invalid draft was accepted")
	}
}

func TestParseLLMClusterResponse(t *testing.T) {
	payload, ok := parseLLMClusterResponse(`Clusters: {"label":"x"} then {"clusters":[{"label":"a-b","task_record_ids":["1"]}]} done`)
	if !ok || len(payload.Clusters) != 1 || payload.Clusters[0].Label != "a-b" {
		t.Fatalf("got %+v, ok=%v", payload, ok)
	}
	if _, ok := parseLLMClusterResponse(`{"label":"x"}`); ok {
		t.Fatal("an object without clusters was accepted")
	}
	if payload, ok := parseLLMClusterResponse(`{"clusters":[]}`); !ok || len(payload.Clusters) != 0 {
		t.Fatalf("empty clusters: got %+v, ok=%v", payload, ok)
	}
}

func TestParseLLMTaskSuccess(t *testing.T) {
	decision, ok := parseLLMTaskSuccess("Verdict:\n```json\n{\"success\":false,\"reason\":\" not done \"}\n```\nThanks.")
	if !ok || decision.Success || decision.Reason != "not done" {
		t.Fatalf("got %+v, ok=%v", decision, ok)
	}
	if _, ok := parseLLMTaskSuccess(`{"reason":"no verdict"}`); ok {
		t.Fatal("an object without success was accepted")
	}
}
