package auth

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("x")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	valid, err := VerifyPassword(encoded, "x")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !valid {
		t.Fatal("VerifyPassword() = false, want true")
	}
	valid, err = VerifyPassword(encoded, "incorrect password")
	if err != nil {
		t.Fatalf("VerifyPassword() wrong password error = %v", err)
	}
	if valid {
		t.Fatal("VerifyPassword() = true for wrong password")
	}
}

func TestPasswordValidation(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("HashPassword() error = nil for empty password")
	}
	if _, err := HashPassword(strings.Repeat("x", 1025)); err == nil {
		t.Fatal("HashPassword() error = nil for oversized password")
	}
	if _, err := VerifyPassword("invalid", "password"); err == nil {
		t.Fatal("VerifyPassword() error = nil for malformed hash")
	}
}
