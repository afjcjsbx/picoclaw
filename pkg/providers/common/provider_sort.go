package common

import (
	"fmt"
	"strings"
)

const (
	ProviderSortPrice      = "price"
	ProviderSortThroughput = "throughput"
	ProviderSortLatency    = "latency"
)

// NormalizeProviderSort validates and canonicalizes an OpenRouter provider
// routing sort value. An empty value means "leave the provider default".
func NormalizeProviderSort(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case ProviderSortPrice:
		return ProviderSortPrice, nil
	case ProviderSortThroughput:
		return ProviderSortThroughput, nil
	case ProviderSortLatency:
		return ProviderSortLatency, nil
	default:
		return "", fmt.Errorf(
			"provider_sort must be one of: %s, %s, %s",
			ProviderSortPrice,
			ProviderSortThroughput,
			ProviderSortLatency,
		)
	}
}
