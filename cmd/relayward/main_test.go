package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/secretbox"
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
	if err := runAdmin([]string{"reset-totp", "-data", directory}, strings.NewReader(""), &stdout, &stderr); err != nil {
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

func TestRunAdminResetPasswordFromStdin(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "relayward.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	oldHash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.InitializeAdministrator(context.Background(), "admin", oldHash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAdmin([]string{"reset-password", "-data", directory, "-password-stdin"},
		strings.NewReader("replacement administrator password\n"), &stdout, &stderr); err != nil {
		t.Fatalf("runAdmin(reset-password) error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "sessions were revoked") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	database, err = store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	administrator, err := database.AdministratorByID(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := auth.VerifyPassword(administrator.PasswordHash, "replacement administrator password")
	if err != nil || !valid {
		t.Fatalf("replacement password valid = %t, %v", valid, err)
	}
}

func TestRunAdminRecoverSecretsReplacesWrongKeyAndDiscardsCiphertext(t *testing.T) {
	directory := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := database.InitializeAdministrator(context.Background(), "admin", "hash", now); err != nil {
		t.Fatal(err)
	}
	manager, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := manager.Encrypt("plugin_installation", "io.relayward.test", "github_token", []byte("private-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PutSecret(context.Background(), "plugin_installation", "io.relayward.test", "github_token", ciphertext, now); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "secrets", "instance.key"), bytes.Repeat([]byte{0x3c}, 32), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAdmin([]string{
		"recover-secrets", "-data", directory, "-confirm-discard-encrypted-secrets",
	}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runAdmin(recover-secrets) error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "discarded 1 encrypted secrets") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	database, err = store.Open(context.Background(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if count, err := database.CountSecrets(context.Background()); err != nil || count != 0 {
		t.Fatalf("secret count = %d, %v", count, err)
	}
	replacement, err := secretbox.Open(directory, 0)
	if err != nil || !replacement.Available() {
		t.Fatalf("replacement key status = %v, %v", replacement.Status(), err)
	}
	if err := runAdmin([]string{
		"recover-secrets", "-data", directory, "-confirm-discard-encrypted-secrets",
	}, strings.NewReader(""), &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "instance key is available") {
		t.Fatalf("second recover-secrets error = %v", err)
	}
}

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := runHealthcheck([]string{"-url", server.URL}); err != nil {
		t.Fatalf("runHealthcheck() error = %v", err)
	}

	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	if err := runHealthcheck([]string{"-url", unavailable.URL}); err == nil {
		t.Fatal("runHealthcheck() accepted an unavailable endpoint")
	}
}
