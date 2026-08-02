package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPVerification(t *testing.T) {
	secret := base32NoPadding.EncodeToString([]byte("12345678901234567890"))
	now := time.Unix(59, 0)
	code := totpCode([]byte("12345678901234567890"), now.Unix()/totpPeriod)
	if !VerifyTOTP(secret, code, now) {
		t.Fatal("VerifyTOTP() = false for valid code")
	}
	if VerifyTOTP(secret, "000000", now) {
		t.Fatal("VerifyTOTP() = true for invalid code")
	}
}

func TestTOTPURI(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("NewTOTPSecret() error = %v", err)
	}
	uri, err := TOTPURI(secret, "admin@example.com")
	if err != nil {
		t.Fatalf("TOTPURI() error = %v", err)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, "issuer=Relayward") {
		t.Fatalf("TOTP URI = %q", uri)
	}
}
