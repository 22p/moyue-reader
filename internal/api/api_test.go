package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"moyue/internal/store"
)

type testClient struct {
	t      *testing.T
	base   string
	client *http.Client
}

func newTestClient(t *testing.T, base string) *testClient {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testClient{t: t, base: base, client: &http.Client{Jar: jar}}
}

func (c *testClient) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var data map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &data)
	}
	return resp.StatusCode, data
}

func TestMultiUserLibraryFlow(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	booksDir := filepath.Join(root, "books")
	libDir := filepath.Join(root, "library")
	for _, dir := range []string{dataDir, booksDir, libDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	txt := "第一章 开始\n\n这是正文第一段。\n\n第二章 继续\n\n这是正文第二段。"
	if err := os.WriteFile(filepath.Join(libDir, "测试书 - 作者.txt"), []byte(txt), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
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
	user := newTestClient(t, ts.URL)

	if code, _ := admin.do("GET", "/api/books", nil); code != http.StatusUnauthorized {
		t.Fatalf("unauthorized books = %d", code)
	}
	if code, _ := admin.do("POST", "/api/auth/setup", map[string]string{"username": "admin", "password": "admin123"}); code != http.StatusCreated {
		t.Fatalf("setup = %d", code)
	}
	if code, _ := admin.do("POST", "/api/auth/setup", map[string]string{"username": "again", "password": "admin123"}); code != http.StatusConflict {
		t.Fatalf("second setup = %d", code)
	}

	code, libResp := admin.do("POST", "/api/admin/libraries", map[string]string{
		"name": "测试库", "path": libDir, "type": "txt",
	})
	if code != http.StatusCreated {
		t.Fatalf("create library = %d (%v)", code, libResp)
	}
	libID := libResp["library"].(map[string]any)["id"].(string)

	var books []any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, list := admin.do("GET", "/api/books", nil)
		if b, ok := list["books"].([]any); ok && len(b) == 1 {
			books = b
			break
		}
		time.Sleep(80 * time.Millisecond)
	}
	if len(books) != 1 {
		t.Fatalf("scanned books = %d, want 1", len(books))
	}
	book := books[0].(map[string]any)
	bookID := book["id"].(string)
	if book["title"] != "测试书" || book["creator"] != "作者" || book["chapterCount"].(float64) != 2 {
		t.Errorf("book meta = %v", book)
	}

	code, userResp := admin.do("POST", "/api/admin/users", map[string]any{
		"username": "reader", "password": "reader123", "libraries": []string{},
	})
	if code != http.StatusCreated {
		t.Fatalf("create user = %d (%v)", code, userResp)
	}
	userID := userResp["user"].(map[string]any)["id"].(string)

	if code, _ := user.do("POST", "/api/auth/login", map[string]string{"username": "reader", "password": "reader123"}); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	_, list := user.do("GET", "/api/books", nil)
	if got := len(list["books"].([]any)); got != 0 {
		t.Errorf("user without library sees %d books", got)
	}
	if code, _ := user.do("GET", "/api/books/"+bookID, nil); code != http.StatusForbidden {
		t.Errorf("forbidden detail = %d", code)
	}
	if code, _ := user.do("GET", "/api/admin/users", nil); code != http.StatusForbidden {
		t.Errorf("non-admin admin api = %d", code)
	}

	if code, _ := admin.do("PATCH", "/api/admin/users/"+userID, map[string]any{"libraries": []string{libID}}); code != http.StatusOK {
		t.Fatalf("grant library = %d", code)
	}
	_, list = user.do("GET", "/api/books", nil)
	if got := len(list["books"].([]any)); got != 1 {
		t.Fatalf("user books after grant = %d", got)
	}

	if code, _ := user.do("PUT", "/api/books/"+bookID+"/progress", map[string]any{"chapter": 1, "chapterPercent": 0.5}); code != http.StatusOK {
		t.Fatalf("save progress = %d", code)
	}
	_, detail := user.do("GET", "/api/books/"+bookID, nil)
	progress := detail["progress"].(map[string]any)
	if p := progress["percent"].(float64); p < 74 || p > 76 {
		t.Errorf("user percent = %v, want 75", p)
	}
	_, adminDetail := admin.do("GET", "/api/books/"+bookID, nil)
	if p := adminDetail["progress"].(map[string]any)["percent"].(float64); p != 0 {
		t.Errorf("admin progress leaked: %v", p)
	}

	// TXT 章节可渲染
	req, _ := http.NewRequest("GET", ts.URL+"/api/books/"+bookID+"/chapter/0", nil)
	resp, err := user.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("这是正文第一段")) {
		t.Errorf("chapter render failed: %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte("这是正文第一段")) {
		t.Error("chapter body missing")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !bytes.Contains([]byte(csp), []byte("script-src 'none'")) {
		t.Errorf("chapter CSP missing: %q", csp)
	}

	// 全文搜索
	if code, search := user.do("GET", "/api/books/"+bookID+"/search?q=%E6%AD%A3%E6%96%87", nil); code != http.StatusOK {
		t.Errorf("search = %d", code)
	} else if hits, _ := search["results"].([]any); len(hits) == 0 {
		t.Error("search returned no results")
	}

	// 划线笔记 CRUD
	code, annResp := user.do("POST", "/api/books/"+bookID+"/annotations", map[string]any{
		"chapter": 0, "start": 0, "end": 2, "text": "这是", "note": "开篇", "color": "yellow",
	})
	if code != http.StatusCreated {
		t.Fatalf("add annotation = %d (%v)", code, annResp)
	}
	annID := annResp["annotation"].(map[string]any)["id"].(string)
	_, detail = user.do("GET", "/api/books/"+bookID, nil)
	if list, _ := detail["annotations"].([]any); len(list) != 1 {
		t.Errorf("annotations = %d, want 1", len(list))
	}
	if code, _ := user.do("PATCH", "/api/books/"+bookID+"/annotations/"+annID, map[string]any{"note": "改过", "color": "green"}); code != http.StatusOK {
		t.Errorf("update annotation = %d", code)
	}
	if code, _ := user.do("DELETE", "/api/books/"+bookID+"/annotations/"+annID, nil); code != http.StatusOK {
		t.Errorf("delete annotation = %d", code)
	}

	// 设备会话：另一个客户端登录后应能看到两个会话，并可撤销其它设备
	other := newTestClient(t, ts.URL)
	if code, _ := other.do("POST", "/api/auth/login", map[string]string{"username": "reader", "password": "reader123"}); code != http.StatusOK {
		t.Fatalf("second login = %d", code)
	}
	_, sessions := user.do("GET", "/api/auth/sessions", nil)
	if list, _ := sessions["sessions"].([]any); len(list) != 2 {
		t.Errorf("sessions = %d, want 2", len(list))
	}
	if code, _ := user.do("POST", "/api/auth/sessions/revoke-others", nil); code != http.StatusOK {
		t.Errorf("revoke others = %d", code)
	}
	_, sessions = user.do("GET", "/api/auth/sessions", nil)
	if list, _ := sessions["sessions"].([]any); len(list) != 1 {
		t.Errorf("sessions after revoke = %d, want 1", len(list))
	}

	// Origin 校验：跨站写请求被拒绝，同源请求放行
	csrfReq := func(origin string) int {
		req, _ := http.NewRequest("POST", ts.URL+"/api/books/"+bookID+"/bookmarks", strings.NewReader(`{"chapter":0,"percent":0.1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		resp, err := user.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := csrfReq("https://evil.example"); got != http.StatusForbidden {
		t.Errorf("cross-origin POST = %d, want 403", got)
	}
	if got := csrfReq(ts.URL); got != http.StatusCreated {
		t.Errorf("same-origin POST = %d, want 201", got)
	}
}

func TestPWARoutes(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	web := fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
		"pdfview.html":         &fstest.MapFile{Data: []byte("<!doctype html><title>pdf</title>")},
		"audioview.html":       &fstest.MapFile{Data: []byte("<!doctype html><title>audio</title>")},
		"manifest.webmanifest": &fstest.MapFile{Data: []byte(`{"name":"墨阅","start_url":"/"}`)},
		"sw.js":                &fstest.MapFile{Data: []byte("self.addEventListener('fetch',()=>{})")},
		"offline.html":         &fstest.MapFile{Data: []byte("<!doctype html><title>offline</title>")},
	}
	srv, err := New(st, filepath.Join(root, "books"), web)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []struct {
		path string
		ct   string
	}{
		{"/manifest.webmanifest", "application/manifest+json"},
		{"/sw.js", "text/javascript"},
		{"/offline.html", "text/html"},
	}
	for _, c := range cases {
		resp, err := http.Get(ts.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d", c.path, resp.StatusCode)
		}
		if !bytes.Contains([]byte(resp.Header.Get("Content-Type")), []byte(c.ct)) {
			t.Errorf("%s content-type = %q", c.path, resp.Header.Get("Content-Type"))
		}
		if len(body) == 0 {
			t.Errorf("%s body empty", c.path)
		}
	}

	resp, err := http.Get(ts.URL + "/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Service-Worker-Allowed"); got != "/" {
		t.Errorf("Service-Worker-Allowed = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); !bytes.Contains([]byte(got), []byte("no-cache")) {
		t.Errorf("sw cache-control = %q", got)
	}
}
