package auth

import "testing"

func TestTokens(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewToken()
	if a == b {
		t.Error("tokens should be unique")
	}
	if TokenHash(a) == TokenHash(b) {
		t.Error("hashes should differ")
	}
	if len(TokenHash(a)) != 64 {
		t.Errorf("hash length = %d", len(TokenHash(a)))
	}
}
