package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Ensure OpenAICompatTranscriber satisfies the Transcriber interface at compile time.
var _ Transcriber = (*OpenAICompatTranscriber)(nil)
var _ Transcriber = (*FallbackTranscriber)(nil)

func TestOpenAICompatTranscriberName(t *testing.T) {
	tr := NewOpenAICompatTranscriber("groq", "sk-test", "https://api.groq.com/openai/v1", "whisper-large-v3")
	if got := tr.Name(); got != "groq" {
		t.Errorf("Name() = %q, want %q", got, "groq")
	}
}

func TestDetectTranscriber(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.Config
		wantNil  bool
		wantName string
	}{
		{
			name:    "no config",
			cfg:     &config.Config{},
			wantNil: true,
		},
		{
			name: "groq provider key (auto-detect)",
			cfg: &config.Config{
				Providers: config.ProvidersConfig{
					Groq: config.ProviderConfig{APIKey: "sk-groq-direct"},
				},
			},
			wantName: "groq",
		},
		{
			name: "groq via model list (auto-detect)",
			cfg: &config.Config{
				ModelList: []config.ModelConfig{
					{Model: "openai/gpt-4o", APIKey: "sk-openai"},
					{Model: "groq/llama-3.3-70b", APIKey: "sk-groq-model"},
				},
			},
			wantName: "groq",
		},
		{
			name: "groq model list entry without key is skipped",
			cfg: &config.Config{
				ModelList: []config.ModelConfig{
					{Model: "groq/llama-3.3-70b", APIKey: ""},
				},
			},
			wantNil: true,
		},
		{
			name: "explicit transcriber selection",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "groq",
					APIKey:      "sk-voice-groq",
				},
			},
			wantName: "groq",
		},
		{
			name: "explicit elevenlabs",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs",
					APIKey:      "sk-elevenlabs",
				},
			},
			wantName: "elevenlabs",
		},
		{
			name: "explicit openai",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "openai",
					APIKey:      "sk-openai-voice",
				},
			},
			wantName: "openai",
		},
		{
			name: "unknown provider returns nil",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "nonexistent",
					APIKey:      "sk-key",
				},
			},
			wantNil: true,
		},
		{
			name: "explicit provider without key returns nil",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs",
				},
			},
			wantNil: true,
		},
		{
			name: "provider key takes priority over model list",
			cfg: &config.Config{
				Providers: config.ProvidersConfig{
					Groq: config.ProviderConfig{APIKey: "sk-groq-direct"},
				},
				ModelList: []config.ModelConfig{
					{Model: "groq/llama-3.3-70b", APIKey: "sk-groq-model"},
				},
			},
			wantName: "groq",
		},
		{
			name: "custom model override",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber:      "groq",
					TranscriberModel: "whisper-large-v3-turbo",
					APIKey:           "sk-groq-custom",
				},
			},
			wantName: "groq",
		},
		{
			name: "fallback chain with shared key",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs,groq",
					APIKey:      "sk-shared",
				},
			},
			wantName: "elevenlabs->groq",
		},
		{
			name: "fallback chain with per-provider overrides",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs,groq",
					Fallback: map[string]config.VoiceProviderConfig{
						"elevenlabs": {APIKey: "sk-el"},
						"groq":       {APIKey: "sk-groq"},
					},
				},
			},
			wantName: "elevenlabs->groq",
		},
		{
			name: "fallback chain skips provider without credentials",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs,groq",
					Fallback: map[string]config.VoiceProviderConfig{
						"groq": {APIKey: "sk-groq"},
					},
				},
			},
			wantName: "groq",
		},
		{
			name: "fallback chain all providers fail to build returns nil",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs,nonexistent",
				},
			},
			wantNil: true,
		},
		{
			name: "three-provider chain",
			cfg: &config.Config{
				Voice: config.VoiceConfig{
					Transcriber: "elevenlabs,openai,groq",
					Fallback: map[string]config.VoiceProviderConfig{
						"elevenlabs": {APIKey: "sk-el"},
						"openai":     {APIKey: "sk-oai"},
						"groq":       {APIKey: "sk-groq"},
					},
				},
			},
			wantName: "elevenlabs->openai->groq",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := DetectTranscriber(tc.cfg)
			if tc.wantNil {
				if tr != nil {
					t.Errorf("DetectTranscriber() = %v, want nil", tr)
				}
				return
			}
			if tr == nil {
				t.Fatal("DetectTranscriber() = nil, want non-nil")
			}
			if got := tr.Name(); got != tc.wantName {
				t.Errorf("Name() = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestTranscribe(t *testing.T) {
	tmpDir := t.TempDir()
	audioPath := filepath.Join(tmpDir, "clip.ogg")
	if err := os.WriteFile(audioPath, []byte("fake-audio-data"), 0o644); err != nil {
		t.Fatalf("failed to write fake audio file: %v", err)
	}

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/audio/transcriptions" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Errorf("unexpected Authorization header: %s", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TranscriptionResponse{
				Text:     "hello world",
				Language: "en",
				Duration: 1.5,
			})
		}))
		defer srv.Close()

		tr := NewOpenAICompatTranscriber("groq", "sk-test", srv.URL, "whisper-large-v3")

		resp, err := tr.Transcribe(context.Background(), audioPath)
		if err != nil {
			t.Fatalf("Transcribe() error: %v", err)
		}
		if resp.Text != "hello world" {
			t.Errorf("Text = %q, want %q", resp.Text, "hello world")
		}
		if resp.Language != "en" {
			t.Errorf("Language = %q, want %q", resp.Language, "en")
		}
	})

	t.Run("api error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"invalid_api_key"}`, http.StatusUnauthorized)
		}))
		defer srv.Close()

		tr := NewOpenAICompatTranscriber("groq", "sk-bad", srv.URL, "whisper-large-v3")

		_, err := tr.Transcribe(context.Background(), audioPath)
		if err == nil {
			t.Fatal("expected error for non-200 response, got nil")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		tr := NewOpenAICompatTranscriber("groq", "sk-test", "http://localhost", "whisper-large-v3")
		_, err := tr.Transcribe(context.Background(), filepath.Join(tmpDir, "nonexistent.ogg"))
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})
}

