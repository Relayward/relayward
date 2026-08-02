package secretbox

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndPersistence(t *testing.T) {
	directory := t.TempDir()
	first, err := Open(directory, 0)
	if err != nil {
		t.Fatalf("Open() first error = %v", err)
	}
	ciphertext, err := first.Encrypt("administrator", "1", "totp", []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	second, err := Open(directory, 1)
	if err != nil {
		t.Fatalf("Open() second error = %v", err)
	}
	plaintext, err := second.Decrypt("administrator", "1", "totp", ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if got := string(plaintext); got != "secret" {
		t.Fatalf("plaintext = %q", got)
	}

	info, err := os.Stat(filepath.Join(directory, "secrets", "instance.key"))
	if err != nil {
		t.Fatalf("stat instance key: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("instance key permissions = %o, want 600", permissions)
	}
}

func TestDecryptRejectsWrongContext(t *testing.T) {
	manager, err := Open(t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ciphertext, err := manager.Encrypt("administrator", "1", "totp", []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if _, err := manager.Decrypt("administrator", "2", "totp", ciphertext); err == nil {
		t.Fatal("Decrypt() error = nil, want authentication error")
	}
}

func TestMissingKeyWithExistingSecretsIsDegraded(t *testing.T) {
	manager, err := Open(t.TempDir(), 1)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if manager.Available() {
		t.Fatal("Available() = true, want degraded manager")
	}
	if !errors.Is(manager.Status(), ErrUnavailable) {
		t.Fatalf("Status() = %v, want ErrUnavailable", manager.Status())
	}
	if _, err := manager.Encrypt("administrator", "1", "totp", []byte("secret")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Encrypt() error = %v, want ErrUnavailable", err)
	}
}

func TestInvalidKeyIsDegraded(t *testing.T) {
	directory := t.TempDir()
	secretDirectory := filepath.Join(directory, "secrets")
	if err := os.Mkdir(secretDirectory, 0o700); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretDirectory, "instance.key"), []byte("short"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	manager, err := Open(directory, 1)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if manager.Available() || !errors.Is(manager.Status(), ErrUnavailable) {
		t.Fatalf("manager status = %v, available = %v", manager.Status(), manager.Available())
	}
}

func TestVerifyDetectsAWellFormedWrongKey(t *testing.T) {
	directory := t.TempDir()
	first, err := Open(directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := first.Encrypt("plugin_installation", "io.relayward.test", "github_token", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "secrets", "instance.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x5a}, keySize), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Open(directory, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Verify("plugin_installation", "io.relayward.test", "github_token", ciphertext); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Verify() error = %v, want ErrUnavailable", err)
	}
	if second.Available() {
		t.Fatal("Available() = true after ciphertext verification failure")
	}
}
