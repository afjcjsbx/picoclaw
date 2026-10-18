package config

import "strings"

// NormalizeMCPTransportType canonicalizes MCP transport names used in config.
// "http" is PicoClaw's streamable HTTP request-response mode, and
// "streamable-http" is accepted as an explicit alias for the same transport.
func NormalizeMCPTransportType(transport string) string {
	normalized := strings.ToLower(strings.TrimSpace(transport))

	switch normalized {
	case "streamable-http", "streamable_http", "streamablehttp":
		return "http"
	default:
		return normalized
	}
}

// EffectiveMCPTransportType returns the normalized configured transport, or the
// inferred default when the config leaves Type empty.
func EffectiveMCPTransportType(server MCPServerConfig) string {
	if transport := NormalizeMCPTransportType(server.Type); transport != "" {
		return transport
	}
	if server.URL != "" {
		return "sse"
	}
	if server.Command != "" {
		return "stdio"
	}
	return ""
}

// EffectiveDeferred reports whether this server's tools are registered as
// hidden (deferred/discovery mode). When Deferred is nil, the global discovery
// setting applies; when discovery is disabled the server can never be
// deferred.
func (c MCPServerConfig) EffectiveDeferred(discoveryEnabled bool) bool {
	if !discoveryEnabled {
		return false
	}
	if c.Deferred != nil {
		return *c.Deferred
	}
	return true
}
