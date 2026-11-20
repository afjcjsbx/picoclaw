package telegram

import (
	"context"
	"encoding/json"
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
