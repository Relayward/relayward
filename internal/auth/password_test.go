package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	valid, err := VerifyPassword(encoded, "correct horse battery staple")
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
	if _, err := HashPassword("too short"); err == nil {
		t.Fatal("HashPassword() error = nil for short password")
	}
	if _, err := VerifyPassword("invalid", "password"); err == nil {
		t.Fatal("VerifyPassword() error = nil for malformed hash")
	}
}
