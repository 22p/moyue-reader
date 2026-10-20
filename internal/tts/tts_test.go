package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSynthesizeMiMoAndCache(t *testing.T) {
	audio := []byte{0x49, 0x44, 0x33, 0x04, 0x00, 0x00}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("api-key"); got != "secret" {
			t.Errorf("api-key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Audio struct {
				Format string `json:"format"`
				Voice  string `json:"voice"`
			} `json:"audio"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != DefaultModel || len(body.Messages) != 1 ||
			body.Messages[0].Role != "assistant" || body.Messages[0].Content == "" {
			t.Errorf("unexpected body: %+v", body)
		}
		if body.Audio.Format != "mp3" || body.Audio.Voice != "茉莉" {
			t.Errorf("unexpected audio: %+v", body.Audio)
		}
		resp := map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(audio)},
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cacheDir := filepath.Join(t.TempDir(), "tts")
	client := NewClient(cacheDir)
	cfg := Config{Provider: ProviderMiMo, BaseURL: srv.URL, Model: DefaultModel, APIKey: "secret", Voice: "茉莉"}

	data, mime, cached, err := client.Synthesize(context.Background(), cfg, "你好", "茉莉")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(audio) || mime != "audio/mpeg" || cached {
		t.Fatalf("data=%v mime=%s cached=%v", data, mime, cached)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}

	data2, _, cached2, err := client.Synthesize(context.Background(), cfg, "你好", "茉莉")
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != string(audio) || !cached2 {
		t.Fatal("expected cache hit")
	}
	if calls != 1 {
		t.Fatalf("cache miss, calls = %d", calls)
	}

	if _, _, _, err := client.Synthesize(context.Background(), cfg, "另一句", "茉莉"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("different text should call API, calls = %d", calls)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("cache files = %d", len(entries))
	}
}

func TestSynthesizeOpenAIFormat(t *testing.T) {
	audio := []byte{0xff, 0xfb, 0x90, 0x64}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["input"] != "hello" || body["response_format"] != "mp3" {
			t.Errorf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(audio)
	}))
	defer srv.Close()

	client := NewClient("")
	cfg := Config{Provider: ProviderOpenAI, BaseURL: srv.URL + "/v1/audio/speech", Model: "tts-1", APIKey: "k", Voice: "alloy"}
	data, mime, _, err := client.Synthesize(context.Background(), cfg, "hello", "alloy")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/audio/speech" {
		t.Fatalf("path = %q", gotPath)
	}
	if string(data) != string(audio) || !strings.HasPrefix(mime, "audio/") {
		t.Fatalf("data=%v mime=%s", data, mime)
	}
}

func TestSynthesizeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	client := NewClient("")
	_, _, _, err := client.Synthesize(context.Background(),
		Config{Provider: ProviderMiMo, BaseURL: srv.URL, APIKey: "bad", Voice: "mimo_default"}, "hi", "")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("err = %v", err)
	}
}

func TestVoicesAndURLValidation(t *testing.T) {
	if !ValidVoice(ProviderMiMo, "冰糖") || ValidVoice(ProviderMiMo, "unknown") {
		t.Fatal("mimo voice validation")
	}
	if !ValidVoice(ProviderOpenAI, "alloy") || ValidVoice(ProviderOpenAI, "bad voice!") {
		t.Fatal("openai voice validation")
	}
	if err := ValidateURL("https://api.xiaomimimo.com/v1/chat/completions"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateURL("ftp://example.com"); err == nil {
		t.Fatal("ftp should be rejected")
	}
	if len(Voices(ProviderMiMo)) != len(MiMoVoices) {
		t.Fatal("voice list")
	}
}
