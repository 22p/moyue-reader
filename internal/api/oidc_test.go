package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-jose/go-jose/v4"

	"moyue/internal/oidc"
	"moyue/internal/store"
)

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/":                    "/",
		"/#/admin":             "/#/admin",
		"/#/read/book-1":       "/#/read/book-1",
		"//evil.example":       "/",
		"https://evil.example": "/",
		"\\evil.example":       "/",
		"/\\evil.example":      "/",
		"/ok?x=1":              "/ok?x=1",
	}
	for input, want := range cases {
		if got := safeNext(input); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestOIDCDisabled(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv, err := New(st, t.TempDir(), testWebFS())
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/api/auth/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("start status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/?auth_error=disabled" {
		t.Fatalf("redirect = %q", got)
	}
}

// mockIDP 是最小可用的 OIDC 提供商：发现文档、JWKS 与令牌端点。
type mockIDP struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	mu      sync.Mutex
	nonce   string
	subject string
	email   string
	name    string
}

func newMockIDP(t *testing.T) *mockIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &mockIDP{key: key}
	mux := http.NewServeMux()
	idp.server = httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                idp.server.URL,
			"authorization_endpoint":                idp.server.URL + "/auth",
			"token_endpoint":                        idp.server.URL + "/token",
			"jwks_uri":                              idp.server.URL + "/jwks",
			"end_session_endpoint":                  idp.server.URL + "/logout",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key:       &idp.key.PublicKey,
			KeyID:     "test-key",
			Algorithm: string(jose.RS256),
			Use:       "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		idp.mu.Lock()
		nonce, subject, email, name := idp.nonce, idp.subject, idp.email, idp.name
		idp.mu.Unlock()
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key},
			(&jose.SignerOptions{}).WithType(jose.ContentType("JWT")).WithHeader("kid", "test-key"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"iss":            idp.server.URL,
			"sub":            subject,
			"aud":            "moyue-test",
			"exp":            time.Now().Add(time.Hour).Unix(),
			"iat":            time.Now().Unix(),
			"nonce":          nonce,
			"email":          email,
			"email_verified": true,
			"name":           name,
		})
		obj, err := signer.Sign(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw, err := obj.CompactSerialize()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "test-token",
			"token_type":   "Bearer",
			"id_token":     raw,
		})
	})
	t.Cleanup(idp.server.Close)
	return idp
}

func (m *mockIDP) setIdentity(subject, email, name, nonce string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subject, m.email, m.name, m.nonce = subject, email, name, nonce
}

func testWebFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
		"pdfview.html":         &fstest.MapFile{Data: []byte("<!doctype html><title>pdf</title>")},
		"audioview.html":       &fstest.MapFile{Data: []byte("<!doctype html><title>audio</title>")},
		"manifest.webmanifest": &fstest.MapFile{Data: []byte(`{"name":"test"}`)},
		"sw.js":                &fstest.MapFile{Data: []byte("self.addEventListener('fetch',()=>{})")},
		"offline.html":         &fstest.MapFile{Data: []byte("<!doctype html><title>offline</title>")},
	}
}

