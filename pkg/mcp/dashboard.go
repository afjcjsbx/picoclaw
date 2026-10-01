package mcp

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

const RedactedValue = "********"

var (
	sensitiveNamePattern = regexp.MustCompile(`(?i)(key|token|secret|auth|pass|pwd|credential|dsn|database[_-]?url|db[_-]?(?:url|uri)|connection[_-]?(?:string|uri)|postgres(?:ql)?[_-]?url|mongo(?:db)?[_-]?uri|redis[_-]?url)`)
	sensitivePairPattern = regexp.MustCompile(`(?i)((?:api[_-]?key|token|secret|auth(?:orization)?|pass(?:word)?|pwd|credential|dsn|database[_-]?url|db[_-]?(?:url|uri)|connection[_-]?(?:string|uri)|postgres(?:ql)?[_-]?url|mongo(?:db)?[_-]?uri|redis[_-]?url)\s*[=:]\s*)([^\s,;&]+)`)
	sensitiveFlagPattern = regexp.MustCompile(`(?i)(--[^\s=]*(?:key|token|secret|auth|pass|pwd|credential|dsn|database[_-]?url|db[_-]?(?:url|uri)|connection[_-]?(?:string|uri)|postgres(?:ql)?[_-]?url|mongo(?:db)?[_-]?uri|redis[_-]?url)[^\s=]*(?:=|\s+))([^\s]+)`)
	knownTokenPattern    = regexp.MustCompile(`(?i)\b(?:sk-[a-z0-9_-]{4,}|gh[pousr]_[a-z0-9_]{8,}|bearer\s+[a-z0-9._~+/=-]{8,})\b`)
	databaseURLPattern   = regexp.MustCompile(`(?i)(?:jdbc:)?(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis|rediss|mssql|sqlserver)://[^\s]+`)
)

type DashboardResponse struct {
	Enabled          bool              `json:"enabled"`
	RuntimeAvailable bool              `json:"runtime_available"`
	Servers          []DashboardServer `json:"servers"`
}

type DashboardServer struct {
	Name              string          `json:"name"`
	Enabled           bool            `json:"enabled"`
	ConfiguredEnabled bool            `json:"configured_enabled"`
	Deferred          bool            `json:"deferred"`
	Transport         string          `json:"transport"`
	Status            ConnectionState `json:"status"`
	Command           string          `json:"command,omitempty"`
	LaunchCommand     string          `json:"launch_command,omitempty"`
	Args              []string        `json:"args,omitempty"`
	Error             string          `json:"error,omitempty"`
	ToolCount         int             `json:"tool_count"`
	Tools             []DashboardTool `json:"tools"`
}

type DashboardTool struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Parameters  []DashboardParameter `json:"parameters"`
}

type DashboardParameter struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// BuildDashboardResponse converts raw runtime state into a browser-safe API model.
func BuildDashboardResponse(globalEnabled, discoveryEnabled, runtimeAvailable bool, statuses map[string]ServerStatus) DashboardResponse {
	response := DashboardResponse{
		Enabled:          globalEnabled,
		RuntimeAvailable: runtimeAvailable,
		Servers:          make([]DashboardServer, 0, len(statuses)),
	}
	for _, status := range statuses {
		cfg := status.Config
		state := status.State
		if !globalEnabled || !cfg.Enabled {
			state = ConnectionDisabled
		}
		redactor := newServerRedactor(cfg)
		server := DashboardServer{
			Name:              redactor.Redact(status.Name),
			Enabled:           globalEnabled && cfg.Enabled,
			ConfiguredEnabled: cfg.Enabled,
			Deferred:          effectiveDeferred(discoveryEnabled, cfg),
			Transport:         config.EffectiveMCPTransportType(cfg),
			Status:            state,
			Command:           redactor.Command(cfg),
			LaunchCommand:     redactor.Redact(sanitizeURL(cfg.Command)),
			Args:              sanitizeArgs(cfg.Args, redactor.knownValues),
			Error:             redactor.Redact(status.Error),
			ToolCount:         len(status.Tools),
			Tools:             make([]DashboardTool, 0, len(status.Tools)),
		}
		for _, tool := range status.Tools {
			if tool == nil || strings.TrimSpace(tool.Name) == "" {
				continue
			}
			server.Tools = append(server.Tools, DashboardTool{
				Name:        redactor.Redact(strings.TrimSpace(tool.Name)),
				Description: redactor.Redact(strings.TrimSpace(tool.Description)),
				Parameters:  summarizeDashboardParameters(tool.InputSchema, redactor),
			})
		}
		sort.Slice(server.Tools, func(i, j int) bool { return server.Tools[i].Name < server.Tools[j].Name })
		server.ToolCount = len(server.Tools)
		response.Servers = append(response.Servers, server)
	}
	sort.Slice(response.Servers, func(i, j int) bool {
		return strings.ToLower(response.Servers[i].Name) < strings.ToLower(response.Servers[j].Name)
	})
	SanitizeDashboardResponse(&response)
	return response
}

