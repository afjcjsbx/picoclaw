package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/channels"
)

// reactionParams mirrors the JSON body telego sends to setMessageReaction. It is
// deliberately decoded as raw JSON instead of telego.SetMessageReactionParams so
// the assertions see exactly what goes on the wire, including whether the
// "reaction" field is present at all.
type reactionParams struct {
	ChatID    int64 `json:"chat_id"`
	MessageID int   `json:"message_id"`
	Reaction  []struct {
		Type  string `json:"type"`
		Emoji string `json:"emoji"`
	} `json:"reaction"`
	IsBig bool `json:"is_big"`
}

func okResponse() *ta.Response {
	return &ta.Response{Ok: true, Result: []byte("true")}
}

// TestReactToMessage_AddsEyesReaction pins the wire format of the acknowledgement
// reaction: one emoji reaction on the right chat/message, using an emoji from the
// set Telegram accepts for non-premium bots.
func TestReactToMessage_AddsEyesReaction(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			return okResponse(), nil
		},
	}
	ch := newTestChannel(t, caller)

	undo, err := ch.ReactToMessage(context.Background(), "-1001234567890/42", "77")
	require.NoError(t, err)
	require.NotNil(t, undo)

	require.Len(t, caller.calls, 1)
	assert.Contains(t, caller.calls[0].URL, "setMessageReaction")

	var params reactionParams
	require.NoError(t, json.Unmarshal(caller.calls[0].Data.BodyRaw, &params))
	assert.Equal(t, int64(-1001234567890), params.ChatID)
	assert.Equal(t, 77, params.MessageID)
	require.Len(t, params.Reaction, 1)
	assert.Equal(t, "emoji", params.Reaction[0].Type)
	assert.Equal(t, telegramAckReaction, params.Reaction[0].Emoji)
}

// TestReactToMessage_UndoClearsReaction guards the removal path. It must be a
// setMessageReaction call with no reactions (a missing "reaction" field means
// "set no reactions"), not deleteMessageReaction, which needs the
// can_delete_messages administrator right in groups.
func TestReactToMessage_UndoClearsReaction(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			return okResponse(), nil
		},
	}
	ch := newTestChannel(t, caller)

	undo, err := ch.ReactToMessage(context.Background(), "-1001234567890", "77")
	require.NoError(t, err)

	undo()

	require.Len(t, caller.calls, 2)
	assert.NotContains(t, caller.calls[1].URL, "deleteMessageReaction",
		"removal must not require the can_delete_messages right")

	var params map[string]any
	require.NoError(t, json.Unmarshal(caller.calls[1].Data.BodyRaw, &params))
	assert.EqualValues(t, -1001234567890, params["chat_id"])
	assert.EqualValues(t, 77, params["message_id"])
	assert.NotContains(t, params, "reaction", "an empty reaction list means the bot's reaction is removed")
}

// TestReactToMessage_UndoIsIdempotent is required by channels.ReactionCapable:
// the Manager may call the undo from both preSend and the TTL janitor, so a
// second call must not issue another API request.
func TestReactToMessage_UndoIsIdempotent(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			return okResponse(), nil
		},
	}
	ch := newTestChannel(t, caller)

	undo, err := ch.ReactToMessage(context.Background(), "42", "7")
	require.NoError(t, err)

	undo()
	undo()
	undo()

	assert.Len(t, caller.calls, 2, "repeated undo calls must be no-ops after the first")
}

// TestReactToMessage_UndoSurvivesCanceledContext covers the real delivery order:
// the reply is sent after the turn ends, so the inbound context handed to
// ReactToMessage may already be canceled when the undo runs. The removal must
// still reach Telegram instead of being dropped, otherwise the 👀 stays forever.
func TestReactToMessage_UndoSurvivesCanceledContext(t *testing.T) {
	caller := &stubCaller{
		callFn: func(ctx context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return okResponse(), nil
		},
	}
	ch := newTestChannel(t, caller)

	ctx, cancel := context.WithCancel(context.Background())
	undo, err := ch.ReactToMessage(ctx, "42", "7")
	require.NoError(t, err)

	cancel()
	undo()

	require.Len(t, caller.calls, 2)
}

// TestReactToMessage_UndoHasDeadline ensures the detached undo context is still
// bounded, so a hung Telegram request cannot leak a goroutine forever.
func TestReactToMessage_UndoHasDeadline(t *testing.T) {
	var deadlines []time.Time
	caller := &stubCaller{
		callFn: func(ctx context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			deadline, _ := ctx.Deadline()
			deadlines = append(deadlines, deadline)
			return okResponse(), nil
		},
	}
	ch := newTestChannel(t, caller)

	undo, err := ch.ReactToMessage(context.Background(), "42", "7")
	require.NoError(t, err)

	// A background parent has no deadline of its own, so any deadline observed
	// on the removal call must come from telegramReactionUndoTimeout.
	undo()

	require.Len(t, deadlines, 2)
	assert.True(t, deadlines[0].IsZero(), "the add call inherits the caller context")
	assert.False(t, deadlines[1].IsZero(), "the undo request must carry its own deadline")
}

// TestReactToMessage_ReportsAddFailure makes sure a failed reaction is surfaced
// to the caller so the Manager skips registering a no-op undo.
func TestReactToMessage_ReportsAddFailure(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			return nil, errors.New(`api: 400 "Bad Request: REACTION_INVALID"`)
		},
	}
	ch := newTestChannel(t, caller)

	undo, err := ch.ReactToMessage(context.Background(), "42", "7")

	require.Error(t, err)
	require.NotNil(t, undo, "a usable no-op undo must always be returned")
	undo() // must not panic
	assert.Len(t, caller.calls, 1)
}

// TestReactToMessage_RemovalFailureIsNotFatal covers a message deleted between
// the reaction and the undo: the error must not panic or retry, only be logged.
func TestReactToMessage_RemovalFailureIsNotFatal(t *testing.T) {
	var calls int
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			calls++
			if calls == 2 {
				return nil, errors.New(`api: 400 "Bad Request: message to react not found"`)
			}
			return okResponse(), nil
		},
	}
	ch := newTestChannel(t, caller)

	undo, err := ch.ReactToMessage(context.Background(), "42", "7")
	require.NoError(t, err)

	assert.NotPanics(t, undo)
	assert.Equal(t, 2, calls)
}

// TestReactToMessage_RejectsBadIDs keeps malformed IDs from reaching the API.
func TestReactToMessage_RejectsBadIDs(t *testing.T) {
	tests := []struct {
		name      string
		chatID    string
		messageID string
	}{
		{name: "non numeric chat", chatID: "not-a-chat", messageID: "7"},
		{name: "non numeric message", chatID: "42", messageID: "not-a-message"},
		{name: "bad thread", chatID: "42/thread", messageID: "7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caller := &stubCaller{
				callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
					t.Fatal("no API call may be made for invalid IDs")
					return nil, nil
				},
			}
			ch := newTestChannel(t, caller)

			undo, err := ch.ReactToMessage(context.Background(), tc.chatID, tc.messageID)

			require.Error(t, err)
			require.NotNil(t, undo)
			undo()
			assert.Empty(t, caller.calls)
		})
	}
}

// TestTelegramImplementsReactionCapable is a runtime guard for the capability the
// Manager discovers by type assertion from agent tooling and BaseChannel.
func TestTelegramImplementsReactionCapable(t *testing.T) {
	var ch any = &TelegramChannel{}
	_, ok := ch.(channels.ReactionCapable)
	assert.True(t, ok, "TelegramChannel must implement channels.ReactionCapable")
}
