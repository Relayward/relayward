package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

var (
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrSecondFactorRequired = errors.New("second factor is required")
	ErrInvalidSecondFactor  = errors.New("invalid second factor")
	ErrSecretsUnavailable   = errors.New("encrypted secrets are unavailable")
	ErrTOTPAlreadyEnabled   = errors.New("TOTP is already enabled")
	ErrInvalidUsername      = errors.New("invalid username")
	usernamePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)
)

const sessionLifetime = 24 * time.Hour

type Service struct {
	store             *store.Store
	secrets           *secretbox.Manager
	now               func() time.Time
	dummyPasswordHash string
}

type Credentials struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
	Admin        store.Administrator
}

type Authenticated struct {
	Session   store.Session
	Admin     store.Administrator
	TokenHash []byte
}

func NewService(database *store.Store, secrets *secretbox.Manager) (*Service, error) {
	dummyHash, err := HashPassword("relayward invalid credential sentinel")
	if err != nil {
		return nil, fmt.Errorf("create password verification sentinel: %w", err)
	}
	return &Service{store: database, secrets: secrets, now: time.Now, dummyPasswordHash: dummyHash}, nil
}

func (service *Service) Setup(ctx context.Context, username, password string) (Credentials, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return Credentials{}, fmt.Errorf("%w: must contain 3 to 64 letters, numbers, dots, underscores, or hyphens", ErrInvalidUsername)
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return Credentials{}, err
	}
	now := service.now().UTC()
	administrator, err := service.store.InitializeAdministrator(ctx, username, passwordHash, now)
	if err != nil {
		return Credentials{}, err
	}
	return service.createSession(ctx, administrator, "administrator.setup", now)
}

func (service *Service) Login(ctx context.Context, username, password, secondFactor string) (Credentials, error) {
	now := service.now().UTC()
	administrator, err := service.store.AdministratorByUsername(ctx, strings.TrimSpace(username))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Credentials{}, err
	}
	passwordHash := service.dummyPasswordHash
	if err == nil {
		passwordHash = administrator.PasswordHash
	}
	validPassword, verifyErr := VerifyPassword(passwordHash, password)
	if verifyErr != nil {
		return Credentials{}, fmt.Errorf("verify password: %w", verifyErr)
	}
	if err != nil || !validPassword {
		if auditErr := service.auditLogin(ctx, "failure", now); auditErr != nil {
			return Credentials{}, auditErr
		}
		return Credentials{}, ErrInvalidCredentials
	}
	if administrator.TOTPEnabled {
		if strings.TrimSpace(secondFactor) == "" {
			return Credentials{}, ErrSecondFactorRequired
		}
		valid, secondFactorErr := service.verifySecondFactor(ctx, secondFactor, now)
		if secondFactorErr != nil {
			return Credentials{}, secondFactorErr
		}
		if !valid {
			if auditErr := service.auditLogin(ctx, "failure", now); auditErr != nil {
				return Credentials{}, auditErr
			}
			return Credentials{}, ErrInvalidSecondFactor
		}
	}
	return service.createSession(ctx, administrator, "administrator.login", now)
}

func (service *Service) verifySecondFactor(ctx context.Context, value string, now time.Time) (bool, error) {
	recovery, err := service.store.ConsumeRecoveryCode(ctx, RecoveryCodeHash(value), now)
	if err != nil {
		return false, err
	}
	if recovery {
		return true, nil
	}
	ciphertext, err := service.store.Secret(ctx, "administrator", "1", "totp")
	if err != nil {
		return false, err
	}
	plaintext, err := service.secrets.Decrypt("administrator", "1", "totp", ciphertext)
	if errors.Is(err, secretbox.ErrUnavailable) {
		return false, ErrSecretsUnavailable
	}
	if err != nil {
		return false, err
	}
	counter, valid := MatchTOTP(string(plaintext), strings.TrimSpace(value), now)
	if !valid {
		return false, nil
	}
	consumed, err := service.store.ConsumeTOTPCounter(ctx, counter, now)
	if err != nil {
		return false, err
	}
	return consumed, nil
}

func (service *Service) Authenticate(ctx context.Context, sessionToken string) (Authenticated, error) {
	if sessionToken == "" {
		return Authenticated{}, ErrInvalidCredentials
	}
	now := service.now().UTC()
	tokenHash := TokenHash(sessionToken)
	session, administrator, err := service.store.SessionByTokenHash(ctx, tokenHash, now)
	if errors.Is(err, store.ErrNotFound) {
		return Authenticated{}, ErrInvalidCredentials
	}
	if err != nil {
		return Authenticated{}, err
	}
	if now.Sub(session.LastSeenAt) >= time.Minute {
		if err := service.store.TouchSession(ctx, tokenHash, now); err != nil {
			return Authenticated{}, err
		}
		session.LastSeenAt = now
	}
	return Authenticated{Session: session, Admin: administrator, TokenHash: tokenHash}, nil
}

func (service *Service) ValidateCSRF(authenticated Authenticated, token string) bool {
	actual := TokenHash(token)
	return subtle.ConstantTimeCompare(actual, authenticated.Session.CSRFHash) == 1
}

func (service *Service) Logout(ctx context.Context, authenticated Authenticated) error {
	now := service.now().UTC()
	return service.store.DeleteSessionWithAudit(ctx, authenticated.TokenHash, store.AuditEntry{
		OccurredAt: now,
		ActorType:  "administrator",
		ActorID:    "1",
		Action:     "administrator.logout",
		TargetType: "session",
		Outcome:    "success",
	})
}

