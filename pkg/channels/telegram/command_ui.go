package telegram

import (
	"regexp"
	"strings"

	"github.com/mymmrac/telego"

	"github.com/sipeed/picoclaw/pkg/commands"
)

const (
	telegramCommandCallbackPrefix = "pc:"
	telegramCommandButtonsPerRow  = 3
	telegramMaxCallbackDataBytes  = 64
)

var (
	// telegramUsageLineRe matches the usage reply produced by the command
	// executor for a command invoked without a sub-command, e.g.
	// "Usage: /list [model|models|...]". It also matches the focused help
	// output ("/help list"), which contains the same usage line.
	telegramUsageLineRe = regexp.MustCompile(`(?m)^Usage: /([a-z0-9_]+) \[`)

	// telegramUnknownOptionUsageRe matches the unknown-sub-command reply, e.g.
	// "Unknown option: nope. Usage: /list [model|models|...]".
	telegramUnknownOptionUsageRe = regexp.MustCompile(`^Unknown option: [^\n]*?\. Usage: /([a-z0-9_]+) \[`)
)

// telegramCommandMenuMarkup builds an inline keyboard for a command usage
// reply such as "Usage: /list [model|models|...]". Buttons are generated from
// the command definitions so the menu stays in sync with /help and with the
// executor. It returns nil when the text is not a command usage reply or when
// no sub-command can be invoked without required arguments.
func telegramCommandMenuMarkup(text string, defs []commands.Definition) *telego.InlineKeyboardMarkup {
	name, ok := telegramUsageCommandName(text)
	if !ok {
		return nil
	}

	def, found := commands.NewRegistry(defs).Lookup(name)
	if !found || len(def.SubCommands) == 0 {
		return nil
	}

	buttons := make([]telego.InlineKeyboardButton, 0, len(def.SubCommands))
	for _, sub := range def.SubCommands {
		subName := strings.TrimSpace(sub.Name)
		if subName == "" || !telegramSubCommandTappable(sub) {
			continue
		}

		callbackData := telegramCommandCallbackPrefix + "/" + def.Name + " " + subName
		if len(callbackData) > telegramMaxCallbackDataBytes {
			continue
		}

		buttons = append(buttons, telego.InlineKeyboardButton{
			Text:         subName,
			CallbackData: callbackData,
		})
	}
	if len(buttons) == 0 {
		return nil
	}

	rowCount := (len(buttons) + telegramCommandButtonsPerRow - 1) / telegramCommandButtonsPerRow
	rows := make([][]telego.InlineKeyboardButton, 0, rowCount)
	for len(buttons) > 0 {
		size := min(telegramCommandButtonsPerRow, len(buttons))
		rows = append(rows, buttons[:size])
		buttons = buttons[size:]
	}

	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func telegramUsageCommandName(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2048 {
		return "", false
	}

	if match := telegramUsageLineRe.FindStringSubmatch(text); len(match) == 2 {
		return match[1], true
	}
	if match := telegramUnknownOptionUsageRe.FindStringSubmatch(text); len(match) == 2 {
		return match[1], true
	}
	return "", false
}

// telegramSubCommandTappable reports whether the bare sub-command is a valid
// command. Sub-commands with required arguments (ArgsUsage such as "<server>")
// would only produce another usage reply, so they are left out of the keyboard.
func telegramSubCommandTappable(sub commands.SubCommand) bool {
	args := strings.TrimSpace(sub.ArgsUsage)
	if args == "" {
		return true
	}
	return strings.HasPrefix(args, "[")
}

// telegramCallbackCommand extracts the command text from callback data created
// by telegramCommandMenuMarkup.
func telegramCallbackCommand(data string) (string, bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, telegramCommandCallbackPrefix) {
		return "", false
	}

	commandText := strings.TrimSpace(strings.TrimPrefix(data, telegramCommandCallbackPrefix))
	if commandText == "" || !strings.HasPrefix(commandText, "/") {
		return "", false
	}
	if strings.ContainsAny(commandText, "\r\n") {
		return "", false
	}
	return commandText, true
}

// telegramCallbackCommandAllowed validates callback data against the command
// definitions. Only commands with sub-commands can be invoked from a menu
// button, so forged callback data cannot reach unrelated commands.
func telegramCallbackCommandAllowed(commandText string, defs []commands.Definition) bool {
	cmdName, ok := commands.CommandName(commandText)
	if !ok {
		return false
	}

	def, found := commands.NewRegistry(defs).Lookup(cmdName)
	if !found || len(def.SubCommands) == 0 {
		return false
	}

	parts := strings.Fields(strings.TrimSpace(commandText))
	if len(parts) < 2 {
		return false
	}

	subName := strings.ToLower(strings.TrimSpace(parts[1]))
	for _, sub := range def.SubCommands {
		if strings.EqualFold(sub.Name, subName) {
			return true
		}
	}
	return false
}