// SanitizeDashboardResponse is a second, idempotent safety boundary for API proxies.
func SanitizeDashboardResponse(response *DashboardResponse) {
	sanitizeDashboardResponse(response, nil)
}

// SanitizeDashboardResponseWithServers applies the proxy-side safety boundary
// with access to configured values, so even an unexpected raw upstream payload
// cannot echo a configured credential into the browser response.
func SanitizeDashboardResponseWithServers(response *DashboardResponse, servers map[string]config.MCPServerConfig) {
	knownValues := make([]string, 0)
	for _, cfg := range servers {
		knownValues = append(knownValues, newServerRedactor(cfg).knownValues...)
	}
	sanitizeDashboardResponse(response, knownValues)
}

func sanitizeDashboardResponse(response *DashboardResponse, knownValues []string) {
	if response == nil {
		return
	}
	for i := range response.Servers {
		server := &response.Servers[i]
		server.Name = RedactText(server.Name, knownValues)
		server.Transport = RedactText(server.Transport, knownValues)
		server.Command = RedactText(server.Command, knownValues)
		server.LaunchCommand = RedactText(server.LaunchCommand, knownValues)
		server.Args = sanitizeArgs(server.Args, knownValues)
		server.Error = RedactText(server.Error, knownValues)
		for j := range server.Tools {
			tool := &server.Tools[j]
			tool.Name = RedactText(tool.Name, knownValues)
			tool.Description = RedactText(tool.Description, knownValues)
			for k := range tool.Parameters {
				param := &tool.Parameters[k]
				param.Name = RedactText(param.Name, knownValues)
				param.Type = RedactText(param.Type, knownValues)
				param.Description = RedactText(param.Description, knownValues)
			}
		}
	}
}

// RedactServerConfig returns a display-safe copy suitable for WebUI config APIs.
func RedactServerConfig(cfg config.MCPServerConfig) config.MCPServerConfig {
	redactor := newServerRedactor(cfg)
	result := cfg
	result.Command = redactor.Redact(sanitizeURL(cfg.Command))
	result.URL = sanitizeURL(cfg.URL)
	result.Args = sanitizeArgs(cfg.Args, redactor.knownValues)
	result.Env = redactMap(cfg.Env, false)
	result.Headers = redactMap(cfg.Headers, true)
	return result
}

// RestoreServerConfigRedactions preserves stored values when a WebUI submits
// unchanged redaction placeholders.
func RestoreServerConfigRedactions(incoming, existing config.MCPServerConfig) config.MCPServerConfig {
	redactedExisting := RedactServerConfig(existing)
	if incoming.Command == redactedExisting.Command && incoming.Command != existing.Command {
		incoming.Command = existing.Command
	}
	if incoming.URL == redactedExisting.URL && incoming.URL != existing.URL {
		incoming.URL = existing.URL
	}
	for i := range incoming.Args {
		if i < len(existing.Args) && i < len(redactedExisting.Args) && incoming.Args[i] == redactedExisting.Args[i] && incoming.Args[i] != existing.Args[i] {
			incoming.Args[i] = existing.Args[i]
		}
	}
	incoming.Env = restoreMapRedactions(incoming.Env, existing.Env)
	incoming.Headers = restoreMapRedactions(incoming.Headers, existing.Headers)
	return incoming
}

func effectiveDeferred(global bool, cfg config.MCPServerConfig) bool {
	if cfg.Deferred != nil {
		return *cfg.Deferred
	}
	return global
}

type serverRedactor struct {
	knownValues []string
}

