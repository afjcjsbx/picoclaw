package config

import (
	"encoding/json"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/require"
)

func TestHardwareConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	require.True(t, cfg.Tools.Hardware.GetRequireApproval())
	require.Equal(t, 300, cfg.Tools.Hardware.GetApprovalTimeoutSeconds())
	require.Equal(t, 30, cfg.Tools.Hardware.GetMaxWritesPerMinute())
	require.Equal(t, 60, cfg.Tools.Hardware.GetSerialSessionTTLSeconds())
	require.Equal(t, 2, cfg.Tools.Hardware.GetSerialMaxSessions())

	require.False(t, cfg.Tools.I2C.AllowWrite)
	require.False(t, cfg.Tools.SPI.AllowWrite)
	require.False(t, cfg.Tools.Serial.AllowWrite)
}

func TestHardwareConfigZeroValueIsFailClosed(t *testing.T) {
	var cfg ToolsConfig

	require.True(t, cfg.Hardware.GetRequireApproval())
	require.Equal(t, 300, cfg.Hardware.GetApprovalTimeoutSeconds())
	require.Equal(t, 30, cfg.Hardware.GetMaxWritesPerMinute())
	require.False(t, cfg.I2C.AllowWrite)
	require.False(t, cfg.SPI.AllowWrite)
	require.False(t, cfg.Serial.AllowWrite)
}

func TestHardwareConfigExplicitValues(t *testing.T) {
	cfg := HardwareConfig{
		RequireApproval:         boolPtr(false),
		MaxWritesPerMinute:      -1,
		SerialSessionTTLSeconds: 5,
		SerialMaxSessions:       1,
	}

	require.False(t, cfg.GetRequireApproval())
	require.Equal(t, -1, cfg.GetMaxWritesPerMinute())
	require.Equal(t, 5, cfg.GetSerialSessionTTLSeconds())
	require.Equal(t, 1, cfg.GetSerialMaxSessions())
}

func TestHardwareConfigJSONAndEnv(t *testing.T) {
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{
		"tools": {
			"i2c": {"enabled": true, "allow_write": true, "allow": ["1:0x76"]},
			"spi": {"enabled": true, "allow_write": false, "allow": ["2.0"]},
			"hardware": {"require_approval": false, "max_writes_per_minute": 5}
		}
	}`), &cfg))

	require.True(t, cfg.Tools.I2C.Enabled)
	require.True(t, cfg.Tools.I2C.AllowWrite)
	require.Equal(t, []string{"1:0x76"}, cfg.Tools.I2C.Allow)
	require.False(t, cfg.Tools.Hardware.GetRequireApproval())
	require.Equal(t, 5, cfg.Tools.Hardware.GetMaxWritesPerMinute())

	t.Setenv("PICOCLAW_TOOLS_HARDWARE_REQUIRE_APPROVAL", "true")
	t.Setenv("PICOCLAW_TOOLS_HARDWARE_MAX_WRITES_PER_MINUTE", "7")
	t.Setenv("PICOCLAW_TOOLS_I2C_ALLOW_WRITE", "false")
	t.Setenv("PICOCLAW_TOOLS_I2C_ALLOW", "1:0x38,1:0x76")
	require.NoError(t, env.Parse(&cfg))

	require.True(t, cfg.Tools.Hardware.GetRequireApproval())
	require.Equal(t, 7, cfg.Tools.Hardware.GetMaxWritesPerMinute())
	require.False(t, cfg.Tools.I2C.AllowWrite)
	require.Equal(t, []string{"1:0x38", "1:0x76"}, cfg.Tools.I2C.Allow)
}
