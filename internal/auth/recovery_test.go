package auth

import (
	"bytes"
	"testing"
)

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(10)
	if err != nil {
		t.Fatalf("NewRecoveryCodes() error = %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("code count = %d", len(codes))
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if len(code) != 19 {
			t.Fatalf("code length = %d for %q", len(code), code)
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("duplicate recovery code %q", code)
		}
		seen[code] = struct{}{}
	}
	if !bytes.Equal(RecoveryCodeHash(codes[0]), RecoveryCodeHash(codes[0])) {
		t.Fatal("recovery code hash is not deterministic")
	}
}