func (service *Service) PrepareTOTP(ctx context.Context, authenticated Authenticated) (string, string, error) {
	if authenticated.Admin.TOTPEnabled {
		return "", "", ErrTOTPAlreadyEnabled
	}
	if !service.secrets.Available() {
		return "", "", ErrSecretsUnavailable
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return "", "", err
	}
	ciphertext, err := service.secrets.Encrypt("administrator", "1", "totp_pending", []byte(secret))
	if err != nil {
		return "", "", err
	}
	if err := service.store.PutSecret(ctx, "administrator", "1", "totp_pending", ciphertext, service.now().UTC()); err != nil {
		return "", "", err
	}
	uri, err := TOTPURI(secret, authenticated.Admin.Username)
	if err != nil {
		return "", "", err
	}
	return secret, uri, nil
}

func (service *Service) EnableTOTP(ctx context.Context, code string) ([]string, error) {
	if !service.secrets.Available() {
		return nil, ErrSecretsUnavailable
	}
	pending, err := service.store.Secret(ctx, "administrator", "1", "totp_pending")
	if err != nil {
		return nil, err
	}
	secret, err := service.secrets.Decrypt("administrator", "1", "totp_pending", pending)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	counter, valid := MatchTOTP(string(secret), strings.TrimSpace(code), now)
	if !valid {
		return nil, ErrInvalidSecondFactor
	}
	ciphertext, err := service.secrets.Encrypt("administrator", "1", "totp", secret)
	if err != nil {
		return nil, err
	}
	codes, err := NewRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	hashes := make([][]byte, len(codes))
	for index, recoveryCode := range codes {
		hashes[index] = RecoveryCodeHash(recoveryCode)
	}
	if err := service.store.EnableTOTP(ctx, ciphertext, hashes, counter, now); err != nil {
		if errors.Is(err, store.ErrStateConflict) {
			return nil, ErrTOTPAlreadyEnabled
		}
		return nil, err
	}
	return codes, nil
}

func (service *Service) DisableTOTP(ctx context.Context, password, secondFactor string) error {
	administrator, err := service.store.AdministratorByID(ctx, 1)
	if err != nil {
		return err
	}
	if err := service.verifySensitiveAction(ctx, administrator, password, secondFactor); err != nil {
		return err
	}
	return service.store.ResetTOTP(ctx, "administrator", service.now().UTC())
}

func (service *Service) RegenerateRecoveryCodes(ctx context.Context, password, secondFactor string) ([]string, error) {
	administrator, err := service.store.AdministratorByID(ctx, 1)
	if err != nil {
		return nil, err
	}
	if !administrator.TOTPEnabled {
		return nil, ErrInvalidSecondFactor
	}
	if err := service.verifySensitiveAction(ctx, administrator, password, secondFactor); err != nil {
		return nil, err
	}
	codes, err := NewRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	hashes := make([][]byte, len(codes))
	for index, code := range codes {
		hashes[index] = RecoveryCodeHash(code)
	}
	if err := service.store.ReplaceRecoveryCodes(ctx, hashes, service.now().UTC()); err != nil {
		return nil, err
	}
	return codes, nil
}

func (service *Service) verifySensitiveAction(ctx context.Context, administrator store.Administrator, password, secondFactor string) error {
	validPassword, err := VerifyPassword(administrator.PasswordHash, password)
	if err != nil {
		return err
	}
	if !validPassword {
		return ErrInvalidCredentials
	}
	if administrator.TOTPEnabled {
		if strings.TrimSpace(secondFactor) == "" {
			return ErrSecondFactorRequired
		}
		valid, err := service.verifySecondFactor(ctx, secondFactor, service.now().UTC())
		if err != nil {
			return err
		}
		if !valid {
			return ErrInvalidSecondFactor
		}
	}
	return nil
}

func (service *Service) createSession(ctx context.Context, administrator store.Administrator, action string, now time.Time) (Credentials, error) {
	sessionToken, err := NewToken(32)
	if err != nil {
		return Credentials{}, err
	}
	csrfToken, err := NewToken(32)
	if err != nil {
		return Credentials{}, err
	}
	expiresAt := now.Add(sessionLifetime)
	session := store.Session{
		TokenHash:       TokenHash(sessionToken),
		CSRFHash:        TokenHash(csrfToken),
		AdministratorID: administrator.ID,
		CreatedAt:       now,
		LastSeenAt:      now,
		ExpiresAt:       expiresAt,
	}
	if err := service.store.CreateSessionWithAudit(ctx, session, store.AuditEntry{
		OccurredAt: now,
		ActorType:  "administrator",
		ActorID:    "1",
		Action:     action,
		TargetType: "session",
		Outcome:    "success",
	}); err != nil {
		return Credentials{}, err
	}
	return Credentials{SessionToken: sessionToken, CSRFToken: csrfToken, ExpiresAt: expiresAt, Admin: administrator}, nil
}

func (service *Service) auditLogin(ctx context.Context, outcome string, now time.Time) error {
	return service.store.AppendAudit(ctx, store.AuditEntry{
		OccurredAt: now,
		ActorType:  "anonymous",
		Action:     "administrator.login",
		TargetType: "administrator",
		Outcome:    outcome,
	})
}
