package integrationtools

import toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"

// Network-only tools: stateless per call and with no filesystem footprint, so
// they can run concurrently with any other safe call.

func (t *WebSearchTool) ParallelSafety(map[string]any) toolshared.ParallelSafety {
	return toolshared.ParallelSafety{Safe: true}
}

func (t *WebFetchTool) ParallelSafety(map[string]any) toolshared.ParallelSafety {
	return toolshared.ParallelSafety{Safe: true}
}

func (t *FindSkillsTool) ParallelSafety(map[string]any) toolshared.ParallelSafety {
	return toolshared.ParallelSafety{Safe: true}
}

func (t *ImageGenerateTool) ParallelSafety(map[string]any) toolshared.ParallelSafety {
	return toolshared.ParallelSafety{Safe: true}
}
