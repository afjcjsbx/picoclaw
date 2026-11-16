// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package channels

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/redact"
)

// textRecorder is the channel-side streamer: it keeps what the manager's
// wrappers hand it.
type textRecorder struct {
	updates, finals       []string
	reasoning, reasonDone []string
}

func (r *textRecorder) Update(_ context.Context, c string) error {
	r.updates = append(r.updates, c)
	return nil
}

func (r *textRecorder) Finalize(_ context.Context, c string) error {
	r.finals = append(r.finals, c)
	return nil
}

func (r *textRecorder) Cancel(context.Context) {}

func (r *textRecorder) UpdateReasoning(_ context.Context, c string) error {
	r.reasoning = append(r.reasoning, c)
	return nil
}

func (r *textRecorder) FinalizeReasoning(_ context.Context, c string) error {
	r.reasonDone = append(r.reasonDone, c)
	return nil
}

// TestStreamersMaskTextTheSameWayThroughout pins the chat policy on both
// wrappers: exact values and known formats are masked on every update and on
// finalize alike, so a streamed message does not change when it completes, and
// code in the reply is left as written.
func TestStreamersMaskTextTheSameWayThroughout(t *testing.T) {
	const key = "registered-key-abcdef123456"
	redact.SetSecrets([]string{key})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	const code = "password = getPassword()"
	in := "use " + key + " and ghp_abcdefghijklmnopqrstuvwxyz0123456789\n" + code
	want := "use [FILTERED] and [FILTERED]\n" + code

	wrappers := map[string]func(*textRecorder, *string) bus.Streamer{
		"finalizeHookStreamer": func(r *textRecorder, hook *string) bus.Streamer {
			return &finalizeHookStreamer{
				Streamer:   r,
				onFinalize: func(_ context.Context, c string) { *hook = c },
			}
		},
		"splitMarkerStreamer": func(r *textRecorder, hook *string) bus.Streamer {
			return &splitMarkerStreamer{
				current:    r,
				reasoning:  r,
				onFinalize: func(_ context.Context, c string) { *hook = c },
			}
		},
	}
	for name, build := range wrappers {
		t.Run(name, func(t *testing.T) {
			rec, hook := &textRecorder{}, ""
			s := build(rec, &hook)
			ctx := context.Background()

			if err := s.Update(ctx, in); err != nil {
				t.Fatal(err)
			}
			reasoning := s.(bus.ReasoningStreamer)
			if err := reasoning.UpdateReasoning(ctx, in); err != nil {
				t.Fatal(err)
			}
			if err := reasoning.FinalizeReasoning(ctx, in); err != nil {
				t.Fatal(err)
			}
			if err := s.Finalize(ctx, in); err != nil {
				t.Fatal(err)
			}

			got := map[string][]string{
				"update":             rec.updates,
				"finalize":           rec.finals,
				"update reasoning":   rec.reasoning,
				"finalize reasoning": rec.reasonDone,
				"finalize hook":      {hook},
			}
			for step, texts := range got {
				if len(texts) != 1 || texts[0] != want {
					t.Errorf("%s = %q, want [%q]", step, texts, want)
				}
			}
		})
	}
}