func newServerRedactor(cfg config.MCPServerConfig) serverRedactor {
	values := make([]string, 0, len(cfg.Env)+len(cfg.Headers))
	for _, value := range cfg.Env {
		values = appendKnownSecret(values, value)
	}
	for _, value := range cfg.Headers {
		values = appendKnownSecret(values, value)
	}
	for i, arg := range cfg.Args {
		if key, value, ok := strings.Cut(arg, "="); ok && sensitiveNamePattern.MatchString(key) {
			values = appendKnownSecret(values, value)
		}
		if i > 0 && isSensitiveStandaloneFlag(cfg.Args[i-1]) {
			values = appendKnownSecret(values, arg)
		}
	}
	if parsed, err := url.Parse(cfg.URL); err == nil {
		if parsed.User != nil {
			if password, ok := parsed.User.Password(); ok {
				values = appendKnownSecret(values, password)
			}
		}
		for key, entries := range parsed.Query() {
			if !sensitiveNamePattern.MatchString(key) {
				continue
			}
			for _, value := range entries {
				values = appendKnownSecret(values, value)
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return serverRedactor{knownValues: values}
}

func isSensitiveStandaloneFlag(value string) bool {
	return strings.HasPrefix(value, "-") && !strings.Contains(value, "=") && sensitiveNamePattern.MatchString(strings.TrimLeft(value, "-"))
}

func (r serverRedactor) Redact(value string) string {
	return RedactText(value, r.knownValues)
}

func (r serverRedactor) Command(cfg config.MCPServerConfig) string {
	transport := config.EffectiveMCPTransportType(cfg)
	if transport == "http" || transport == "sse" {
		return strings.TrimSpace(transport + " " + sanitizeURL(cfg.URL))
	}
	parts := make([]string, 0, len(cfg.Args)+1)
	if command := strings.TrimSpace(cfg.Command); command != "" {
		parts = append(parts, shellDisplayArg(r.Redact(command)))
	}
	for _, arg := range sanitizeArgs(cfg.Args, r.knownValues) {
		parts = append(parts, shellDisplayArg(arg))
	}
	return strings.Join(parts, " ")
}

func RedactText(value string, knownValues []string) string {
	if value == "" {
		return ""
	}
	for _, secret := range knownValues {
		if len(secret) >= 4 {
			value = strings.ReplaceAll(value, secret, RedactedValue)
		}
	}
	value = sensitivePairPattern.ReplaceAllString(value, `${1}`+RedactedValue)
	value = sensitiveFlagPattern.ReplaceAllString(value, `${1}`+RedactedValue)
	value = knownTokenPattern.ReplaceAllString(value, RedactedValue)
	value = databaseURLPattern.ReplaceAllString(value, RedactedValue)
	return value
}

func sanitizeArgs(args, knownValues []string) []string {
	result := make([]string, len(args))
	maskNext := false
	for i, arg := range args {
		if maskNext {
			result[i] = RedactedValue
			maskNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") && sensitiveNamePattern.MatchString(strings.TrimLeft(arg, "-")) {
			if key, _, ok := strings.Cut(arg, "="); ok {
				result[i] = key + "=" + RedactedValue
			} else {
				result[i] = arg
				maskNext = true
			}
			continue
		}
		if key, _, ok := strings.Cut(arg, "="); ok && sensitiveNamePattern.MatchString(key) {
			result[i] = key + "=" + RedactedValue
			continue
		}
		result[i] = RedactText(sanitizeURL(arg), knownValues)
	}
	return result
}

func sanitizeURL(value string) string {
	if databaseURLPattern.MatchString(strings.TrimSpace(value)) {
		return RedactedValue
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return value
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if username == "" {
			parsed.User = nil
		} else {
			parsed.User = url.UserPassword(username, RedactedValue)
		}
	}
	query := parsed.Query()
	for key := range query {
		if sensitiveNamePattern.MatchString(key) {
			query.Set(key, RedactedValue)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func redactMap(input map[string]string, maskAll bool) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		if maskAll || sensitiveNamePattern.MatchString(key) {
			result[key] = RedactedValue
		} else {
			result[key] = RedactText(sanitizeURL(value), nil)
		}
	}
	return result
}

func restoreMapRedactions(incoming, existing map[string]string) map[string]string {
	if incoming == nil {
		return nil
	}
	result := cloneStringMap(incoming)
	for key, value := range result {
		if value == RedactedValue {
			if original, ok := existing[key]; ok {
				result[key] = original
			}
		}
	}
	return result
}

func appendKnownSecret(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if len(value) >= 4 && value != RedactedValue {
		return append(values, value)
	}
	return values
}

func shellDisplayArg(value string) string {
	if value == "" {
		return `""`
	}
	if strings.IndexFunc(value, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' }) >= 0 {
		return strconv.Quote(value)
	}
	return value
}

func summarizeDashboardParameters(schema any, redactor serverRedactor) []DashboardParameter {
	schemaMap := normalizeDashboardSchema(schema)
	properties, ok := schemaMap["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return []DashboardParameter{}
	}
	required := make(map[string]struct{})
	switch values := schemaMap["required"].(type) {
	case []string:
		for _, name := range values {
			required[name] = struct{}{}
		}
	case []any:
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]DashboardParameter, 0, len(names))
	for _, name := range names {
		parameter := DashboardParameter{Name: redactor.Redact(name)}
		_, parameter.Required = required[name]
		if property, ok := properties[name].(map[string]any); ok {
			parameter.Type = redactor.Redact(schemaType(property))
			if description, ok := property["description"].(string); ok {
				parameter.Description = redactor.Redact(strings.TrimSpace(description))
			}
		}
		result = append(result, parameter)
	}
	return result
}

func normalizeDashboardSchema(schema any) map[string]any {
	if schema == nil {
		return map[string]any{}
	}
	if schemaMap, ok := schema.(map[string]any); ok {
		return schemaMap
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{}
	}
	return result
}

func schemaType(property map[string]any) string {
	switch value := property["type"].(type) {
	case string:
		if value == "array" {
			if items, ok := property["items"].(map[string]any); ok {
				if itemType, ok := items["type"].(string); ok && itemType != "" {
					return "array<" + itemType + ">"
				}
			}
		}
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, entry := range value {
			if text, ok := entry.(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " | ")
	}
	if _, ok := property["enum"]; ok {
		return "enum"
	}
	if _, ok := property["oneOf"]; ok {
		return "union"
	}
	if _, ok := property["anyOf"]; ok {
		return "union"
	}
	return ""
}
