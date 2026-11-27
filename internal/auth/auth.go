// Package auth 提供会话令牌生成与摘要工具。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

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
