package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfigIncludesA2AChannelDefaults(t *testing.T) {
	bc := DefaultConfig().Channels[ChannelA2A]
	require.NotNil(t, bc)
	assert.False(t, bc.Enabled)
	assert.Equal(t, ChannelA2A, bc.Type)

	decoded, err := bc.GetDecoded()
	require.NoError(t, err)
	settings, ok := decoded.(*A2ASettings)
	require.True(t, ok, "decoded as %T", decoded)
	assert.Equal(t, "127.0.0.1", settings.Host)
	assert.Equal(t, 9900, settings.Port)
	assert.Equal(t, "PicoClaw", settings.Name)
	assert.Equal(t, 1<<20, settings.MaxBodyBytes)
	assert.Equal(t, 60, settings.RateLimitPerMinute)
	assert.Equal(t, 256, settings.MaxTasks)
	assert.Equal(t, 4, settings.MaxConcurrent)
	assert.Equal(t, 300, settings.TaskTimeoutSeconds)
	assert.False(t, settings.PushAllowPrivate)
	assert.False(t, settings.Streaming.Enabled)
}

// The token of a remote agent lives in security.yml; everything else in config.json.
// Loading must merge them, and saving must keep the token out of config.json.
func TestA2AAgentsTokenFromSecurityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgJSON := fmt.Sprintf(`{
  "version": %d,
  "a2a_agents": {
    "researcher": {"url": "http://10.0.0.5:9900", "auth": {"type": "bearer"}, "timeout": 30, "capabilities": ["research"]}
  },
  "channel_list": {
    "a2a": {"enabled": true, "type": "a2a", "settings": {"port": 9911, "max_concurrent": 2}}
  }
}`, CurrentVersion)
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))
	sec := "a2a_agents:\n  researcher:\n    auth:\n      token: s3cret\nchannel_list:\n  a2a:\n    settings:\n      tokens:\n        - tok-a\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, SecurityConfigFile), []byte(sec), 0o600))

	check := func(cfg *Config) {
		t.Helper()
		a := cfg.A2AAgents["researcher"]
		require.NotNil(t, a)
		assert.Equal(t, "http://10.0.0.5:9900", a.URL)
		assert.Equal(t, 30, a.Timeout)
		assert.Equal(t, "bearer", a.Auth.Type)
		assert.Equal(t, "s3cret", a.Auth.Token.String())

		ch := cfg.Channels["a2a"]
		require.NotNil(t, ch)
		decoded, err := ch.GetDecoded()
		require.NoError(t, err)
		s, ok := decoded.(*A2ASettings)
		require.True(t, ok, "decoded as %T", decoded)
		assert.Equal(t, 9911, s.Port)
		assert.Equal(t, 2, s.MaxConcurrent)
		assert.Equal(t, []string{"tok-a"}, s.Tokens.Values())
	}

	cfg, err := LoadConfig(cfgPath)
	require.NoError(t, err)
	check(cfg)

	require.NoError(t, SaveConfig(cfgPath, cfg))
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "s3cret")
	assert.NotContains(t, string(raw), "tok-a")

	cfg, err = LoadConfig(cfgPath)
	require.NoError(t, err)
	check(cfg)
}