func TestOIDCLoginFlow(t *testing.T) {
	idp := newMockIDP(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	client, err := oidc.New(context.Background(), oidc.Config{
		DiscoveryURL: idp.server.URL + "/.well-known/openid-configuration",
		ClientID:     "moyue-test",
		AdminIDs:     []string{"admin-sub"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, t.TempDir(), testWebFS(), Options{OIDC: client})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	login := func(subject, email, name, next string) (*http.Client, string) {
		t.Helper()
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatal(err)
		}
		hc := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		resp, err := hc.Get(ts.URL + "/api/auth/oidc/start?next=" + url.QueryEscape(next))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("start status = %d", resp.StatusCode)
		}
		authURL, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		state := authURL.Query().Get("state")
		if state == "" || authURL.Query().Get("code_challenge") == "" ||
			authURL.Query().Get("code_challenge_method") != "S256" {
			t.Fatalf("auth url missing state/PKCE: %s", authURL)
		}
		idp.setIdentity(subject, email, name, authURL.Query().Get("nonce"))

		callback, err := hc.Get(ts.URL + "/api/auth/oidc/callback?code=test-code&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatal(err)
		}
		callback.Body.Close()
		if callback.StatusCode != http.StatusFound {
			t.Fatalf("callback status = %d", callback.StatusCode)
		}
		if got := callback.Header.Get("Location"); got != next {
			t.Fatalf("callback redirect = %q, want %q", got, next)
		}
		return hc, state
	}

	adminClient, _ := login("admin-sub", "admin@example.com", "Admin", "/#/admin")
	resp, err := adminClient.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d", resp.StatusCode)
	}
	if user, _ := me["user"].(map[string]any); user == nil || user["isAdmin"] != true {
		t.Fatalf("admin login = %v", me)
	}
	users := st.ListUsers()
	if len(users) != 1 || users[0].OIDCSubject != "admin-sub" || !users[0].IsAdmin {
		t.Fatalf("admin user = %+v", users)
	}

	userClient, _ := login("reader-sub", "reader@example.com", "Reader", "/")
	resp, err = userClient.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if user, _ := me["user"].(map[string]any); user == nil || user["isAdmin"] != false {
		t.Fatalf("reader login = %v", me)
	}
	if users := st.ListUsers(); len(users) != 2 {
		t.Fatalf("users = %d, want 2", len(users))
	}

	// 同一 subject 再次登录不应重复建号。
	again, _ := login("admin-sub", "admin@example.com", "Admin", "/")
	if users := st.ListUsers(); len(users) != 2 {
		t.Fatalf("users after relogin = %d, want 2", len(users))
	}

	// 退出时返回身份提供商的注销地址，并携带 id_token_hint 与注销回调。
	logoutResp, err := again.Post(ts.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	var logoutBody map[string]any
	_ = json.NewDecoder(logoutResp.Body).Decode(&logoutBody)
	logoutResp.Body.Close()
	redirect, _ := logoutBody["redirectUrl"].(string)
	if redirect == "" {
		t.Fatalf("logout redirect missing: %v", logoutBody)
	}
	logoutURL, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	if logoutURL.String() == "" || logoutURL.Query().Get("client_id") != "moyue-test" ||
		logoutURL.Query().Get("id_token_hint") == "" ||
		!strings.HasSuffix(logoutURL.Query().Get("post_logout_redirect_uri"), "/api/auth/oidc/logout/callback") {
		t.Fatalf("logout url = %s", redirect)
	}

	// 注销回调清除本地会话并回到登录页。
	noRedirect := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	callback, err := noRedirect.Get(ts.URL + "/api/auth/oidc/logout/callback")
	if err != nil {
		t.Fatal(err)
	}
	callback.Body.Close()
	if callback.StatusCode != http.StatusFound || callback.Header.Get("Location") != "/?logged_out=1" {
		t.Fatalf("logout callback = %d %q", callback.StatusCode, callback.Header.Get("Location"))
	}
}

func TestOIDCCallbackRejectsBadState(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	idp := newMockIDP(t)
	client, err := oidc.New(context.Background(), oidc.Config{
		DiscoveryURL: idp.server.URL + "/.well-known/openid-configuration",
		ClientID:     "moyue-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, t.TempDir(), testWebFS(), Options{OIDC: client})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/auth/oidc/callback?code=x&state=forged")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("followed callback status = %d", resp.StatusCode)
	}
	if got := resp.Request.URL.Query().Get("auth_error"); got != "state" {
		t.Fatalf("auth_error = %q", got)
	}
}

func TestOIDCSilentLogin(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	idp := newMockIDP(t)
	client, err := oidc.New(context.Background(), oidc.Config{
		DiscoveryURL: idp.server.URL + "/.well-known/openid-configuration",
		ClientID:     "moyue-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, t.TempDir(), testWebFS(), Options{OIDC: client})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// 静默登录必须携带 prompt=none，避免身份提供商弹出登录页。
	resp, err := hc.Get(ts.URL + "/api/auth/oidc/start?silent=1&next=" + url.QueryEscape("/#/read/abc"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	authURL, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := authURL.Query().Get("prompt"); got != "none" {
		t.Fatalf("silent prompt = %q, want none", got)
	}
	state := authURL.Query().Get("state")

	// 身份提供商未登录时返回 login_required，应回到本地登录页而非报错。
	callback, err := hc.Get(ts.URL + "/api/auth/oidc/callback?error=login_required&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	callback.Body.Close()
	if got := callback.Header.Get("Location"); got != "/?sso=0#/read/abc" {
		t.Fatalf("silent callback redirect = %q", got)
	}
	if me, err := hc.Get(ts.URL + "/api/auth/me"); err != nil {
		t.Fatal(err)
	} else {
		me.Body.Close()
		if me.StatusCode != http.StatusUnauthorized {
			t.Fatalf("silent failure should not create session, /me = %d", me.StatusCode)
		}
	}

	// 普通登录不带 prompt，用户点击按钮后仍由身份提供商展示登录页。
	resp, err = hc.Get(ts.URL + "/api/auth/oidc/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	authURL, err = url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := authURL.Query().Get("prompt"); got != "" {
		t.Fatalf("normal prompt = %q, want empty", got)
	}
}
