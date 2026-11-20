package telegram

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mymmrac/telego"

	"github.com/sipeed/picoclaw/pkg/commands"
)

const (
	telegramCommandCallbackPrefix = "pc:"
	telegramCommandButtonsPerRow  = 3
	telegramMaxCallbackDataBytes  = 64

	telegramSkillCallbackPrefix = "pu:"
	telegramSkillPageAction     = "p:"
	telegramSkillSelectAction   = "s:"
	telegramSkillButtonsPerPage = 12
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
	if markup := telegramUseCommandMenuMarkup(text); markup != nil {
		return markup
	}

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

	return &telego.InlineKeyboardMarkup{
		InlineKeyboard: chunkInlineButtons(buttons, telegramCommandButtonsPerRow),
	}
}

// telegramUseCommandMenuMarkup builds the paginated skill picker for the /use
// help reply. The help text already contains the installed skill names, so the
// menu can be generated without reaching into the agent runtime.
func telegramUseCommandMenuMarkup(text string) *telego.InlineKeyboardMarkup {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "Usage: /use <skill>") {
		return nil
	}

	skills := telegramSkillNames(text)
	if len(skills) == 0 {
		return nil
	}
	return telegramSkillMenuMarkup(skills, 0)
}

// telegramSkillNames extracts skill names from the "Installed Skills:" block
// emitted by buildUseCommandHelp.
func telegramSkillNames(text string) []string {
	const marker = "Installed Skills:"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return nil
	}

	rest := text[idx+len(marker):]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		rest = rest[:end]
	}

	names := make([]string, 0)
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// telegramSkillMenuMarkup renders one page of the skill picker. Skill
// selection uses the global skill index, so callback data stays short even for
// long skill names.
func telegramSkillMenuMarkup(skills []string, page int) *telego.InlineKeyboardMarkup {
	if len(skills) == 0 {
		return nil
	}

	totalPages := (len(skills) + telegramSkillButtonsPerPage - 1) / telegramSkillButtonsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * telegramSkillButtonsPerPage
	end := min(start+telegramSkillButtonsPerPage, len(skills))

	buttons := make([]telego.InlineKeyboardButton, 0, end-start)
	for i := start; i < end; i++ {
		buttons = append(buttons, telego.InlineKeyboardButton{
			Text:         skills[i],
			CallbackData: fmt.Sprintf("%s%s%d", telegramSkillCallbackPrefix, telegramSkillSelectAction, i),
		})
	}

	rows := chunkInlineButtons(buttons, telegramCommandButtonsPerRow)
	if totalPages > 1 {
		nav := make([]telego.InlineKeyboardButton, 0, 3)
		if page > 0 {
			nav = append(nav, telego.InlineKeyboardButton{
				Text:         "◀ Prev",
				CallbackData: fmt.Sprintf("%s%s%d", telegramSkillCallbackPrefix, telegramSkillPageAction, page-1),
			})
		}
		nav = append(nav, telego.InlineKeyboardButton{
			Text:         fmt.Sprintf("%d/%d", page+1, totalPages),
			CallbackData: fmt.Sprintf("%s%s%d", telegramSkillCallbackPrefix, telegramSkillPageAction, page),
		})
		if page < totalPages-1 {
			nav = append(nav, telego.InlineKeyboardButton{
				Text:         "Next ▶",
				CallbackData: fmt.Sprintf("%s%s%d", telegramSkillCallbackPrefix, telegramSkillPageAction, page+1),
			})
		}
		rows = append(rows, nav)
	}

	return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func chunkInlineButtons(
	buttons []telego.InlineKeyboardButton,
	perRow int,
) [][]telego.InlineKeyboardButton {
	if perRow <= 0 {
		perRow = 1
	}
	rows := make([][]telego.InlineKeyboardButton, 0, (len(buttons)+perRow-1)/perRow)
	for len(buttons) > 0 {
		size := min(perRow, len(buttons))
		rows = append(rows, buttons[:size])
		buttons = buttons[size:]
	}
	return rows
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
// definitions. Only commands with sub-commands can be invoked from a command
// menu button, so forged callback data cannot reach unrelated commands. The
// /use skill picker is the exception: it runs /use <skill> for a skill listed
// in the menu.
func telegramCallbackCommandAllowed(commandText string, defs []commands.Definition) bool {
	cmdName, ok := commands.CommandName(commandText)
	if !ok {
		return false
	}

	if cmdName == "use" {
		parts := strings.Fields(strings.TrimSpace(commandText))
		return len(parts) == 2 && !strings.EqualFold(parts[1], "clear")
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

type telegramSkillCallbackKind string

const (
	telegramSkillCallbackPage   telegramSkillCallbackKind = "page"
	telegramSkillCallbackSelect telegramSkillCallbackKind = "select"
)

// telegramSkillCallback parses pagination and selection callbacks emitted by
// telegramSkillMenuMarkup.
func telegramSkillCallback(data string) (telegramSkillCallbackKind, int, bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, telegramSkillCallbackPrefix) {
		return "", 0, false
	}

	rest := strings.TrimPrefix(data, telegramSkillCallbackPrefix)
	var action string
	var kind telegramSkillCallbackKind
	switch {
	case strings.HasPrefix(rest, telegramSkillPageAction):
		action = strings.TrimPrefix(rest, telegramSkillPageAction)
		kind = telegramSkillCallbackPage
	case strings.HasPrefix(rest, telegramSkillSelectAction):
		action = strings.TrimPrefix(rest, telegramSkillSelectAction)
		kind = telegramSkillCallbackSelect
	default:
		return "", 0, false
	}

	value, err := strconv.Atoi(action)
	if err != nil || value < 0 {
		return "", 0, false
	}
	return kind, value, true
}
