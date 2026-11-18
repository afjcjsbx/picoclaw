package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// testModelConfig builds a config with one entry per alias, all on openrouter.
func testModelConfig(names ...string) *config.Config {
	cfg := &config.Config{}
	cfg.Agents.Defaults.Provider = "openrouter"
	for _, name := range names {
		cfg.ModelList = append(cfg.ModelList, &config.ModelConfig{
			ModelName: name,
			Provider:  "openrouter",
			Model:     "openrouter/" + name,
		})
	}
	return cfg
}

// runCommand executes a command against rt and returns the reply. It is shared
// with cmd_check_test.go.
func runCommand(t *testing.T, rt *Runtime, text string) string {
	t.Helper()
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: text,
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("%s: outcome=%v, want=%v", text, res.Outcome, OutcomeHandled)
	}
	return reply
}

func TestSwitchModel_ByName(t *testing.T) {
	var switched string
	rt := &Runtime{
		Config: testModelConfig("gpt-4", "deepseek-v4-flash"),
		SwitchModel: func(value string) (string, error) {
			switched = value
			return "old-model", nil
		},
	}

	reply := runCommand(t, rt, "/switch model deepseek-v4-flash")

	if switched != "deepseek-v4-flash" {
		t.Fatalf("switched to %q, want %q", switched, "deepseek-v4-flash")
	}
	want := "Switched model from old-model to deepseek-v4-flash"
	if reply != want {
		t.Fatalf("reply=%q, want=%q", reply, want)
	}
}

func TestSwitchModel_ByIndex(t *testing.T) {
	var switched string
	rt := &Runtime{
		Config: testModelConfig("gpt-4", "deepseek-v4-flash", "glm-5.3-flash"),
		SwitchModel: func(value string) (string, error) {
			switched = value
			return "old-model", nil
		},
	}

	reply := runCommand(t, rt, "/switch model 2")

	if switched != "deepseek-v4-flash" {
		t.Fatalf("switched to %q, want %q", switched, "deepseek-v4-flash")
	}
	if !strings.Contains(reply, "to deepseek-v4-flash") {
		t.Fatalf("reply=%q, want target name in success message", reply)
	}
}

func TestSwitchModel_LegacyToKeywordStillWorks(t *testing.T) {
	var switched string
	rt := &Runtime{
		Config: testModelConfig("gpt-4"),
		SwitchModel: func(value string) (string, error) {
			switched = value
			return "old", nil
		},
	}

	runCommand(t, rt, "/switch model to gpt-4")

	if switched != "gpt-4" {
		t.Fatalf("switched to %q, want %q", switched, "gpt-4")
	}
}

func TestSwitchModel_UnambiguousPrefix(t *testing.T) {
	var switched string
	rt := &Runtime{
		Config: testModelConfig("minimax-m2.7", "minimax-m3.1-flash-free"),
		SwitchModel: func(value string) (string, error) {
			switched = value
			return "old", nil
		},
	}

	runCommand(t, rt, "/switch model minimax-m3")

	if switched != "minimax-m3.1-flash-free" {
		t.Fatalf("switched to %q, want %q", switched, "minimax-m3.1-flash-free")
	}
}

func TestSwitchModel_AmbiguousPrefixListsCandidates(t *testing.T) {
	called := false
	rt := &Runtime{
		Config: testModelConfig("gemini-3-5-flash-lite-free", "gemini-3-pro"),
		SwitchModel: func(_ string) (string, error) {
			called = true
			return "old", nil
		},
	}

	reply := runCommand(t, rt, "/switch model gemini")

	if called {
		t.Fatal("SwitchModel must not be called for an ambiguous prefix")
	}
	for _, want := range []string{"Ambiguous model", "gemini-3-5-flash-lite-free", "gemini-3-pro"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestSwitchModel_BareListsNumberedModels(t *testing.T) {
	called := false
	rt := &Runtime{
		Config: testModelConfig("gpt-4", "deepseek-v4-flash"),
		SwitchModel: func(_ string) (string, error) {
			called = true
			return "old", nil
		},
	}

	reply := runCommand(t, rt, "/switch model")

	if called {
		t.Fatal("bare /switch model must not switch")
	}
	for _, want := range []string{"1. gpt-4", "2. deepseek-v4-flash", "/switch model <index>"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply=%q missing %q", reply, want)
		}
	}
}

func TestSwitchModel_UnknownValuePassesThroughToRuntime(t *testing.T) {
	rt := &Runtime{
		Config: testModelConfig("gpt-4"),
		SwitchModel: func(value string) (string, error) {
			return "", fmt.Errorf("model %q not found in model_list or providers", value)
		},
	}

	reply := runCommand(t, rt, "/switch model nope")

	if reply != `model "nope" not found in model_list or providers` {
		t.Fatalf("reply=%q, want runtime error passthrough", reply)
	}
}

func TestSwitchModel_OutOfRangeIndexPassesThrough(t *testing.T) {
	rt := &Runtime{
		Config: testModelConfig("gpt-4"),
		SwitchModel: func(value string) (string, error) {
			return "", fmt.Errorf("model %q not found in model_list or providers", value)
		},
	}

	reply := runCommand(t, rt, "/switch model 99")

	if !strings.Contains(reply, `"99" not found`) {
		t.Fatalf("reply=%q, want runtime error for unresolvable index", reply)
	}
}

func TestSwitchModel_NilDep(t *testing.T) {
	reply := runCommand(t, &Runtime{}, "/switch model 1")

	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want %q", reply, unavailableMsg)
	}
}

func TestSwitch_BangPrefix(t *testing.T) {
	var switched string
	rt := &Runtime{
		Config: testModelConfig("gpt-4"),
		SwitchModel: func(value string) (string, error) {
			switched = value
			return "old", nil
		},
	}

	reply := runCommand(t, rt, "!switch model 1")

	if switched != "gpt-4" {
		t.Fatalf("switched to %q, want %q", switched, "gpt-4")
	}
	if reply != "Switched model from old to gpt-4" {
		t.Fatalf("reply=%q, want success message", reply)
	}
}

func TestSwitch_NoSubCommand(t *testing.T) {
	reply := runCommand(t, &Runtime{}, "/switch")

	if reply == "" {
		t.Fatal("expected usage reply for bare /switch")
	}
	if !strings.Contains(reply, "/switch [") || !strings.Contains(reply, "model") {
		t.Fatalf("reply=%q, want usage listing sub-commands", reply)
	}
}

func TestSwitchChannel_Redirect(t *testing.T) {
	reply := runCommand(t, &Runtime{}, "/switch channel telegram")

	want := "This command has moved. Please use: /check channel <name>"
	if reply != want {
		t.Fatalf("reply=%q, want=%q", reply, want)
	}
}
