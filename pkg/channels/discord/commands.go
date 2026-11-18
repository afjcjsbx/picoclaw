package discord

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/identity"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	discordMaxCommands                 = 100
	discordMaxCommandNameLength        = 32
	discordMaxCommandDescriptionRunes  = 100
	discordCommandArgumentsOption      = "arguments"
	discordCommandArgumentsDescription = "Arguments for this command"
	discordCommandFallbackDescription  = "Custom command"
)

func (c *DiscordChannel) commandDefinitions() []commands.Definition {
	if c.commandDefs != nil {
		if defs := c.commandDefs(); len(defs) > 0 {
			return defs
		}
	}
	return commands.BuiltinDefinitions()
}

// RegisterCommands replaces Discord's global slash command menu with the
// built-in and custom definitions.
func (c *DiscordChannel) RegisterCommands(ctx context.Context, defs []commands.Definition) error {
	appID := c.applicationID()
	if appID == "" {
		return fmt.Errorf("discord application ID is unavailable")
	}
	if c.session == nil {
		return fmt.Errorf("discord session is unavailable")
	}

	botCommands := discordBotCommands(defs)
	if len(botCommands) == 0 {
		return nil
	}
	_, err := c.session.ApplicationCommandBulkOverwrite(
		appID,
		"",
		botCommands,
		discordgo.WithContext(ctx),
	)
	return err
}

func (c *DiscordChannel) registerCommands(defs []commands.Definition) {
	if err := c.RegisterCommands(c.ctx, defs); err != nil {
		logger.WarnCF("discord", "Failed to register slash commands", map[string]any{
			"error": err.Error(),
		})
		return
	}
	logger.InfoCF("discord", "Discord slash commands registered", map[string]any{
		"count": len(defs),
	})
}

func (c *DiscordChannel) applicationID() string {
	if c.botUserID != "" {
		return c.botUserID
	}
	if c.session != nil && c.session.State != nil && c.session.State.User != nil {
		return c.session.State.User.ID
	}
	return ""
}

func discordBotCommands(defs []commands.Definition) []*discordgo.ApplicationCommand {
	botCommands := make([]*discordgo.ApplicationCommand, 0, len(defs))
	seen := make(map[string]struct{}, len(defs))

	for _, def := range defs {
		name, ok := discordCommandName(def.Name)
		if !ok {
			if strings.TrimSpace(def.Name) != "" {
				logger.DebugCF("discord", "Skipping command that Discord cannot represent", map[string]any{
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
			description = discordCommandFallbackDescription
		}
		botCommands = append(botCommands, &discordgo.ApplicationCommand{
			Name:        name,
			Description: truncateDiscordCommandDescription(description),
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        discordCommandArgumentsOption,
				Description: discordCommandArgumentsDescription,
				Required:    false,
			}},
		})
		if len(botCommands) == discordMaxCommands {
			logger.WarnCF("discord", "Discord slash command menu reached its limit", map[string]any{
				"limit": discordMaxCommands,
			})
			break
		}
	}
	return botCommands
}

func discordCommandName(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || len(name) > discordMaxCommandNameLength {
		return "", false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return "", false
	}
	return name, true
}

func truncateDiscordCommandDescription(description string) string {
	runes := []rune(description)
	if len(runes) <= discordMaxCommandDescriptionRunes {
		return description
	}
	return string(runes[:discordMaxCommandDescriptionRunes-3]) + "..."
}

func discordInteractionContent(data discordgo.ApplicationCommandInteractionData) string {
	content := "/" + data.Name
	if option := data.GetOption(discordCommandArgumentsOption); option != nil &&
		option.Type == discordgo.ApplicationCommandOptionString &&
		option.Value != nil {
		if arguments := strings.TrimSpace(option.StringValue()); arguments != "" {
			content += " " + arguments
		}
	}
	return content
}

func (c *DiscordChannel) handleInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i == nil || i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if strings.TrimSpace(data.Name) == "" {
		return
	}

	user := interactionUser(i)
	if user == nil {
		return
	}

	sender := bus.SenderInfo{
		Platform:    "discord",
		PlatformID:  user.ID,
		CanonicalID: identity.BuildCanonicalID("discord", user.ID),
		Username:    user.Username,
		DisplayName: discordDisplayName(user),
	}
	if !c.IsAllowedSender(sender) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You are not allowed to use this bot.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		logger.WarnCF("discord", "Failed to acknowledge slash command interaction", map[string]any{
			"error": err.Error(),
		})
		return
	}

	content := discordInteractionContent(data)
	peerKind := "channel"
	isDM := "false"
	if i.GuildID == "" {
		peerKind = "direct"
		isDM = "true"
	}
	metadata := map[string]string{
		"user_id":      user.ID,
		"username":     user.Username,
		"display_name": sender.DisplayName,
		"guild_id":     i.GuildID,
		"channel_id":   i.ChannelID,
		"is_dm":        isDM,
		"is_command":   "true",
	}
	inboundCtx := bus.InboundContext{
		Channel:   c.Name(),
		ChatID:    i.ChannelID,
		ChatType:  peerKind,
		SenderID:  user.ID,
		MessageID: i.ID,
		Mentioned: true,
		Raw:       metadata,
	}
	if i.GuildID != "" {
		inboundCtx.SpaceID = i.GuildID
		inboundCtx.SpaceType = "guild"
	}
	c.HandleInboundContext(c.ctx, i.ChannelID, content, nil, inboundCtx, sender)
}

func interactionUser(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

func discordDisplayName(user *discordgo.User) string {
	if user.Discriminator != "" && user.Discriminator != "0" {
		return user.Username + "#" + user.Discriminator
	}
	return user.Username
}
