package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type DelegateTaskTool struct {
	models    []config.ModelConfig
	workspace string
}

func NewDelegateTaskTool(models []config.ModelConfig, workspace string) *DelegateTaskTool {
	return &DelegateTaskTool{
		models:    models,
		workspace: workspace,
	}
}

func (t *DelegateTaskTool) Name() string {
	return "delegate_task"
}

func (t *DelegateTaskTool) Description() string {
	return "Delegate a specific complex task to a specialized model. Use this for 'vision' (analyzing images), 'audio' (transcribing), or 'coding' (complex programming tasks)."
}

func (t *DelegateTaskTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"capability": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"vision", "audio", "coding"},
				"description": "The specific capability required for the task.",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "Detailed instructions or question for the specialized model.",
			},
			"file_path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the target file (required for vision/audio, optional for coding if you want to modify an existing file).",
			},
		},
		"required": []string{"capability", "prompt"},
	}
}

func (t *DelegateTaskTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	capability, ok := args["capability"].(string)
	if !ok || capability == "" {
		return ErrorResult("The parameter 'capability' is mandatory.")
	}

	prompt, ok := args["prompt"].(string)
	if !ok || prompt == "" {
		return ErrorResult("The parameter 'prompt' is mandatory.")
	}

	filePath, _ := args["file_path"].(string) // optional

	// automatic search for suitable model
	var targetModel *config.ModelConfig
	for _, m := range t.models {
		for _, cap := range m.Capabilities {
			if cap == capability {
				mCopy := m
				targetModel = &mCopy
				break
			}
		}
		if targetModel != nil {
			break
		}
	}

	if targetModel == nil {
		return ErrorResult(fmt.Sprintf("No model configured to handle capability: %s", capability))
	}

	// setup workspace
	if targetModel.Workspace == "" {
		targetModel.Workspace = t.workspace
	}

	// provider creation via factory
	provider, _, err := providers.CreateProviderFromConfig(targetModel)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Impossibile creare il provider per il modello %q: %v", targetModel.Model, err))
	}

	// Capability-based execution
	switch capability {

	case "vision":
		if filePath == "" {
			return ErrorResult("For the capability 'vision' it is mandatory to provide 'file_path'.")
		}
		if visionProv, ok := provider.(providers.VisionProvider); ok {
			result, err := visionProv.AnalyzeImage(ctx, targetModel.Model, filePath, prompt)
			if err != nil {
				return ErrorResult(fmt.Sprintf("API Vision Error: %v", err))
			}
			return NewToolResult(result)
		}

	case "audio":
		if filePath == "" {
			return ErrorResult("For the capability 'audio' it is mandatory to provide 'file_path'.")
		}
		if audioProv, ok := provider.(providers.AudioProvider); ok {
			result, err := audioProv.TranscribeAudio(ctx, targetModel.Model, filePath)
			if err != nil {
				return ErrorResult(fmt.Sprintf("Audio API Error: %v", err))
			}
			return NewToolResult(result)
		}

	case "coding":
		if codingProv, ok := provider.(providers.CodingProvider); ok {
			var existingCode string
			if filePath != "" {
				fileData, err := os.ReadFile(filePath)
				if err != nil {
					existingCode = fmt.Sprintf("Error: unable to read file %s: %v]\n", filePath, err)
				} else {
					existingCode = string(fileData)
				}
			}

			result, err := codingProv.GenerateCode(ctx, targetModel.Model, prompt, existingCode)
			if err != nil {
				return ErrorResult(fmt.Sprintf("error in coding api: %v", err))
			}
			return NewToolResult(result)
		}

	default:
		return ErrorResult(fmt.Sprintf("Capability '%s' not supported by internal switch.", capability))
	}

	return ErrorResult(fmt.Sprintf("The '%s' provider is configured for '%s' but does not implement the correct Go interface.", targetModel.Model, capability))
}
