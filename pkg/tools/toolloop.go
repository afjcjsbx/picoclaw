// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	cliprovider "github.com/sipeed/picoclaw/pkg/providers/cli"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// maxParallelToolCalls bounds how many tool calls from a single assistant
// response run at the same time. The count is model-controlled, and every call
// can spawn a process or open network connections, so the fan-out must not be
// unbounded.
const maxParallelToolCalls = 8

// ToolLoopConfig configures the tool execution loop.
type ToolLoopConfig struct {
	Provider      providers.LLMProvider
	Model         string
	Tools         *ToolRegistry
	MaxIterations int
	LLMOptions    map[string]any

	// MaxParallel caps how many parallel-safe tool calls of one response run
	// at once. 0 uses maxParallelToolCalls, 1 runs every call sequentially.
	MaxParallel int

	// MediaResolver resolves media:// refs in messages before each LLM call.
	// This is optional and is mainly used by subagent legacy fallback execution
	// so subagents can reuse the same multimodal media handling as the main loop.
	MediaResolver func(messages []providers.Message) []providers.Message
}

// ToolLoopResult contains the result of running the tool loop.
type ToolLoopResult struct {
	Content    string
	Iterations int
}

// RunToolLoop executes the LLM + tool call iteration loop.
// This is the core agent logic that can be reused by both main agent and subagents.
func RunToolLoop(
	ctx context.Context,
	config ToolLoopConfig,
	messages []providers.Message,
	channel, chatID string,
) (*ToolLoopResult, error) {
	iteration := 0
	var finalContent string

	for iteration < config.MaxIterations {
		iteration++

		logger.DebugCF("toolloop", "LLM iteration",
			map[string]any{
				"iteration": iteration,
				"max":       config.MaxIterations,
			})

		// 1. Build tool definitions
		var providerToolDefs []providers.ToolDefinition
		if config.Tools != nil {
			providerToolDefs = config.Tools.ToProviderDefs()
		}

		// 2. Set default LLM options
		llmOpts := config.LLMOptions
		if llmOpts == nil {
			llmOpts = map[string]any{}
		}

		// 3. Resolve media:// refs and Call LLM.
		// Tools like load_image produce media:// refs in their result messages.
		// Without this step, the LLM would receive raw "media://uuid" strings
		// instead of base64-encoded image data URLs.
		//
		// We build a separate callMessages slice so that:
		//   (a) the resolver output is used for the LLM call only,
		//   (b) the original `messages` slice keeps the unresolved refs for
		//       subsequent iterations — the resolver is idempotent but working
		//       on the original avoids double-encoding issues.
		//
		// On iteration 1 the initial user messages typically have no media://
		// refs (they come from plain text), so this is effectively a no-op;
		// it becomes relevant from iteration 2 onward when tool results may
		// contain media refs.
		callMessages := messages
		if config.MediaResolver != nil && iteration > 1 {
			callMessages = config.MediaResolver(messages)
		}
		response, err := config.Provider.Chat(ctx, callMessages, providerToolDefs, config.Model, llmOpts)
		if err != nil {
			logger.ErrorCF("toolloop", "LLM call failed",
				map[string]any{
					"iteration": iteration,
					"error":     err.Error(),
				})
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}

		// 4. If no tool calls, we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			logger.InfoCF("toolloop", "LLM response without tool calls (direct answer)",
				map[string]any{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		normalizedToolCalls := make([]providers.ToolCall, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			normalizedToolCalls = append(normalizedToolCalls, cliprovider.NormalizeToolCall(tc))
		}

		// 5. Log tool calls
		toolNames := make([]string, 0, len(normalizedToolCalls))
		for _, tc := range normalizedToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("toolloop", "LLM requested tool calls",
			map[string]any{
				"tools":     toolNames,
				"count":     len(normalizedToolCalls),
				"iteration": iteration,
			})

		// 6. Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
		}
		for _, tc := range normalizedToolCalls {
			argumentsJSON, err := json.Marshal(tc.Arguments)
			if err != nil {
				logger.Warnf("toolloop: failed to marshal tool call arguments for %s: %v", tc.Name, err)
				argumentsJSON = []byte("{}")
			}
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:        tc.ID,
				Type:      "function",
				Name:      tc.Name,
				Arguments: tc.Arguments,
				Function: &providers.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argumentsJSON),
				},
			})
		}
		messages = append(messages, assistantMsg)

		// 7. Execute the tool calls segment by segment: consecutive calls that
		// declare themselves parallel-safe run concurrently (bounded), everything
		// else runs one by one in the model's order.
		results := make([]*ToolResult, len(normalizedToolCalls))
		runCall := func(i int) {
			tc := normalizedToolCalls[i]
			logger.InfoCF("toolloop", fmt.Sprintf("Tool call: %s", tc.Name),
				map[string]any{
					"tool":      tc.Name,
					"arg_keys":  utils.SortedArgKeys(tc.Arguments),
					"arg_count": len(tc.Arguments),
					"iteration": iteration,
				})
			if config.Tools == nil {
				results[i] = ErrorResult("No tools available")
				return
			}
			results[i] = config.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, channel, chatID, nil)
		}
		maxParallel := config.MaxParallel
		if maxParallel == 0 {
			maxParallel = maxParallelToolCalls
		}
		for _, seg := range PlanBatch(config.Tools, normalizedToolCalls) {
			if seg.Parallel && maxParallel > 1 {
				RunConcurrent(len(seg.Calls), maxParallel, func(n int) { runCall(seg.Calls[n]) })
				continue
			}
			for _, i := range seg.Calls {
				runCall(i)
			}
		}

		// Append results in original order
		for i, tc := range normalizedToolCalls {
			result := results[i]
			if result == nil {
				result = ErrorResult(fmt.Sprintf("internal panic in tool %s", tc.Name))
			}
			toolMsg := providers.Message{
				Role:       "tool",
				Content:    result.ContentForLLM(),
				ToolCallID: tc.ID,
			}
			if len(result.Media) > 0 && !result.ResponseHandled {
				toolMsg.Media = append(toolMsg.Media, result.Media...)
			}
			messages = append(messages, toolMsg)
		}
	}

	return &ToolLoopResult{
		Content:    finalContent,
		Iterations: iteration,
	}, nil
}
