package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relayward/relayward/internal/store"
)

func TestRunAdminResetTOTP(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "relayward.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	now := time.Now().UTC()
	if _, err := database.InitializeAdministrator(context.Background(), "admin", "hash", now); err != nil {
		t.Fatalf("InitializeAdministrator() error = %v", err)
	}
	if err := database.EnableTOTP(context.Background(), []byte("ciphertext"), [][]byte{make([]byte, 32)}, 1, now); err != nil {
		t.Fatalf("EnableTOTP() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("database.Close() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAdmin([]string{"reset-totp", "-data", directory}, &stdout, &stderr); err != nil {
		t.Fatalf("runAdmin() error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sessions were revoked") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	database, err = store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open() after reset error = %v", err)
	}
	defer database.Close()
	administrator, err := database.AdministratorByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("AdministratorByID() error = %v", err)
	}
	if administrator.TOTPEnabled {
		t.Fatal("TOTP remains enabled after reset")
	}
	if _, err := database.Secret(context.Background(), "administrator", "1", "totp"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Secret() error = %v, want ErrNotFound", err)
	}
}
