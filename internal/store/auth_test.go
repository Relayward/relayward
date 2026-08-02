package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestInitializeAdministratorOnlySucceedsOnce(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	start := make(chan struct{})
	errorsByCall := make([]error, 2)
	var wait sync.WaitGroup
	for index := range errorsByCall {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByCall[index] = database.InitializeAdministrator(context.Background(), "admin", "hash", time.Now())
		}(index)
	}
	close(start)
	wait.Wait()

	successes := 0
	alreadyInitialized := 0
	for _, callErr := range errorsByCall {
		switch {
		case callErr == nil:
			successes++
		case errors.Is(callErr, ErrAlreadyInitialized):
			alreadyInitialized++
		default:
			t.Fatalf("InitializeAdministrator() unexpected error = %v", callErr)
		}
	}
	if successes != 1 || alreadyInitialized != 1 {
		t.Fatalf("successes = %d, already initialized = %d", successes, alreadyInitialized)
	}
}

func TestEnableTOTPRollsBackAllStateOnFailure(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if _, err := database.InitializeAdministrator(ctx, "admin", "hash", now); err != nil {
		t.Fatalf("InitializeAdministrator() error = %v", err)
	}
	if err := database.PutSecret(ctx, "administrator", "1", "totp_pending", []byte("pending"), now); err != nil {
		t.Fatalf("PutSecret() error = %v", err)
	}

	err = database.EnableTOTP(ctx, []byte("final"), [][]byte{{1}}, 1, now)
	if err == nil {
		t.Fatal("EnableTOTP() error = nil, want recovery hash constraint error")
	}
	administrator, err := database.AdministratorByID(ctx, 1)
	if err != nil {
		t.Fatalf("AdministratorByID() error = %v", err)
	}
	if administrator.TOTPEnabled {
		t.Fatal("TOTP enabled after failed transaction")
	}
	if _, err := database.Secret(ctx, "administrator", "1", "totp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("final Secret() error = %v, want ErrNotFound", err)
	}
	if pending, err := database.Secret(ctx, "administrator", "1", "totp_pending"); err != nil || string(pending) != "pending" {
		t.Fatalf("pending Secret() = %q, %v", pending, err)
	}
}
