package main

import "testing"

func TestEnvDefault(t *testing.T) {
	t.Setenv("MOYUE_OIDC_DISCOVERY", "")
	if got := envDefault("fallback", "MOYUE_OIDC_DISCOVERY"); got != "fallback" {
		t.Fatalf("empty env = %q", got)
	}

	// 只接受 MOYUE_ 前缀，避免与其他服务的同名变量冲突。
	t.Setenv("OIDC_DISCOVERY", "https://unprefixed.example.com")
	if got := envDefault("fallback", "MOYUE_OIDC_DISCOVERY"); got != "fallback" {
		t.Fatalf("unprefixed env should be ignored, got %q", got)
	}

	t.Setenv("MOYUE_OIDC_DISCOVERY", "https://prefixed.example.com")
	if got := envDefault("fallback", "MOYUE_OIDC_DISCOVERY"); got != "https://prefixed.example.com" {
		t.Fatalf("prefixed env = %q", got)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("openid, profile\nemail  user-1,user-2")
	want := []string{"openid", "profile", "email", "user-1", "user-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
