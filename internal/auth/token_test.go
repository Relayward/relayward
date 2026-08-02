package auth

import (
	"bytes"
	"testing"
)

func TestNewToken(t *testing.T) {
	first, err := NewToken(32)
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	second, err := NewToken(32)
	if err != nil {
		t.Fatalf("NewToken() second error = %v", err)
	}
	if first == second || len(first) < 40 {
		t.Fatalf("generated tokens are not distinct with expected entropy")
	}
	if bytes.Equal(TokenHash(first), TokenHash(second)) {
		t.Fatal("token hashes are equal")
	}
}
