package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type PythonExecutorTool struct {
	registry *ToolRegistry
}

func NewPythonExecutorTool(registry *ToolRegistry) *PythonExecutorTool {
	return &PythonExecutorTool{
		registry: registry,
	}
}

func (t *PythonExecutorTool) Name() string {
	return "python_run"
}

func (t *PythonExecutorTool) Description() string {
	return `Execute Python code in a local ephemeral sandbox (using 'uv run') to process data or call tools programmatically. 
You can call other picoclaw tools using the 'picoclaw' module. Example:
res = picoclaw.call_tool('web_search', query='news')

If you need external packages, you MUST declare them using PEP-723 inline script metadata at the very top of your code. Example:
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "requests",
#   "pandas",
# ]
# ///
import requests
import picoclaw

You MUST use print() to output the final results you want the user to see.`
}

func (t *PythonExecutorTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type":        "string",
				"description": "The Python code to execute. Must print() the final output.",
			},
		},
		"required": []string{"code"},
	}
}

func (t *PythonExecutorTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	code, ok := args["code"].(string)
	if !ok {
		return ErrorResult("code argument is required")
	}

	// 1. Creiamo un server HTTP temporaneo su una porta casuale libera
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to start local bridge: %v", err))
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// 2. Handler che intercetta le chiamate Python e lancia i Tool nativi
	mux := http.NewServeMux()
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		res := t.registry.Execute(ctx, req.Tool, req.Args)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"result":   res.ForLLM,
			"is_error": res.IsError,
		})
	})

	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()

	// 3. Prepariamo la cartella temporanea per lo script
	tmpDir, err := os.MkdirTemp("", "picoclaw-python-*")
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create temp dir: %v", err))
	}
	defer os.RemoveAll(tmpDir)

	// 4. Scriviamo la libreria fittizia "picoclaw.py"
	sdkCode := `import os, json, urllib.request

def call_tool(name, *args, **kwargs):
    final_args = {}
    if len(args) > 0 and isinstance(args[0], dict):
        final_args.update(args[0])
    final_args.update(kwargs)
    
    port = os.environ.get("PICOCLAW_BRIDGE_PORT")
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/call",
        data=json.dumps({"tool": name, "args": final_args}).encode("utf-8"),
        headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req) as response:
        data = json.loads(response.read().decode("utf-8"))
        if data.get("is_error"):
            raise Exception(f"Tool {name} failed: {data.get('result')}")
        return data.get("result")
`
	os.WriteFile(filepath.Join(tmpDir, "picoclaw.py"), []byte(sdkCode), 0o644)
	os.WriteFile(filepath.Join(tmpDir, "main.py"), []byte(code), 0o644)

	// 5. Lanciamo Python usando "uv run" per l'isolamento!
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "uv", "run", "main.py")
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PICOCLAW_BRIDGE_PORT=%d", port))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	outStr := stdout.String()
	errStr := stderr.String()

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return ErrorResult(fmt.Sprintf("Execution timed out.\nStdout:\n%s\nStderr:\n%s", outStr, errStr))
		}
		return ErrorResult(fmt.Sprintf("Python error: exit status 1\nStdout:\n%s\nStderr:\n%s", outStr, errStr))
	}

	if outStr == "" && errStr == "" {
		outStr = "(Script executed successfully but printed nothing)"
	}

	res := outStr

	// Mostriamo il log di stderr solo se non è vuoto (spesso `uvx` stampa qui i log di installazione o warning)
	if errStr != "" {
		res += "\n\nLogs/Stderr:\n" + errStr
	}

	return SilentResult(res)
}
