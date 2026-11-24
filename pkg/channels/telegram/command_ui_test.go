package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestTelegramCommandMenuMarkup_List(t *testing.T) {
	markup := telegramCommandMenuMarkup(
		"Usage: /list [model|models|channels|agents|skills|mcp|plugins]",
		commands.BuiltinDefinitions(),
	)
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 3)

	var labels []string
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			labels = append(labels, button.Text)
			require.Equal(t, "pc:/list "+button.Text, button.CallbackData)
		}
	}
	require.Equal(t, []string{
		"model", "models", "channels",
		"agents", "skills", "mcp",
		"plugins",
	}, labels)
}

func TestTelegramCommandMenuMarkup_SkipsRequiredArguments(t *testing.T) {
	markup := telegramCommandMenuMarkup(
		"Usage: /show [model|channel|agents|mcp <server>]",
		commands.BuiltinDefinitions(),
	)
	require.NotNil(t, markup)

	var labels []string
	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			labels = append(labels, button.Text)
		}
	}
	require.Equal(t, []string{"model", "channel", "agents"}, labels)
}

func TestTelegramCommandMenuMarkup_UnknownOption(t *testing.T) {
	markup := telegramCommandMenuMarkup(
		"Unknown option: nope. Usage: /list [model|models|channels|agents|skills|mcp|plugins]",
		commands.BuiltinDefinitions(),
	)
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 3)
}

func TestTelegramCommandMenuMarkup_FocusedHelp(t *testing.T) {
	markup := telegramCommandMenuMarkup(
		"/list - List available options\nUsage: /list [model|models|channels|agents|skills|mcp|plugins]",
		commands.BuiltinDefinitions(),
	)
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 3)
}

func TestTelegramCommandMenuMarkup_NoMatch(t *testing.T) {
	defs := commands.BuiltinDefinitions()

	require.Nil(t, telegramCommandMenuMarkup("Hello, world!", defs))
	require.Nil(t, telegramCommandMenuMarkup("Usage: /check [channel <name>]", defs))
	require.Nil(t, telegramCommandMenuMarkup("Use /list models to see the models.", defs))
}

func TestTelegramCommandMenuMarkup_SkipsCallbackDataOverLimit(t *testing.T) {
	name := strings.Repeat("a", 60)
	defs := []commands.Definition{{
		Name: name,
		SubCommands: []commands.SubCommand{
			{Name: "x"},
		},
	}}

	require.Nil(t, telegramCommandMenuMarkup("Usage: /"+name+" [x]", defs))
}

func TestTelegramCommandMenuCallbackRoundTrip(t *testing.T) {
	defs := commands.BuiltinDefinitions()
	markup := telegramCommandMenuMarkup(
		"Usage: /list [model|models|channels|agents|skills|mcp|plugins]",
		defs,
	)
	require.NotNil(t, markup)

	for _, row := range markup.InlineKeyboard {
		for _, button := range row {
			commandText, ok := telegramCallbackCommand(button.CallbackData)
			require.True(t, ok, "callback data %q should parse", button.CallbackData)
			require.True(t, telegramCallbackCommandAllowed(commandText, defs),
				"callback data %q should be allowed", button.CallbackData)
		}
	}
}

