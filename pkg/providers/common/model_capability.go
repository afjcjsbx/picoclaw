// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package common

import "strings"

// temperatureDeprecatedModelMarkers lists model-name fragments whose API
// rejects an explicit temperature parameter with:
//
//	temperature is deprecated for this model.
//
// Anthropic enables extended thinking by default on these models and does not
// accept temperature alongside thinking. Bedrock uses the same markers because
// its model IDs and inference-profile ARNs embed the Anthropic model name.
var temperatureDeprecatedModelMarkers = []string{
	// Claude 5.5 models.
	"claude-haiku-5-5",
	"claude-sonnet-5-5",
	"claude-opus-5-5",
	// Claude 5 models and newer 5.x minor versions.
	"claude-haiku-5",
	"claude-sonnet-5",
	"claude-opus-5",
	"claude-opus-4-5",
	"claude-sonnet-4-5",
	"claude-opus-4-8",
}

// ModelDeprecatesTemperature reports whether the model rejects an explicit
// temperature parameter. Matching is case-insensitive and substring-based so
// it covers provider-prefixed IDs (for example "anthropic/claude-sonnet-5-5")
// and Bedrock inference profiles.
func ModelDeprecatesTemperature(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	model = strings.ReplaceAll(model, ".", "-")

	for _, marker := range temperatureDeprecatedModelMarkers {
		if strings.Contains(model, marker) {
			return true
		}
	}
	return false
}
