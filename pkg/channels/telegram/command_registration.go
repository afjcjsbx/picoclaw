package telegram

import (
	"context"
	"math/rand"
	"slices"
	"strings"
	"time"

	"github.com/mymmrac/telego"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	telegramMaxBotCommands             = 100
	telegramMaxCommandNameLength       = 32
	telegramMaxCommandDescriptionRunes = 256
)

var commandRegistrationBackoff = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
	5 * time.Minute,
	10 * time.Minute,
}

func commandRegistrationDelay(attempt int) time.Duration {
	if len(commandRegistrationBackoff) == 0 {
		return 0
	}
	base := commandRegistrationBackoff[min(attempt, len(commandRegistrationBackoff)-1)]
	// Full jitter in [0.5, 1.0) to avoid synchronized retries across instances.
	return time.Duration(float64(base) * (0.5 + rand.Float64()*0.5))
}

// RegisterCommands registers bot commands on Telegram platform.
func (c *TelegramChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	botCommands := telegramBotCommands(defs)

	current, err := c.bot.GetMyCommands(ctx, &telego.GetMyCommandsParams{})
	if err != nil {
		// If we can't read current commands, fall through to set them.
		logger.WarnCF("telegram", "Failed to get current commands, will set unconditionally",
			map[string]any{"error": err.Error()})
	} else if slices.Equal(current, botCommands) {
		logger.DebugCF("telegram", "Bot commands are up to date", nil)
		return nil
	}

	return c.bot.SetMyCommands(ctx, &telego.SetMyCommandsParams{
		Commands: botCommands,
	})
}

func telegramBotCommands(defs []commands.Definition) []telego.BotCommand {
	botCommands := make([]telego.BotCommand, 0, len(defs))
	seen := make(map[string]struct{}, len(defs))

	for _, def := range defs {
		name, ok := telegramCommandName(def.Name)
		if !ok {
			if strings.TrimSpace(def.Name) != "" {
				logger.DebugCF("telegram", "Skipping command that Telegram cannot represent", map[string]any{
					"command": def.Name,
				})
			}
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		description := strings.TrimSpace(def.Description)
		if description == "" {
			description = "Custom command"
		}
		botCommands = append(botCommands, telego.BotCommand{
			Command:     name,
			Description: truncateTelegramCommandDescription(description),
		})
		if len(botCommands) == telegramMaxBotCommands {
			logger.WarnCF("telegram", "Telegram command menu reached its limit", map[string]any{
				"limit": telegramMaxBotCommands,
			})
			break
		}
	}
	return botCommands
}

func telegramCommandName(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > telegramMaxCommandNameLength {
		return "", false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return "", false
	}
	return name, true
}

func truncateTelegramCommandDescription(description string) string {
	runes := []rune(description)
	if len(runes) <= telegramMaxCommandDescriptionRunes {
		return description
	}
	return string(runes[:telegramMaxCommandDescriptionRunes-3]) + "..."
}

func (c *TelegramChannel) startCommandRegistration(ctx context.Context, defs []commands.Definition) {
	if len(defs) == 0 {
		return
	}

	register := c.registerFunc
	if register == nil {
		register = c.RegisterCommands
	}
	delayFn := c.commandRegDelayFn
	if delayFn == nil {
		delayFn = commandRegistrationDelay
	}

	regCtx, cancel := context.WithCancel(ctx)
	c.commandRegCancel = cancel

	// Registration runs asynchronously so Telegram message intake is never blocked
	// by temporary upstream API failures. Retry stops on success or channel shutdown.
	go func() {
		attempt := 0
		timer := time.NewTimer(0)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		defer timer.Stop()
		for {
			err := register(regCtx, defs)
			if err == nil {
				logger.InfoCF("telegram", "Telegram commands registered", map[string]any{
					"count": len(defs),
				})
				return
			}

			delay := delayFn(attempt)
			logger.WarnCF("telegram", "Telegram command registration failed; will retry", map[string]any{
				"error":       err.Error(),
				"retry_after": delay.String(),
			})
			attempt++

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(delay)

			select {
			case <-regCtx.Done():
				return
			case <-timer.C:
			}
		}
	}()
}