func TestTelegramCallbackCommand(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
		ok   bool
	}{
		{name: "valid", data: "pc:/list models", want: "/list models", ok: true},
		{name: "valid bare", data: "pc:/list", want: "/list", ok: true},
		{name: "missing command", data: "pc:", ok: false},
		{name: "missing slash", data: "pc:list models", ok: false},
		{name: "other prefix", data: "other:/list models", ok: false},
		{name: "newline", data: "pc:/list models\n/clear", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := telegramCallbackCommand(tt.data)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTelegramCallbackCommandAllowed(t *testing.T) {
	defs := commands.BuiltinDefinitions()

	require.True(t, telegramCallbackCommandAllowed("/list models", defs))
	require.True(t, telegramCallbackCommandAllowed("/switch model", defs))
	require.False(t, telegramCallbackCommandAllowed("/list nope", defs))
	require.False(t, telegramCallbackCommandAllowed("/list", defs))
	require.False(t, telegramCallbackCommandAllowed("/unknown", defs))
	// Simple commands are not reachable from a command menu callback.
	require.False(t, telegramCallbackCommandAllowed("/clear", defs))
}

func TestHandleCallbackQueryPublishesCommand(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "answerCallbackQuery") {
				t.Fatalf("unexpected API call: %s", url)
			}
			return &ta.Response{Ok: true, Result: []byte("true")}, nil
		},
	}
	ch, messageBus := newCallbackTestChannel(t, caller, nil)

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:      "cb-1",
		From:    telego.User{ID: 777, FirstName: "Alice", Username: "alice"},
		Data:    "pc:/list models",
		Message: &telego.Message{MessageID: 42, Chat: telego.Chat{ID: 12345, Type: "private"}},
	})
	require.NoError(t, err)

	select {
	case inbound := <-messageBus.InboundChan():
		require.Equal(t, "/list models", inbound.Content)
		require.Equal(t, "telegram", inbound.Channel)
		require.Equal(t, "12345", inbound.ChatID)
		require.Equal(t, "direct", inbound.Context.ChatType)
		require.Equal(t, "777", inbound.SenderID)
		require.Equal(t, "telegram:777", inbound.Sender.CanonicalID)
		require.Equal(t, "42", inbound.Context.ReplyToMessageID)
		require.Equal(t, "callback", inbound.Context.Raw["via"])
		require.Equal(t, "42", inbound.Context.Raw["callback_message_id"])
	case <-time.After(time.Second):
		t.Fatal("no inbound message published")
	}

	requireCallbackAnswered(t, caller)
}

func TestHandleCallbackQuery_ForumTopic(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "answerCallbackQuery") {
				t.Fatalf("unexpected API call: %s", url)
			}
			return &ta.Response{Ok: true, Result: []byte("true")}, nil
		},
	}
	ch, messageBus := newCallbackTestChannel(t, caller, nil)

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:   "cb-2",
		From: telego.User{ID: 777, FirstName: "Alice"},
		Data: "pc:/list channels",
		Message: &telego.Message{
			MessageID:       42,
			MessageThreadID: 7,
			Chat:            telego.Chat{ID: 12345, Type: "supergroup", IsForum: true},
		},
	})
	require.NoError(t, err)

	select {
	case inbound := <-messageBus.InboundChan():
		require.Equal(t, "12345/7", inbound.ChatID)
		require.Equal(t, "7", inbound.Context.TopicID)
	case <-time.After(time.Second):
		t.Fatal("no inbound message published")
	}
}

func TestHandleCallbackQuery_RejectsUnauthorizedUser(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "answerCallbackQuery") {
				t.Fatalf("unexpected API call: %s", url)
			}
			return &ta.Response{Ok: true, Result: []byte("true")}, nil
		},
	}
	ch, messageBus := newCallbackTestChannel(t, caller, []string{"999"})

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:      "cb-3",
		From:    telego.User{ID: 777, FirstName: "Alice"},
		Data:    "pc:/list models",
		Message: &telego.Message{MessageID: 42, Chat: telego.Chat{ID: 12345, Type: "private"}},
	})
	require.NoError(t, err)

	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	case <-time.After(100 * time.Millisecond):
	}

	requireCallbackAnswered(t, caller)
}

func TestHandleCallbackQuery_RejectsInvalidData(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "answerCallbackQuery") {
				t.Fatalf("unexpected API call: %s", url)
			}
			return &ta.Response{Ok: true, Result: []byte("true")}, nil
		},
	}
	ch, messageBus := newCallbackTestChannel(t, caller, nil)

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:      "cb-4",
		From:    telego.User{ID: 777, FirstName: "Alice"},
		Data:    "pc:/clear",
		Message: &telego.Message{MessageID: 42, Chat: telego.Chat{ID: 12345, Type: "private"}},
	})
	require.NoError(t, err)

	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	case <-time.After(100 * time.Millisecond):
	}

	requireCallbackAnswered(t, caller)
}

func TestSend_CommandUsageAttachesInlineKeyboard(t *testing.T) {
	var body []byte
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "sendMessage") {
				t.Fatalf("unexpected API call: %s", url)
			}
			body = append([]byte(nil), data.BodyRaw...)
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	_, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Usage: /list [model|models|channels|agents|skills|mcp|plugins]",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	markup, ok := payload["reply_markup"].(map[string]any)
	require.True(t, ok)
	rows, ok := markup["inline_keyboard"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 3)

	firstRow, ok := rows[0].([]any)
	require.True(t, ok)
	firstButton, ok := firstRow[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "model", firstButton["text"])
	require.Equal(t, "pc:/list model", firstButton["callback_data"])
}

