// Demo Agent Plugin. Build this program into bin/demo-plugin before enabling it.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GreetInput struct {
	Name string `json:"name" jsonschema:"Name of the person to greet"`
}
type GreetOutput struct {
	Greeting string `json:"greeting"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "demo-plugin", Version: "1.0.0"}, nil)
	mcp.AddTool(
		server,
		&mcp.Tool{Name: "greet", Description: "Greet a person by name."},
		func(ctx context.Context, request *mcp.CallToolRequest, input GreetInput) (*mcp.CallToolResult, GreetOutput, error) {
			if strings.TrimSpace(input.Name) == "" {
				return nil, GreetOutput{}, fmt.Errorf("name must not be empty")
			}
			greeting := "Hello, " + strings.TrimSpace(input.Name) + "!"
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: greeting}},
			}, GreetOutput{
				Greeting: greeting,
			}, nil
		},
	)
	// stdout is reserved for MCP; diagnostics go to stderr.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
