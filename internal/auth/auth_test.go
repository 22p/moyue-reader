package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("reader123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$") {
		t.Fatalf("unexpected format: %s", hash)
	}
	if !VerifyPassword(hash, "reader123") {
		t.Error("correct password rejected")
	}
	if VerifyPassword(hash, "reader124") {
		t.Error("wrong password accepted")
	}
	if VerifyPassword("broken", "reader123") {
		t.Error("broken hash accepted")
	}
	hash2, _ := HashPassword("reader123")
	if hash == hash2 {
		t.Error("salt should make hashes differ")
	}
}

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

func TestValidation(t *testing.T) {
	if err := ValidateUsername("ab"); err != nil {
		t.Errorf("valid username rejected: %v", err)
	}
	if err := ValidateUsername("a"); err == nil {
		t.Error("short username accepted")
	}
	if err := ValidateUsername("bad name"); err == nil {
		t.Error("username with space accepted")
	}
	if err := ValidateUsername("小明"); err != nil {
		t.Errorf("chinese username rejected: %v", err)
	}
	if err := ValidatePassword("123456"); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}
	if err := ValidatePassword("123"); err == nil {
		t.Error("short password accepted")
	}
}
