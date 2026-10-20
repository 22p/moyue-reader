// Package auth 提供密码哈希与会话令牌工具。
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	iterations = 210_000
	keyLength  = 32
	saltLength = 16
)

// HashPassword 使用 PBKDF2-HMAC-SHA256 生成可存储的密码摘要。
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLength)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword 校验明文密码是否匹配存储的摘要。
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 || iter > 5_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewToken 生成会话令牌明文（只下发给客户端一次）。
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// TokenHash 返回令牌的 SHA-256，用于服务端存储。
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ValidatePassword 检查密码长度等基本规则。
func ValidatePassword(password string) error {
	if len([]rune(password)) < 6 {
		return errors.New("密码至少需要 6 个字符")
	}
	if len(password) > 256 {
		return errors.New("密码过长")
	}
	return nil
}

// ValidateUsername 检查用户名规则。
func ValidateUsername(username string) error {
	n := len([]rune(username))
	if n < 2 || n > 24 {
		return errors.New("用户名长度需在 2-24 个字符之间")
	}
	for _, r := range username {
		ok := r == '_' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || (r >= 0x4e00 && r <= 0x9fa5)
		if !ok {
			return errors.New("用户名只能包含中英文、数字、下划线和短横线")
		}
	}
	return nil
}