func TestEditMessage_CommandUsageAttachesInlineKeyboard(t *testing.T) {
	var body []byte
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "editMessageText") {
				t.Fatalf("unexpected API call: %s", url)
			}
			body = append([]byte(nil), data.BodyRaw...)
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	err := ch.EditMessage(
		context.Background(),
		"12345",
		"7",
		"Usage: /list [model|models|channels|agents|skills|mcp|plugins]",
	)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	markup, ok := payload["reply_markup"].(map[string]any)
	require.True(t, ok)
	rows, ok := markup["inline_keyboard"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 3)
}

func TestSend_NormalMessageHasNoInlineKeyboard(t *testing.T) {
	var body []byte
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, data *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "sendMessage") {
				t.Fatalf("unexpected API call: %s", url)
			}
			body = append([]byte(nil), data.BodyRaw...)
			return successResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	_, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "12345",
		Content: "Hello, world!",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	_, hasMarkup := payload["reply_markup"]
	require.False(t, hasMarkup)
}

func TestTelegramSkillNames(t *testing.T) {
	skills := []string{"weather", "github", "summarize"}
	require.Equal(t, skills, telegramSkillNames(useHelpText(skills...)))
	require.Nil(t, telegramSkillNames(useHelpText()))
	require.Nil(t, telegramSkillNames("hello"))
}

func TestTelegramUseCommandMenuMarkup(t *testing.T) {
	markup := telegramCommandMenuMarkup(
		useHelpText("weather", "github", "summarize"),
		commands.BuiltinDefinitions(),
	)
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 1)
	require.Len(t, markup.InlineKeyboard[0], 3)

	require.Equal(t, "weather", markup.InlineKeyboard[0][0].Text)
	require.Equal(t, "pu:s:0", markup.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "github", markup.InlineKeyboard[0][1].Text)
	require.Equal(t, "pu:s:1", markup.InlineKeyboard[0][1].CallbackData)
	require.Equal(t, "summarize", markup.InlineKeyboard[0][2].Text)
	require.Equal(t, "pu:s:2", markup.InlineKeyboard[0][2].CallbackData)
}

func TestTelegramSkillMenuMarkup_Pagination(t *testing.T) {
	skills := make([]string, 25)
	for i := range skills {
		skills[i] = fmt.Sprintf("skill-%02d", i)
	}

	first := telegramSkillMenuMarkup(skills, 0)
	require.NotNil(t, first)
	require.Len(t, first.InlineKeyboard, 5)
	require.Equal(t, "skill-00", first.InlineKeyboard[0][0].Text)
	require.Equal(t, "pu:s:0", first.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "skill-11", first.InlineKeyboard[3][2].Text)
	require.Equal(t, "pu:s:11", first.InlineKeyboard[3][2].CallbackData)

	firstNav := first.InlineKeyboard[4]
	require.Len(t, firstNav, 2)
	require.Equal(t, "1/3", firstNav[0].Text)
	require.Equal(t, "pu:p:0", firstNav[0].CallbackData)
	require.Equal(t, "Next ▶", firstNav[1].Text)
	require.Equal(t, "pu:p:1", firstNav[1].CallbackData)

	second := telegramSkillMenuMarkup(skills, 1)
	require.NotNil(t, second)
	require.Len(t, second.InlineKeyboard, 5)
	require.Equal(t, "skill-12", second.InlineKeyboard[0][0].Text)
	require.Equal(t, "pu:s:12", second.InlineKeyboard[0][0].CallbackData)

	secondNav := second.InlineKeyboard[4]
	require.Len(t, secondNav, 3)
	require.Equal(t, "◀ Prev", secondNav[0].Text)
	require.Equal(t, "pu:p:0", secondNav[0].CallbackData)
	require.Equal(t, "2/3", secondNav[1].Text)
	require.Equal(t, "pu:p:1", secondNav[1].CallbackData)
	require.Equal(t, "Next ▶", secondNav[2].Text)
	require.Equal(t, "pu:p:2", secondNav[2].CallbackData)

	last := telegramSkillMenuMarkup(skills, 2)
	require.NotNil(t, last)
	require.Len(t, last.InlineKeyboard, 2)
	require.Equal(t, "skill-24", last.InlineKeyboard[0][0].Text)
	require.Equal(t, "pu:s:24", last.InlineKeyboard[0][0].CallbackData)

	lastNav := last.InlineKeyboard[1]
	require.Len(t, lastNav, 2)
	require.Equal(t, "◀ Prev", lastNav[0].Text)
	require.Equal(t, "pu:p:1", lastNav[0].CallbackData)
	require.Equal(t, "3/3", lastNav[1].Text)
}

