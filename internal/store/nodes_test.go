package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRegistrationTokenReplacesPreviousUnusedToken(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Date(2026, time.August, 2, 9, 0, 0, 0, time.UTC)
	if err := database.CreateNode(ctx, Node{ID: "node-id", Name: "node", Enabled: true}, now); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	first := make([]byte, 32)
	second := make([]byte, 32)
	second[0] = 1
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", first, now.Add(time.Minute), now); err != nil {
		t.Fatalf("first CreateNodeRegistrationToken() error = %v", err)
	}
	if err := database.CreateNodeRegistrationToken(ctx, "node-id", second, now.Add(2*time.Minute), now.Add(time.Second)); err != nil {
		t.Fatalf("second CreateNodeRegistrationToken() error = %v", err)
	}

	var active, invalidated int
	if err := database.db.QueryRowContext(ctx, `
SELECT count(*) FILTER (WHERE used_at IS NULL), count(*) FILTER (WHERE used_at IS NOT NULL)
FROM node_registration_tokens WHERE node_id = 'node-id'`).Scan(&active, &invalidated); err != nil {
		t.Fatalf("query registration tokens: %v", err)
	}
	if active != 1 || invalidated != 1 {
		t.Fatalf("active tokens = %d, invalidated tokens = %d", active, invalidated)
	}
}