func TestFallbackTranscriber(t *testing.T) {
	tmpDir := t.TempDir()
	audioPath := filepath.Join(tmpDir, "clip.ogg")
	if err := os.WriteFile(audioPath, []byte("fake-audio-data"), 0o644); err != nil {
		t.Fatalf("failed to write fake audio file: %v", err)
	}

	t.Run("first succeeds", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TranscriptionResponse{Text: "from first"})
		}))
		defer srv.Close()

		first := NewOpenAICompatTranscriber("first", "sk-1", srv.URL, "m1")
		second := NewOpenAICompatTranscriber("second", "sk-2", srv.URL, "m2")
		ft := NewFallbackTranscriber([]Transcriber{first, second})

		resp, err := ft.Transcribe(context.Background(), audioPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != "from first" {
			t.Errorf("Text = %q, want %q", resp.Text, "from first")
		}
	})

	t.Run("first fails second succeeds", func(t *testing.T) {
		callCount := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				http.Error(w, "fail", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TranscriptionResponse{Text: "from second"})
		}))
		defer srv.Close()

		first := NewOpenAICompatTranscriber("first", "sk-1", srv.URL, "m1")
		second := NewOpenAICompatTranscriber("second", "sk-2", srv.URL, "m2")
		ft := NewFallbackTranscriber([]Transcriber{first, second})

		resp, err := ft.Transcribe(context.Background(), audioPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != "from second" {
			t.Errorf("Text = %q, want %q", resp.Text, "from second")
		}
	})

	t.Run("all fail", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fail", http.StatusInternalServerError)
		}))
		defer srv.Close()

		first := NewOpenAICompatTranscriber("first", "sk-1", srv.URL, "m1")
		second := NewOpenAICompatTranscriber("second", "sk-2", srv.URL, "m2")
		ft := NewFallbackTranscriber([]Transcriber{first, second})

		_, err := ft.Transcribe(context.Background(), audioPath)
		if err == nil {
			t.Fatal("expected error when all transcribers fail")
		}
		if !strings.Contains(err.Error(), "all 2 transcribers failed") {
			t.Errorf("error = %q, want it to contain 'all 2 transcribers failed'", err.Error())
		}
	})

	t.Run("context cancelled stops chain", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "fail", http.StatusInternalServerError)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		first := NewOpenAICompatTranscriber("first", "sk-1", srv.URL, "m1")
		second := NewOpenAICompatTranscriber("second", "sk-2", srv.URL, "m2")
		ft := NewFallbackTranscriber([]Transcriber{first, second})

		_, err := ft.Transcribe(ctx, audioPath)
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})

	t.Run("name shows chain", func(t *testing.T) {
		first := NewOpenAICompatTranscriber("elevenlabs", "sk-1", "http://a", "m1")
		second := NewOpenAICompatTranscriber("groq", "sk-2", "http://b", "m2")
		third := NewOpenAICompatTranscriber("openai", "sk-3", "http://c", "m3")
		ft := NewFallbackTranscriber([]Transcriber{first, second, third})

		if got := ft.Name(); got != "elevenlabs->groq->openai" {
			t.Errorf("Name() = %q, want %q", got, "elevenlabs->groq->openai")
		}
	})
}

func TestRegisteredTranscriberNames(t *testing.T) {
	names := registeredTranscriberNames()
	want := map[string]bool{"groq": false, "elevenlabs": false, "openai": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("expected %q to be registered, but it was not found in %v", n, names)
		}
	}
}
