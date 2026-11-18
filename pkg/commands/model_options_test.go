package commands

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestCollectModelOptions_SkipsNilAndEmptyNames(t *testing.T) {
	cfg := &config.Config{}
	cfg.ModelList = []*config.ModelConfig{
		nil,
		{ModelName: "", Provider: "openrouter", Model: "openrouter/x"},
		{ModelName: "gpt-4", Provider: "openrouter", Model: "openrouter/gpt-4"},
	}

	options := CollectModelOptions(cfg)

	if len(options) != 1 {
		t.Fatalf("got %d options, want 1: %+v", len(options), options)
	}
	if options[0].Name != "gpt-4" || options[0].Index != 1 {
		t.Fatalf("got %+v, want index 1 name gpt-4", options[0])
	}
}

func TestCollectModelOptions_CollapsesDuplicateAliases(t *testing.T) {
	cfg := &config.Config{}
	for i := 0; i < 3; i++ {
		cfg.ModelList = append(cfg.ModelList, &config.ModelConfig{
			ModelName: "balanced",
			Provider:  "openrouter",
			Model:     "openrouter/model",
		})
	}
	cfg.ModelList = append(cfg.ModelList, &config.ModelConfig{
		ModelName: "cheap",
		Provider:  "openrouter",
		Model:     "openrouter/cheap",
	})

	options := CollectModelOptions(cfg)

	if len(options) != 2 {
		t.Fatalf("got %d options, want 2: %+v", len(options), options)
	}
	if options[0].Name != "balanced" || options[1].Name != "cheap" {
		t.Fatalf("got %+v, want balanced then cheap", options)
	}
	if options[1].Index != 2 {
		t.Fatalf("got index %d for cheap, want 2", options[1].Index)
	}
}

func TestCollectModelOptions_NilConfig(t *testing.T) {
	if options := CollectModelOptions(nil); options != nil {
		t.Fatalf("got %+v, want nil", options)
	}
}

func TestCollectModelOptions_InfersProviderFromModel(t *testing.T) {
	cfg := &config.Config{}
	cfg.ModelList = []*config.ModelConfig{
		{ModelName: "no-provider", Model: "openrouter/some-model"},
	}

	options := CollectModelOptions(cfg)

	if len(options) != 1 {
		t.Fatalf("got %d options, want 1", len(options))
	}
	if options[0].Provider != "openrouter" {
		t.Fatalf("provider=%q, want openrouter", options[0].Provider)
	}
}

func TestFormatModelOptions_Numbered(t *testing.T) {
	options := []ModelOption{
		{Index: 1, Name: "gpt-4", Provider: "openai"},
		{Index: 2, Name: "glm-5.3-flash", Provider: "openrouter"},
	}

	got := FormatModelOptions(options)

	for _, want := range []string{
		"Configured Models:",
		"1. gpt-4 (Provider: openai)",
		"2. glm-5.3-flash (Provider: openrouter)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted list missing %q, got:\n%s", want, got)
		}
	}
}

func TestFormatModelOptions_Empty(t *testing.T) {
	if got := FormatModelOptions(nil); got != "No models configured" {
		t.Fatalf("got %q, want empty-state message", got)
	}
}

func TestResolveModelOption_Index(t *testing.T) {
	options := []ModelOption{
		{Index: 1, Name: "gpt-4"},
		{Index: 2, Name: "glm-5.3-flash"},
	}

	name, candidates := ResolveModelOption(options, "2")

	if name != "glm-5.3-flash" || candidates != nil {
		t.Fatalf("got name=%q candidates=%v, want glm-5.3-flash", name, candidates)
	}
}

func TestResolveModelOption_OutOfRangeIndex(t *testing.T) {
	options := []ModelOption{{Index: 1, Name: "gpt-4"}}

	name, candidates := ResolveModelOption(options, "5")

	if name != "" || candidates != nil {
		t.Fatalf("got name=%q candidates=%v, want empty result", name, candidates)
	}
}

func TestResolveModelOption_ExactName(t *testing.T) {
	options := []ModelOption{{Index: 1, Name: "gpt-4o-mini"}}

	name, _ := ResolveModelOption(options, "gpt-4o-mini")

	if name != "gpt-4o-mini" {
		t.Fatalf("got %q, want gpt-4o-mini", name)
	}
}

func TestResolveModelOption_CaseInsensitiveName(t *testing.T) {
	options := []ModelOption{{Index: 1, Name: "GLM-5.3-Flash"}}

	name, _ := ResolveModelOption(options, "glm-5.3-flash")

	if name != "GLM-5.3-Flash" {
		t.Fatalf("got %q, want canonical name", name)
	}
}

func TestResolveModelOption_PrefixBeatsIndexWhenNotNumeric(t *testing.T) {
	options := []ModelOption{
		{Index: 1, Name: "gpt-4"},
		{Index: 2, Name: "gpt-4o-mini"},
	}

	name, candidates := ResolveModelOption(options, "gpt-4o")

	if name != "gpt-4o-mini" || candidates != nil {
		t.Fatalf("got name=%q candidates=%v, want gpt-4o-mini", name, candidates)
	}
}

func TestResolveModelOption_AmbiguousPrefixReturnsCandidates(t *testing.T) {
	options := []ModelOption{
		{Index: 1, Name: "gemini-3-5-flash-lite-free"},
		{Index: 2, Name: "gemini-3-pro"},
	}

	name, candidates := ResolveModelOption(options, "gemini")

	if name != "" {
		t.Fatalf("got name=%q, want empty for ambiguous prefix", name)
	}
	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2: %v", len(candidates), candidates)
	}
}

func TestResolveModelOption_Unknown(t *testing.T) {
	options := []ModelOption{{Index: 1, Name: "gpt-4"}}

	name, candidates := ResolveModelOption(options, "nope")

	if name != "" || candidates != nil {
		t.Fatalf("got name=%q candidates=%v, want empty result", name, candidates)
	}
}

func TestResolveModelOption_EmptyValue(t *testing.T) {
	options := []ModelOption{{Index: 1, Name: "gpt-4"}}

	name, candidates := ResolveModelOption(options, "  ")

	if name != "" || candidates != nil {
		t.Fatalf("got name=%q candidates=%v, want empty result", name, candidates)
	}
}

func TestListModels_NumberedOutput(t *testing.T) {
	rt := &Runtime{Config: testModelConfig("gpt-4", "glm-5.3-flash")}

	reply := runCommand(t, rt, "/list models")

	for _, want := range []string{
		"1. gpt-4 (Provider: openrouter)",
		"2. glm-5.3-flash (Provider: openrouter)",
		"/switch model <index>",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestListModels_EmptyState(t *testing.T) {
	rt := &Runtime{Config: &config.Config{}}

	reply := runCommand(t, rt, "/list models")

	if reply != "No models configured" {
		t.Fatalf("reply=%q, want empty-state message", reply)
	}
}
