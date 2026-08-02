package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

func TestSetupSessionAndLogin(t *testing.T) {
	service, database, _ := newTestService(t)
	ctx := context.Background()

	credentials, err := service.Setup(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	authenticated, err := service.Authenticate(ctx, credentials.SessionToken)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if authenticated.Admin.Username != "admin" {
		t.Fatalf("administrator username = %q", authenticated.Admin.Username)
	}
	if !service.ValidateCSRF(authenticated, credentials.CSRFToken) || service.ValidateCSRF(authenticated, "wrong") {
		t.Fatal("CSRF validation returned an unexpected result")
	}
	if _, err := service.Setup(ctx, "other", "another secure administrator password"); !errors.Is(err, store.ErrAlreadyInitialized) {
		t.Fatalf("second Setup() error = %v, want ErrAlreadyInitialized", err)
	}
	if _, err := service.Login(ctx, "admin", "wrong password", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() wrong password error = %v", err)
	}
	login, err := service.Login(ctx, "ADMIN", "correct horse battery staple", "")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if _, _, err := database.SessionByTokenHash(ctx, TokenHash(login.SessionToken), service.now()); err != nil {
		t.Fatalf("stored login session error = %v", err)
	}
}

func TestTOTPAndRecoveryCodes(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, time.August, 2, 6, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	credentials, err := service.Setup(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	authenticated, err := service.Authenticate(ctx, credentials.SessionToken)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	secret, _, err := service.PrepareTOTP(ctx, authenticated)
	if err != nil {
		t.Fatalf("PrepareTOTP() error = %v", err)
	}
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		t.Fatalf("decode TOTP secret: %v", err)
	}
	initialCode := totpCode(key, fixed.Unix()/totpPeriod)
	recoveryCodes, err := service.EnableTOTP(ctx, initialCode)
	if err != nil {
		t.Fatalf("EnableTOTP() error = %v", err)
	}
	if len(recoveryCodes) != 10 {
		t.Fatalf("recovery code count = %d", len(recoveryCodes))
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", ""); !errors.Is(err, ErrSecondFactorRequired) {
		t.Fatalf("Login() without TOTP error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", initialCode); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Fatalf("Login() replayed setup code error = %v", err)
	}

	fixed = fixed.Add(30 * time.Second)
	loginCode := totpCode(key, fixed.Unix()/totpPeriod)
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", loginCode); err != nil {
		t.Fatalf("Login() with TOTP error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", loginCode); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Fatalf("Login() replayed TOTP error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", recoveryCodes[0]); err != nil {
		t.Fatalf("Login() with recovery code error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", recoveryCodes[0]); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Fatalf("Login() reused recovery code error = %v", err)
	}
}

func TestRecoveryCodeWorksWithoutInstanceKey(t *testing.T) {
	service, database, directory := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, time.August, 2, 6, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	credentials, err := service.Setup(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	authenticated, err := service.Authenticate(ctx, credentials.SessionToken)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	secret, _, err := service.PrepareTOTP(ctx, authenticated)
	if err != nil {
		t.Fatalf("PrepareTOTP() error = %v", err)
	}
	key, _ := decodeTOTPSecret(secret)
	codes, err := service.EnableTOTP(ctx, totpCode(key, fixed.Unix()/totpPeriod))
	if err != nil {
		t.Fatalf("EnableTOTP() error = %v", err)
	}

	keyPath := filepath.Join(directory, "secrets", "instance.key")
	if err := os.Rename(keyPath, keyPath+".lost"); err != nil {
		t.Fatalf("rename instance key: %v", err)
	}
	count, err := database.CountSecrets(ctx)
	if err != nil {
		t.Fatalf("CountSecrets() error = %v", err)
	}
	degraded, err := secretbox.Open(directory, count)
	if err != nil {
		t.Fatalf("secretbox.Open() error = %v", err)
	}
	degradedService, err := NewService(database, degraded)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	degradedService.now = func() time.Time { return fixed.Add(time.Minute) }
	if _, err := degradedService.Login(ctx, "admin", "correct horse battery staple", codes[0]); err != nil {
		t.Fatalf("Login() with recovery code and missing key error = %v", err)
	}
}

func TestRegenerateRecoveryCodesAndDisableTOTP(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, time.August, 2, 6, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	credentials, err := service.Setup(ctx, "admin", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	authenticated, err := service.Authenticate(ctx, credentials.SessionToken)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	secret, _, err := service.PrepareTOTP(ctx, authenticated)
	if err != nil {
		t.Fatalf("PrepareTOTP() error = %v", err)
	}
	key, _ := decodeTOTPSecret(secret)
	oldCodes, err := service.EnableTOTP(ctx, totpCode(key, fixed.Unix()/totpPeriod))
	if err != nil {
		t.Fatalf("EnableTOTP() error = %v", err)
	}

	newCodes, err := service.RegenerateRecoveryCodes(ctx, "correct horse battery staple", oldCodes[0])
	if err != nil {
		t.Fatalf("RegenerateRecoveryCodes() error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", oldCodes[1]); !errors.Is(err, ErrInvalidSecondFactor) {
		t.Fatalf("Login() with replaced recovery code error = %v", err)
	}
	if _, err := service.Login(ctx, "admin", "correct horse battery staple", newCodes[0]); err != nil {
		t.Fatalf("Login() with new recovery code error = %v", err)
	}

	fixed = fixed.Add(time.Minute)
	if err := service.DisableTOTP(ctx, "correct horse battery staple", totpCode(key, fixed.Unix()/totpPeriod)); err != nil {
		t.Fatalf("DisableTOTP() error = %v", err)
	}
	if _, err := service.Authenticate(ctx, credentials.SessionToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate() after disable error = %v", err)
	}
	administrator, err := service.store.AdministratorByID(ctx, 1)
	if err != nil {
		t.Fatalf("AdministratorByID() error = %v", err)
	}
	if administrator.TOTPEnabled {
		t.Fatal("TOTP remains enabled")
	}
}

func newTestService(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	directory := t.TempDir()
	database, err := store.Open(context.Background(), filepath.Join(directory, "relayward.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	secrets, err := secretbox.Open(directory, 0)
	if err != nil {
		t.Fatalf("secretbox.Open() error = %v", err)
	}
	service, err := NewService(database, secrets)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, database, directory
}
