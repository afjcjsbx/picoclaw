package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type durationTestProvider struct {
	usage *providers.UsageInfo
}

func (p *durationTestProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	time.Sleep(2 * time.Millisecond)
	return &providers.LLMResponse{Content: "ok", Usage: p.usage}, nil
}

func (p *durationTestProvider) GetDefaultModel() string {
	return "duration-test"
}

func TestChatWithDurationStampsProviderTime(t *testing.T) {
	resp, err := chatWithDuration(
		context.Background(),
		&durationTestProvider{usage: &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5}},
		nil,
		nil,
		"duration-test",
		nil,
	)
	if err != nil {
		t.Fatalf("chatWithDuration() error = %v", err)
	}
	if resp == nil || resp.Usage == nil {
		t.Fatal("chatWithDuration() returned no usage")
	}
	if resp.Usage.DurationMS < 1 {
		t.Fatalf("DurationMS = %d, want the measured provider time", resp.Usage.DurationMS)
	}
}

func TestRecordProviderDurationSkipsMissingUsage(t *testing.T) {
	recordProviderDuration(nil, time.Second)
	resp := &providers.LLMResponse{}
	recordProviderDuration(resp, time.Second)
	if resp.Usage != nil {
		t.Fatalf("Usage = %+v, want nil", resp.Usage)
	}
}