func TestTelegramSkillCallback(t *testing.T) {
	kind, value, ok := telegramSkillCallback("pu:p:2")
	require.True(t, ok)
	require.Equal(t, telegramPickerPage, kind)
	require.Equal(t, 2, value)

	kind, value, ok = telegramSkillCallback("pu:s:5")
	require.True(t, ok)
	require.Equal(t, telegramPickerSelect, kind)
	require.Equal(t, 5, value)

	for _, data := range []string{"pu:x:1", "pu:p:-1", "pu:s:", "pc:/list models", ""} {
		_, _, ok := telegramSkillCallback(data)
		require.False(t, ok, "callback data %q should be rejected", data)
	}
}

func TestTelegramCallbackCommandAllowed_UseSkill(t *testing.T) {
	defs := commands.BuiltinDefinitions()

	require.True(t, telegramCallbackCommandAllowed("/use weather", defs))
	require.False(t, telegramCallbackCommandAllowed("/use clear", defs))
	require.False(t, telegramCallbackCommandAllowed("/use", defs))
	require.False(t, telegramCallbackCommandAllowed("/use weather extra", defs))
}

func TestHandleCallbackQuery_SkillSelection(t *testing.T) {
	testHandlePickerSelection(t, "cb-skill", "pu:s:1", useHelpText("weather", "github"), "/use github")
}

func TestHandleCallbackQuery_SkillPageEditsMarkup(t *testing.T) {
	skills := make([]string, 25)
	for i := range skills {
		skills[i] = fmt.Sprintf("skill-%02d", i)
	}
	testHandlePickerPageEdit(
		t,
		"cb-skill-page",
		"pu:p:1",
		useHelpText(skills...),
		"skill-12",
		"pu:s:12",
		5,
	)
}

func TestHandleCallbackQuery_SkillMenuExpired(t *testing.T) {
	testHandlePickerExpired(t, "cb-skill-expired", "pu:s:0", "No installed skills found.")
}

func TestTelegramModelOptions(t *testing.T) {
	text := modelListText("gpt-4.1", "claude-sonnet-4")
	require.Equal(t, []telegramModelOption{
		{Index: 1, Name: "gpt-4.1"},
		{Index: 2, Name: "claude-sonnet-4"},
	}, telegramModelOptions(text))

	require.Nil(t, telegramModelOptions("No models configured"))
	require.Nil(t, telegramModelOptions("Configured Models:\nnot a model"))
}

func TestTelegramCommandMenuMarkup_ModelList(t *testing.T) {
	markup := telegramCommandMenuMarkup(
		modelListText("gpt-4.1", "claude-sonnet-4"),
		commands.BuiltinDefinitions(),
	)
	require.NotNil(t, markup)
	require.Len(t, markup.InlineKeyboard, 1)
	require.Len(t, markup.InlineKeyboard[0], 2)

	require.Equal(t, "1. gpt-4.1", markup.InlineKeyboard[0][0].Text)
	require.Equal(t, "pm:s:0", markup.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "2. claude-sonnet-4", markup.InlineKeyboard[0][1].Text)
	require.Equal(t, "pm:s:1", markup.InlineKeyboard[0][1].CallbackData)

	// /list models uses the same list with a different footer.
	listText := strings.Replace(
		modelListText("gpt-4.1", "claude-sonnet-4"),
		"Use /switch model",
		"Switch with /switch model",
		1,
	)
	require.NotNil(t, telegramCommandMenuMarkup(listText, commands.BuiltinDefinitions()))
}

