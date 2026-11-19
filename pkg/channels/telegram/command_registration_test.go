package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/commands"
)

func TestTelegramBotCommands_FiltersAndNormalizes(t *testing.T) {
	defs := []commands.Definition{
		{Name: "Help", Description: "Help"},
		{Name: "team/review", Description: "Nested command"},
		{Name: "bad-name", Description: "Hyphenated command"},
		{Name: "dot.name", Description: "Dotted command"},
		{Name: strings.Repeat("a", 33), Description: "Too long"},
		{Name: "review", Description: ""},
		{Name: "long", Description: strings.Repeat("x", 300)},
		{Name: "HELP", Description: "Duplicate"},
	}

	got := telegramBotCommands(defs)
	if len(got) != 3 {
		t.Fatalf("len(commands) = %d, want 3: %+v", len(got), got)
	}
	if got[0].Command != "help" || got[0].Description != "Help" {
		t.Fatalf("commands[0] = %+v, want help/Help", got[0])
	}
	if got[1].Command != "review" || got[1].Description != "Custom command" {
		t.Fatalf("commands[1] = %+v, want review/Custom command", got[1])
	}
	if got[2].Command != "long" {
		t.Fatalf("commands[2] = %+v, want long", got[2])
	}
	if len([]rune(got[2].Description)) != telegramMaxCommandDescriptionRunes {
		t.Fatalf(
			"description length = %d, want %d",
			len([]rune(got[2].Description)),
			telegramMaxCommandDescriptionRunes,
		)
	}
	if !strings.HasSuffix(got[2].Description, "...") {
		t.Fatalf("truncated description = %q, want ... suffix", got[2].Description)
	}
}

func TestTelegramBotCommands_CapsAtTelegramLimit(t *testing.T) {
	defs := make([]commands.Definition, telegramMaxBotCommands+5)
	for i := range defs {
		defs[i] = commands.Definition{
			Name:        fmt.Sprintf("cmd_%d", i),
			Description: "Command",
		}
	}

	if got := telegramBotCommands(defs); len(got) != telegramMaxBotCommands {
		t.Fatalf("len(commands) = %d, want %d", len(got), telegramMaxBotCommands)
	}
}

func TestTelegramCommandDefinitions_UsesProvider(t *testing.T) {
	ch := &TelegramChannel{
		commandDefs: func() []commands.Definition {
			return []commands.Definition{{Name: "review", Description: "Review code"}}
		},
	}

	got := ch.commandDefinitions()
	if len(got) != 1 || got[0].Name != "review" {
		t.Fatalf("commandDefinitions() = %+v, want provider command", got)
	}
}

func TestTelegramCommandDefinitions_FallsBackToBuiltins(t *testing.T) {
	ch := &TelegramChannel{}
	if got := ch.commandDefinitions(); len(got) != len(commands.BuiltinDefinitions()) {
		t.Fatalf("commandDefinitions() = %d, want %d", len(got), len(commands.BuiltinDefinitions()))
	}
}

func TestStartCommandRegistration_DoesNotBlock(t *testing.T) {
	ch := &TelegramChannel{}
	started := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch.registerFunc = func(context.Context, []commands.Definition) error {
		started <- struct{}{}
		return errors.New("temporary failure")
	}

	ch.startCommandRegistration(ctx, []commands.Definition{{Name: "help"}})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("registration did not start asynchronously")
	}
}

func TestStartCommandRegistration_RetriesUntilSuccessThenStops(t *testing.T) {
	ch := &TelegramChannel{
		commandRegDelayFn: func(int) time.Duration { return 5 * time.Millisecond },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int32
	ch.registerFunc = func(context.Context, []commands.Definition) error {
		n := attempts.Add(1)
		if n < 3 {
			return errors.New("temporary failure")
		}
		return nil
	}

	ch.startCommandRegistration(ctx, []commands.Definition{{Name: "help", Description: "Help"}})

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if attempts.Load() >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if attempts.Load() < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts.Load())
	}

	stable := attempts.Load()
	time.Sleep(30 * time.Millisecond)
	if attempts.Load() != stable {
		t.Fatalf("expected retries to stop after success, got %d -> %d", stable, attempts.Load())
	}
}

func TestStartCommandRegistration_StopsAfterCancel(t *testing.T) {
	ch := &TelegramChannel{
		commandRegDelayFn: func(int) time.Duration { return 5 * time.Millisecond },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts atomic.Int32
	ch.registerFunc = func(context.Context, []commands.Definition) error {
		attempts.Add(1)
		return errors.New("always fail")
	}

	ch.startCommandRegistration(ctx, []commands.Definition{{Name: "help", Description: "Help"}})

	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond) // allow in-flight attempt to settle
	stable := attempts.Load()
	time.Sleep(30 * time.Millisecond)
	if attempts.Load() != stable {
		t.Fatalf("expected retries to quiesce after cancel, got %d -> %d", stable, attempts.Load())
	}
}
