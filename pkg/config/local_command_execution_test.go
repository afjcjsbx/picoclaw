package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalCommandExecutionAllowed(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{name: "unset", set: false, want: false},
		{name: "empty", value: "", set: true, want: false},
		{name: "true", value: "true", set: true, want: true},
		{name: "one", value: "1", set: true, want: true},
		{name: "uppercase", value: "TRUE", set: true, want: true},
		{name: "whitespace", value: " true ", set: true, want: true},
		{name: "false", value: "false", set: true, want: false},
		{name: "zero", value: "0", set: true, want: false},
		{name: "invalid", value: "yes", set: true, want: false},
		{name: "partially valid", value: "true-ish", set: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(LocalCommandExecutionEnv, tt.value)
			if !tt.set {
				require.NoError(t, os.Unsetenv(LocalCommandExecutionEnv))
			}

			err := RequireLocalCommandExecution()
			if tt.want {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrLocalCommandExecutionDisabled)
			}
			require.Equal(t, tt.want, LocalCommandExecutionAllowed())
		})
	}
}