func TestTelegramModelMenuMarkup_Pagination(t *testing.T) {
	models := make([]telegramModelOption, 23)
	for i := range models {
		models[i] = telegramModelOption{
			Index: i + 1,
			Name:  fmt.Sprintf("model-%02d", i),
		}
	}

	first := telegramModelMenuMarkup(models, 0)
	require.NotNil(t, first)
	require.Len(t, first.InlineKeyboard, 6)
	require.Equal(t, "1. model-00", first.InlineKeyboard[0][0].Text)
	require.Equal(t, "pm:s:0", first.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "10. model-09", first.InlineKeyboard[4][1].Text)
	require.Equal(t, "pm:s:9", first.InlineKeyboard[4][1].CallbackData)

	firstNav := first.InlineKeyboard[5]
	require.Len(t, firstNav, 2)
	require.Equal(t, "1/3", firstNav[0].Text)
	require.Equal(t, "pm:p:0", firstNav[0].CallbackData)
	require.Equal(t, "Next ▶", firstNav[1].Text)
	require.Equal(t, "pm:p:1", firstNav[1].CallbackData)

	second := telegramModelMenuMarkup(models, 1)
	require.NotNil(t, second)
	require.Len(t, second.InlineKeyboard, 6)
	require.Equal(t, "11. model-10", second.InlineKeyboard[0][0].Text)
	require.Equal(t, "pm:s:10", second.InlineKeyboard[0][0].CallbackData)

	secondNav := second.InlineKeyboard[5]
	require.Len(t, secondNav, 3)
	require.Equal(t, "◀ Prev", secondNav[0].Text)
	require.Equal(t, "pm:p:0", secondNav[0].CallbackData)
	require.Equal(t, "2/3", secondNav[1].Text)
	require.Equal(t, "Next ▶", secondNav[2].Text)
	require.Equal(t, "pm:p:2", secondNav[2].CallbackData)

	last := telegramModelMenuMarkup(models, 2)
	require.NotNil(t, last)
	require.Len(t, last.InlineKeyboard, 3)
	require.Equal(t, "21. model-20", last.InlineKeyboard[0][0].Text)
	require.Equal(t, "pm:s:20", last.InlineKeyboard[0][0].CallbackData)
	require.Equal(t, "23. model-22", last.InlineKeyboard[1][0].Text)
	require.Equal(t, "pm:s:22", last.InlineKeyboard[1][0].CallbackData)

	lastNav := last.InlineKeyboard[2]
	require.Len(t, lastNav, 2)
	require.Equal(t, "◀ Prev", lastNav[0].Text)
	require.Equal(t, "pm:p:1", lastNav[0].CallbackData)
	require.Equal(t, "3/3", lastNav[1].Text)
}

func TestTelegramModelCallback(t *testing.T) {
	kind, value, ok := telegramModelCallback("pm:p:2")
	require.True(t, ok)
	require.Equal(t, telegramPickerPage, kind)
	require.Equal(t, 2, value)

	kind, value, ok = telegramModelCallback("pm:s:5")
	require.True(t, ok)
	require.Equal(t, telegramPickerSelect, kind)
	require.Equal(t, 5, value)

	for _, data := range []string{"pm:x:1", "pm:p:-1", "pm:s:", "pu:s:1", "pc:/list models", ""} {
		_, _, ok := telegramModelCallback(data)
		require.False(t, ok, "callback data %q should be rejected", data)
	}
}

func TestHandleCallbackQuery_ModelSelection(t *testing.T) {
	testHandlePickerSelection(
		t,
		"cb-model",
		"pm:s:1",
		modelListText("gpt-4.1", "claude-sonnet-4"),
		"/switch model 2",
	)
}

func TestHandleCallbackQuery_ModelPageEditsMarkup(t *testing.T) {
	models := make([]string, 23)
	for i := range models {
		models[i] = fmt.Sprintf("model-%02d", i)
	}
	testHandlePickerPageEdit(
		t,
		"cb-model-page",
		"pm:p:1",
		modelListText(models...),
		"11. model-10",
		"pm:s:10",
		6,
	)
}

func TestHandleCallbackQuery_ModelMenuExpired(t *testing.T) {
	testHandlePickerExpired(t, "cb-model-expired", "pm:s:0", "No models configured")
}

func testHandlePickerSelection(t *testing.T, id, data, text, wantCommand string) {
	t.Helper()

	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "answerCallbackQuery") {
				t.Fatalf("unexpected API call: %s", url)
			}
			return &ta.Response{Ok: true, Result: []byte("true")}, nil
		},
	}
	ch, messageBus := newCallbackTestChannel(t, caller, nil)

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:   id,
		From: telego.User{ID: 777, FirstName: "Alice", Username: "alice"},
		Data: data,
		Message: &telego.Message{
			MessageID: 42,
			Chat:      telego.Chat{ID: 12345, Type: "private"},
			Text:      text,
		},
	})
	require.NoError(t, err)

	select {
	case inbound := <-messageBus.InboundChan():
		require.Equal(t, wantCommand, inbound.Content)
		require.Equal(t, "42", inbound.Context.ReplyToMessageID)
	case <-time.After(time.Second):
		t.Fatal("no inbound message published")
	}

	requireCallbackAnswered(t, caller)
}

