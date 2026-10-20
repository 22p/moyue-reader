package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"moyue/internal/store"
)

func TestTTSSettingsAndSpeak(t *testing.T) {
	audio := []byte{0x49, 0x44, 0x33, 0x04, 0x00}
	var calls int
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		resp := map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{
					"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(audio)},
				},
			}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mock.Close()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	booksDir := filepath.Join(root, "books")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(booksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	web := fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
		"pdfview.html":         &fstest.MapFile{Data: []byte("<!doctype html><title>pdf</title>")},
		"audioview.html":       &fstest.MapFile{Data: []byte("<!doctype html><title>audio</title>")},
		"manifest.webmanifest": &fstest.MapFile{Data: []byte(`{"name":"test"}`)},
		"sw.js":                &fstest.MapFile{Data: []byte("self.addEventListener('fetch',()=>{})")},
		"offline.html":         &fstest.MapFile{Data: []byte("<!doctype html><title>offline</title>")},
	}
	srv, err := New(st, booksDir, web)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newTestClient(t, ts.URL)

	if code, _ := admin.do("POST", "/api/auth/setup", map[string]string{"username": "admin", "password": "admin123"}); code != http.StatusCreated {
		t.Fatalf("setup = %d", code)
	}

	code, body := admin.do("GET", "/api/admin/settings/tts", nil)
	if code != http.StatusOK {
		t.Fatalf("get settings = %d", code)
	}
	ttsView := body["tts"].(map[string]any)
	if ttsView["enabled"] != false || ttsView["hasKey"] != false || ttsView["provider"] != "mimo" {
		t.Fatalf("unexpected defaults: %+v", ttsView)
	}
	if ttsView["baseUrl"] == "" || ttsView["model"] != "mimo-v2.5-tts" {
		t.Fatalf("missing defaults: %+v", ttsView)
	}

	if code, _ := admin.do("PUT", "/api/admin/settings/tts", map[string]any{
		"enabled": true, "baseUrl": "ftp://bad", "model": "mimo-v2.5-tts", "voice": "mimo_default",
	}); code != http.StatusBadRequest {
		t.Fatalf("invalid url = %d", code)
	}

	code, body = admin.do("PUT", "/api/admin/settings/tts", map[string]any{
		"enabled":  true,
		"provider": "mimo",
		"baseUrl":  mock.URL,
		"model":    "mimo-v2.5-tts",
		"apiKey":   "secret-key",
		"voice":    "茉莉",
	})
	if code != http.StatusOK {
		t.Fatalf("put settings = %d (%v)", code, body)
	}
	ttsView = body["tts"].(map[string]any)
	if ttsView["hasKey"] != true || ttsView["keyHint"] != "…-key" {
		t.Fatalf("key hint: %+v", ttsView)
	}
	if strings.Contains(string(mustJSON(t, body)), "secret-key") {
		t.Fatal("api key leaked in response")
	}

	code, body = admin.do("GET", "/api/tts/status", nil)
	if code != http.StatusOK || body["enabled"] != true {
		t.Fatalf("status = %d (%v)", code, body)
	}

	// 直接写入书库与书籍，避免异步扫描带来的等待。
	lib := &store.Library{Name: "lib", Path: booksDir, Type: "txt"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	book := &store.Book{ID: "book-1", LibraryID: lib.ID, Type: "txt", File: "a.txt", Title: "测试书", ChapterCount: 1}
	if err := st.AddBook(book); err != nil {
		t.Fatal(err)
	}

	postTTS := func() (*http.Response, []byte) {
		raw, _ := json.Marshal(map[string]string{"text": "你好，世界。", "voice": "茉莉"})
		req, err := http.NewRequest("POST", ts.URL+"/api/books/book-1/tts", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := admin.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, data
	}

	resp, data := postTTS()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(data, audio) {
		t.Fatalf("speak = %d %q", resp.StatusCode, data)
	}
	if resp.Header.Get("X-Moyue-TTS-Cache") != "miss" {
		t.Fatalf("cache header = %q", resp.Header.Get("X-Moyue-TTS-Cache"))
	}
	resp, data = postTTS()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(data, audio) {
		t.Fatalf("speak cached = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Moyue-TTS-Cache") != "hit" {
		t.Fatalf("expected cache hit, got %q", resp.Header.Get("X-Moyue-TTS-Cache"))
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}

	// 未登录不能读取状态或合成。
	anon := newTestClient(t, ts.URL)
	if code, _ := anon.do("GET", "/api/tts/status", nil); code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d", code)
	}

	// 非管理员不能修改设置。
	if code, _ := admin.do("POST", "/api/admin/users", map[string]any{
		"username": "reader", "password": "reader123", "libraries": []string{lib.ID},
	}); code != http.StatusCreated {
		t.Fatalf("create user = %d", code)
	}
	reader := newTestClient(t, ts.URL)
	if code, _ := reader.do("POST", "/api/auth/login", map[string]string{"username": "reader", "password": "reader123"}); code != http.StatusOK {
		t.Fatalf("reader login = %d", code)
	}
	if code, _ := reader.do("PUT", "/api/admin/settings/tts", map[string]any{"enabled": false}); code != http.StatusForbidden {
		t.Fatalf("reader put settings = %d", code)
	}
	if code, _ := reader.do("GET", "/api/tts/status", nil); code != http.StatusOK {
		t.Fatalf("reader status = %d", code)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
