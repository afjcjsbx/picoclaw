package commands

import (
	"context"
	"strings"
	"testing"
)

func executeHelp(t *testing.T, text string) string {
	t.Helper()
	defs := BuiltinDefinitions()
	ex := NewExecutor(NewRegistry(defs), nil)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: text,
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("%s: outcome=%v, want=%v", text, res.Outcome, OutcomeHandled)
	}
	if res.Command != "help" {
		t.Fatalf("%s: command=%q, want=%q", text, res.Command, "help")
	}
	return reply
}

func TestHelpCommand_OverviewListsAllCommands(t *testing.T) {
	reply := executeHelp(t, "/help")

	for _, want := range []string{
		"/help - Show this help message",
		"/list [model|models|channels|agents|skills|mcp|plugins] - List available options",
		"/show [model|channel|agents|mcp <server>] - Show current configuration",
		"Use /help <command> to see detailed help for a single command.",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/help reply missing %q, got:\n%s", want, reply)
		}
	}
}

func TestHelpCommand_FocusedHelpIncludesDescriptionUsageAndSubcommands(t *testing.T) {
	reply := executeHelp(t, "/help list")

	for _, want := range []string{
		"/list - List available options",
		"Usage: /list [model|models|channels|agents|skills|mcp|plugins]",
		"Subcommands:",
		"/list model - Current model and provider",
		"/list models - Configured models (numbered, use with /switch)",
		"/list channels - Enabled channels",
		"/list agents - Registered agents",
		"/list skills - Installed skills",
		"/list mcp - Configured MCP servers",
		"/list plugins - Installed plugins",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/help list reply missing %q, got:\n%s", want, reply)
		}
	}
}

func TestHelpCommand_FocusedHelpIncludesSubcommandArguments(t *testing.T) {
	reply := executeHelp(t, "/help show")

	if !strings.Contains(reply, "Usage: /show [model|channel|agents|mcp <server>]") {
		t.Fatalf("/help show reply missing usage, got:\n%s", reply)
	}
	if !strings.Contains(reply, "/show mcp <server> - Active tools for an MCP server") {
		t.Fatalf("/help show reply missing subcommand arguments, got:\n%s", reply)
	}
}

func TestHelpCommand_FocusedHelpForSimpleCommand(t *testing.T) {
	reply := executeHelp(t, "/help stop")

	for _, want := range []string{
		"/stop - Stop the current task",
		"Usage: /stop",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/help stop reply missing %q, got:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "Subcommands:") {
		t.Fatalf("/help stop reply should not list subcommands, got:\n%s", reply)
	}
}

func TestHelpCommand_AcceptsAliasesPrefixesAndCase(t *testing.T) {
	tests := []string{
		"/help LIST",
		"/help /list",
		"!help list",
		"/help list@my_bot",
	}
	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			reply := executeHelp(t, text)
			if !strings.Contains(reply, "/list - List available options") {
				t.Fatalf("%s reply missing focused /list help, got:\n%s", text, reply)
			}
		})
	}

	reply := executeHelp(t, "/help new")
	if !strings.Contains(reply, "/clear - Clear the chat history") {
		t.Fatalf("/help new should resolve the /new alias, got:\n%s", reply)
	}
	if !strings.Contains(reply, "Aliases: /new") {
		t.Fatalf("/help new should list aliases, got:\n%s", reply)
	}
}

func TestHelpCommand_UnknownCommandReturnsClearResponse(t *testing.T) {
	reply := executeHelp(t, "/help does-not-exist")

	if !strings.Contains(reply, "Unknown command: /does-not-exist") {
		t.Fatalf("/help unknown reply missing command name, got:\n%s", reply)
	}
	if !strings.Contains(reply, "Use /help to see all commands.") {
		t.Fatalf("/help unknown reply missing guidance, got:\n%s", reply)
	}
}

func TestHelpCommand_UsesRuntimeDefinitions(t *testing.T) {
	rt := &Runtime{
		ListDefinitions: func() []Definition {
			return []Definition{
				{
					Name:        "custom",
					Description: "Custom command",
					SubCommands: []SubCommand{
						{Name: "run", Description: "Run the custom action"},
					},
				},
			}
		},
	}
	ex := NewExecutor(NewRegistry(BuiltinDefinitions()), rt)

	var reply string
	res := ex.Execute(context.Background(), Request{
		Text: "/help custom",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v, want=%v", res.Outcome, OutcomeHandled)
	}
	for _, want := range []string{
		"/custom - Custom command",
		"Usage: /custom [run]",
		"/custom run - Run the custom action",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/help custom reply missing %q, got:\n%s", want, reply)
		}
	}
}
