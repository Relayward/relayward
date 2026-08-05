package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigratesDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relayward.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() first error = %v", err)
	}
	if err := first.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() second error = %v", err)
	}
	defer second.Close()

	var count int
	if err := second.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if count != len(migrations) {
		t.Fatalf("migration count = %d, want %d", count, len(migrations))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("database permissions = %o, want 600", permissions)
	}
}

func TestAuditOutcomeNormalizationMigration(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "relayward.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	now := time.Now().UTC()
	for _, outcome := range []string{"succeeded", "failed"} {
		if err := database.AppendAudit(ctx, AuditEntry{
			OccurredAt: now, ActorType: "agent", Action: "node.command.complete",
			TargetType: "agent_command", Outcome: outcome,
		}); err != nil {
			t.Fatalf("AppendAudit(%q) error = %v", outcome, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, migrations[len(migrations)-1].sql); err != nil {
		t.Fatalf("normalize audit outcomes: %v", err)
	}
	entries, err := database.ListAudit(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Outcome != "failure" || entries[1].Outcome != "success" {
		t.Fatalf("normalized audit outcomes = %+v", entries)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("Open() error = nil, want path error")
	}
}
