package oidc

import (
	"net/url"
	"testing"
)

func TestAdminMatching(t *testing.T) {
	c := &Client{
		adminSubjects: map[string]struct{}{"AdminSub": {}},
	}
	if !c.IsAdminSubject("AdminSub") {
		t.Error("exact subject should match")
	}
	if c.IsAdminSubject("adminsub") {
		t.Error("subject matching must be case-sensitive")
	}
	if c.IsAdminSubject("admin@example.com") {
		t.Error("email must not be treated as an admin subject")
	}
}

func TestEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Error("empty config should be disabled")
	}
	if !(Config{DiscoveryURL: "https://id.example.com/.well-known/openid-configuration"}).Enabled() {
		t.Error("discovery URL should enable OIDC")
	}
}

func TestLogoutURL(t *testing.T) {
	c := &Client{
		config:    Config{ClientID: "moyue"},
		discovery: Discovery{EndSessionEndpoint: "https://id.example.com/logout"},
	}
	got := c.LogoutURL("https://reader.example/api/auth/oidc/logout/callback", "raw-id-token")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "id.example.com" || q.Get("client_id") != "moyue" ||
		q.Get("post_logout_redirect_uri") != "https://reader.example/api/auth/oidc/logout/callback" ||
		q.Get("id_token_hint") != "raw-id-token" {
		t.Fatalf("logout url = %s", got)
	}
	c.discovery.EndSessionEndpoint = ""
	if got := c.LogoutURL("https://reader.example", "x"); got != "" {
		t.Fatalf("unsupported logout endpoint = %q", got)
	}
}
