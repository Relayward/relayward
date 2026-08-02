package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewToken(bytes int) (string, error) {
	if bytes < 16 || bytes > 128 {
		return "", fmt.Errorf("token entropy must be between 16 and 128 bytes")
	}
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func TokenHash(token string) []byte {
	value := sha256.Sum256([]byte(token))
	return value[:]
}
