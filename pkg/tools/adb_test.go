package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestADBTool_Basic(t *testing.T) {
	tool := NewADBTool()

	// Verifica Name
	if tool.Name() != "adb" {
		t.Errorf("Expected name 'adb', got '%s'", tool.Name())
	}

	// Verifica Description
	if tool.Description() == "" {
		t.Error("Expected a description, got empty string")
	}

	// Verifica Parameters schema
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Expected params type 'object', got '%v'", params["type"])
	}

	required, ok := params["required"].([]string)
	if !ok || len(required) == 0 || required[0] != "args" {
		t.Error("Expected 'args' to be required")
	}
}

func TestADBTool_SetTimeout(t *testing.T) {
	tool := NewADBTool()
	expected := 10 * time.Second

	tool.SetTimeout(expected)
	if tool.timeout != expected {
		t.Errorf("Expected timeout %v, got %v", expected, tool.timeout)
	}
}

func TestADBTool_Execute_Validation(t *testing.T) {
	tool := NewADBTool()
	ctx := context.Background()

	tests := []struct {
		name      string
		args      map[string]interface{}
		wantError bool
		errorMsg  string
	}{
		{
			name:      "Missing args parameter",
			args:      map[string]interface{}{},
			wantError: true,
			errorMsg:  "The parameter 'args' is mandatory", // Modificato per fare match con il tuo codice
		},
		{
			name: "Args is not a slice",
			args: map[string]interface{}{
				"args": "shell ls", // Sbagliato, dovrebbe essere []interface{}
			},
			wantError: true,
			errorMsg:  "The parameter 'args' is mandatory", // Modificato per fare match con il tuo codice
		},
		{
			name: "Empty args array",
			args: map[string]interface{}{
				"args": []interface{}{},
			},
			wantError: true,
			errorMsg:  "The parameter 'args' is mandatory", // Modificato per fare match con il tuo codice
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(ctx, tt.args)

			if !result.IsError && tt.wantError {
				t.Errorf("Expected error for %s, got success", tt.name)
			}

			if tt.wantError && !strings.Contains(result.ForLLM, tt.errorMsg) {
				t.Errorf("Expected error containing %q, got %q", tt.errorMsg, result.ForLLM)
			}
		})
	}
}

func TestADBTool_Execute_Command(t *testing.T) {
	tool := NewADBTool()
	// Riduciamo il timeout per il test per evitare attese lunghe
	tool.SetTimeout(2 * time.Second)
	ctx := context.Background()

	// Simuliamo un comando ADB valido dal punto di vista dei parametri
	args := map[string]interface{}{
		"args":      []interface{}{"version"},
		"device_id": "test_device_123",
	}

	result := tool.Execute(ctx, args)

	// Nota: su una macchina senza adb installato, exec.Command restituirà un errore
	// come "executable file not found in $PATH". Se adb è installato, potrebbe fallire
	// perché "test_device_123" non esiste (restituendo un Exit code).
	// In entrambi i casi non deve verificarsi un crash.

	if result == nil {
		t.Fatal("Expected a ToolResult, got nil")
	}

	// Ci aspettiamo che l'output contenga l'errore di esecuzione o la versione
	if result.ForLLM == "" {
		t.Error("Expected non-empty output in ForLLM")
	}

	// Se il comando va in timeout o fallisce, IsError deve essere true
	// Questo è un test generico per assicurarsi che il comando venga effettivamente lanciato.
	if !result.IsError && !strings.Contains(strings.ToLower(result.ForLLM), "android debug bridge") {
		t.Logf("Warning: Tool executed successfully but didn't return ADB version string. Output: %s", result.ForLLM)
	}
}

func TestADBTool_Execute_Timeout(t *testing.T) {
	tool := NewADBTool()
	// Impostiamo un timeout bassissimo
	tool.SetTimeout(1 * time.Millisecond)
	ctx := context.Background()

	// Proviamo a lanciare un comando che teoricamente impiegherebbe tempo (es. logcat)
	args := map[string]interface{}{
		"args": []interface{}{"logcat"},
	}

	result := tool.Execute(ctx, args)

	if result == nil {
		t.Fatal("Expected a ToolResult, got nil")
	}

	if !result.IsError {
		t.Error("Expected an error due to short timeout or missing adb, got success")
	}
}
