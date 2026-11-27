package api

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"moyue/internal/auth"
	"moyue/internal/store"
)

// signInAs 直接在数据层创建会话，避免测试依赖已经移除的本地密码登录。
func signInAs(t *testing.T, c *testClient, st *store.Store, u *store.User) {
	t.Helper()
	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.PutSession(hashToken(token), store.Session{
		UserID:    u.ID,
		UserAgent: "test",
		IP:        "127.0.0.1",
		CreatedAt: now,
		LastSeen:  now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(c.base)
	if err != nil {
		t.Fatal(err)
	}
	c.client.Jar.SetCookies(base, []*http.Cookie{{
		Name:  sessionCookie,
		Value: token,
		Path:  "/",
	}})
}
