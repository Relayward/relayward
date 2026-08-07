package management

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

func TestNodeLifecycleAndRegistrationToken(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	fixed := time.Date(2026, time.August, 2, 8, 30, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	created, err := service.CreateNode(ctx, NodeInput{Name: " Edge One ", Enabled: true})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("node ID = %q: %v", created.ID, err)
	}
	if created.Name != "Edge One" || !created.Enabled {
		t.Fatalf("created node = %+v", created)
	}
	if _, err := service.CreateNode(ctx, NodeInput{Name: "edge one", Enabled: true}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate CreateNode() error = %v", err)
	}

	updated, err := service.UpdateNode(ctx, created.ID, NodeInput{Name: "Edge Renamed", Enabled: false})
	if err != nil {
		t.Fatalf("UpdateNode() error = %v", err)
	}
	if updated.Name != "Edge Renamed" || updated.Enabled {
		t.Fatalf("updated node = %+v", updated)
	}
	token, err := service.CreateRegistrationToken(ctx, created.ID)
	if err != nil {
		t.Fatalf("CreateRegistrationToken() error = %v", err)
	}
	if !strings.HasPrefix(token.Token, "rwr_") || token.ExpiresAt != fixed.Add(registrationTokenLifetime) {
		t.Fatalf("registration token metadata = %+v", token)
	}
	if err := service.DeleteNode(ctx, created.ID); err != nil {
		t.Fatalf("DeleteNode() error = %v", err)
	}
	if _, err := service.Node(ctx, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Node() after delete error = %v", err)
	}
}

func TestUserLifecycleAndValidation(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	invalidEmail := "Admin <admin@example.com>"
	if _, err := service.CreateUser(ctx, UserInput{DisplayName: "Alice", Email: &invalidEmail}); fieldName(err) != "email" {
		t.Fatalf("CreateUser() invalid email error = %v", err)
	}
	email := "alice@example.com"
	telegram := "@alice"
	created, err := service.CreateUser(ctx, UserInput{DisplayName: " Alice ", Email: &email, Telegram: &telegram, Note: "line one\nline two"})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if created.DisplayName != "Alice" || created.Email == nil || *created.Email != email || created.Note != "line one\nline two" {
		t.Fatalf("created user = %+v", created)
	}
	if _, err := service.CreateUser(ctx, UserInput{DisplayName: "ALICE"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate CreateUser() error = %v", err)
	}
	empty := " "
	updated, err := service.UpdateUser(ctx, created.ID, UserInput{DisplayName: "Alice Updated", Email: &empty})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.Email != nil || updated.DisplayName != "Alice Updated" {
		t.Fatalf("updated user = %+v", updated)
	}
	if err := service.DeleteUser(ctx, created.ID); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
}

func newTestService(t *testing.T) *Service {
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
	return NewService(database, secrets)
}

func fieldName(err error) string {
	var fieldError *FieldError
	if errors.As(err, &fieldError) {
		return fieldError.Field
	}
	return ""
}
