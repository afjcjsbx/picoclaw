package discord

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/sipeed/picoclaw/pkg/commands"
)

func TestDiscordBotCommands_FiltersAndNormalizes(t *testing.T) {
	defs := []commands.Definition{
		{Name: "Help", Description: "Help"},
		{Name: "team/review", Description: "Nested command"},
		{Name: "bad.name", Description: "Dotted command"},
		{Name: "bad-name", Description: "Hyphenated command"},
		{Name: strings.Repeat("a", 33), Description: "Too long"},
		{Name: "review", Description: ""},
		{Name: "long", Description: strings.Repeat("x", 120)},
		{Name: "HELP", Description: "Duplicate"},
	}

	got := discordBotCommands(defs)
	if len(got) != 4 {
		t.Fatalf("len(commands) = %d, want 4: %+v", len(got), got)
	}
	if got[0].Name != "help" || got[0].Description != "Help" {
		t.Fatalf("commands[0] = %+v, want help/Help", got[0])
	}
	if got[1].Name != "bad-name" {
		t.Fatalf("commands[1] = %+v, want bad-name", got[1])
	}
	if got[2].Name != "review" || got[2].Description != "Custom command" {
		t.Fatalf("commands[2] = %+v, want review/Custom command", got[2])
	}
	if got[3].Name != "long" {
		t.Fatalf("commands[3] = %+v, want long", got[3])
	}
	if len([]rune(got[3].Description)) != discordMaxCommandDescriptionRunes {
		t.Fatalf(
			"description length = %d, want %d",
			len([]rune(got[3].Description)),
			discordMaxCommandDescriptionRunes,
		)
	}
	if !strings.HasSuffix(got[3].Description, "...") {
		t.Fatalf("truncated description = %q, want ... suffix", got[3].Description)
	}
	for _, command := range got {
		if len(command.Options) != 1 || command.Options[0].Name != discordCommandArgumentsOption {
			t.Fatalf("command %q options = %+v, want one arguments option", command.Name, command.Options)
		}
	}
}

func TestDiscordBotCommands_CapsAtDiscordLimit(t *testing.T) {
	defs := make([]commands.Definition, discordMaxCommands+5)
	for i := range defs {
		defs[i] = commands.Definition{
			Name:        fmt.Sprintf("cmd-%d", i),
			Description: "Command",
		}
	}

	if got := discordBotCommands(defs); len(got) != discordMaxCommands {
		t.Fatalf("len(commands) = %d, want %d", len(got), discordMaxCommands)
	}
}

func TestDiscordCommandDefinitions_UsesProvider(t *testing.T) {
	ch := &DiscordChannel{
		commandDefs: func() []commands.Definition {
			return []commands.Definition{{Name: "review", Description: "Review code"}}
		},
	}

	got := ch.commandDefinitions()
	if len(got) != 1 || got[0].Name != "review" {
		t.Fatalf("commandDefinitions() = %+v, want provider command", got)
	}
}

func TestDiscordCommandDefinitions_FallsBackToBuiltins(t *testing.T) {
	ch := &DiscordChannel{}
	if got := ch.commandDefinitions(); len(got) != len(commands.BuiltinDefinitions()) {
		t.Fatalf("commandDefinitions() = %d, want %d", len(got), len(commands.BuiltinDefinitions()))
	}
}

func TestDiscordInteractionContent(t *testing.T) {
	data := discordgo.ApplicationCommandInteractionData{
		Name: "review",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{{
			Name:  discordCommandArgumentsOption,
			Type:  discordgo.ApplicationCommandOptionString,
			Value: `src/auth.ts "error handling"`,
		}},
	}

	if got := discordInteractionContent(data); got != `/review src/auth.ts "error handling"` {
		t.Fatalf("discordInteractionContent() = %q, want full command text", got)
	}

	if got := discordInteractionContent(discordgo.ApplicationCommandInteractionData{Name: "help"}); got != "/help" {
		t.Fatalf("discordInteractionContent() = %q, want /help", got)
	}
}