func testHandlePickerPageEdit(
	t *testing.T,
	id, data, text, wantFirstLabel, wantFirstCallback string,
	wantRows int,
) {
	t.Helper()

	var editBody []byte
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, req *ta.RequestData) (*ta.Response, error) {
			switch {
			case strings.Contains(url, "answerCallbackQuery"):
				return &ta.Response{Ok: true, Result: []byte("true")}, nil
			case strings.Contains(url, "editMessageReplyMarkup"):
				editBody = append([]byte(nil), req.BodyRaw...)
				return successResponse(t), nil
			default:
				t.Fatalf("unexpected API call: %s", url)
				return nil, nil
			}
		},
	}
	ch, _ := newCallbackTestChannel(t, caller, nil)

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:   id,
		From: telego.User{ID: 777, FirstName: "Alice"},
		Data: data,
		Message: &telego.Message{
			MessageID: 42,
			Chat:      telego.Chat{ID: 12345, Type: "private"},
			Text:      text,
		},
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(editBody, &payload))
	markup, ok := payload["reply_markup"].(map[string]any)
	require.True(t, ok)
	rows, ok := markup["inline_keyboard"].([]any)
	require.True(t, ok)
	require.Len(t, rows, wantRows)

	firstRow, ok := rows[0].([]any)
	require.True(t, ok)
	firstButton, ok := firstRow[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, wantFirstLabel, firstButton["text"])
	require.Equal(t, wantFirstCallback, firstButton["callback_data"])

	navRow, ok := rows[wantRows-1].([]any)
	require.True(t, ok)
	require.NotEmpty(t, navRow)
	requireCallbackAnswered(t, caller)
}

func testHandlePickerExpired(t *testing.T, id, data, text string) {
	t.Helper()

	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			if !strings.Contains(url, "answerCallbackQuery") {
				t.Fatalf("unexpected API call: %s", url)
			}
			return &ta.Response{Ok: true, Result: []byte("true")}, nil
		},
	}
	ch, messageBus := newCallbackTestChannel(t, caller, nil)

	err := ch.handleCallbackQuery(context.Background(), telego.CallbackQuery{
		ID:   id,
		From: telego.User{ID: 777, FirstName: "Alice"},
		Data: data,
		Message: &telego.Message{
			MessageID: 42,
			Chat:      telego.Chat{ID: 12345, Type: "private"},
			Text:      text,
		},
	})
	require.NoError(t, err)

	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	case <-time.After(100 * time.Millisecond):
	}

	requireCallbackAnswered(t, caller)
}

func useHelpText(skills ...string) string {
	if len(skills) == 0 {
		return "Usage: /use <skill> [message]\nNo installed skills found."
	}
	return fmt.Sprintf(
		"Usage: /use <skill> [message]\n\nInstalled Skills:\n- %s\n\nUse /use <skill> to apply a skill to your next message, or /use <skill> <message> to force it immediately.",
		strings.Join(skills, "\n- "),
	)
}

func modelListText(models ...string) string {
	if len(models) == 0 {
		return "No models configured"
	}
	lines := make([]string, len(models))
	for i, name := range models {
		lines[i] = fmt.Sprintf("%d. %s (Provider: test)", i+1, name)
	}
	return "Configured Models:\n" + strings.Join(lines, "\n") +
		"\n\nUse /switch model <index> or /switch model <name>."
}

func newCallbackTestChannel(
	t *testing.T,
	caller *stubCaller,
	allowFrom []string,
) (*TelegramChannel, *bus.MessageBus) {
	t.Helper()

	bot, err := telego.NewBot(testToken,
		telego.WithAPICaller(caller),
		telego.WithDiscardLogger(),
	)
	require.NoError(t, err)

	messageBus := bus.NewMessageBus()
	base := channels.NewBaseChannel("telegram", nil, messageBus, allowFrom)
	base.SetRunning(true)

	return &TelegramChannel{
		BaseChannel: base,
		bot:         bot,
		chatIDs:     make(map[string]int64),
		ctx:         context.Background(),
		tgCfg:       &config.TelegramSettings{},
	}, messageBus
}

func requireCallbackAnswered(t *testing.T, caller *stubCaller) {
	t.Helper()

	for _, call := range caller.calls {
		if strings.Contains(call.URL, "answerCallbackQuery") {
			return
		}
	}
	t.Fatal("answerCallbackQuery was not called")
}
