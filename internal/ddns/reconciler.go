package ddns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/store"
)

const defaultReconcileInterval = time.Minute

type Reconciler struct {
	store    *store.Store
	secrets  *secretbox.Manager
	provider DNSProvider
	logger   *slog.Logger
	interval time.Duration
	now      func() time.Time
}

func New(database *store.Store, secrets *secretbox.Manager, logger *slog.Logger) (*Reconciler, error) {
	if database == nil || secrets == nil {
		return nil, errors.New("business store and secret manager are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{store: database, secrets: secrets, provider: newCloudflareProvider(), logger: logger,
		interval: defaultReconcileInterval, now: func() time.Time { return time.Now().UTC().Truncate(time.Second) }}, nil
}

func (reconciler *Reconciler) Run(ctx context.Context) error {
	reconciler.runCycle(ctx)
	ticker := time.NewTicker(reconciler.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			reconciler.runCycle(ctx)
		}
	}
}

func (reconciler *Reconciler) RunOnce(ctx context.Context) error {
	values, err := reconciler.store.ListManagedDDNSEndpoints(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, value := range values {
		if err := reconciler.reconcile(ctx, value); err != nil {
			result = errors.Join(result, fmt.Errorf("endpoint %s: %w", value.Endpoint.ID, err))
		}
	}
	return result
}

func (reconciler *Reconciler) runCycle(ctx context.Context) {
	if err := reconciler.RunOnce(ctx); err != nil && ctx.Err() == nil {
		reconciler.logger.Warn("DDNS reconciliation failed", "error", err)
	}
}

func (reconciler *Reconciler) reconcile(ctx context.Context, value store.ManagedDDNSEndpoint) error {
	now := reconciler.now()
	if value.Desired == nil {
		problem := "Waiting for the node to report a public " + value.Endpoint.SourceFamily + " address."
		return reconciler.store.RecordDDNSSync(ctx, value.Endpoint.ID, "pending", value.Endpoint.ActualAddress, problem, time.Time{}, now)
	}
	if value.Endpoint.SyncStatus == "synced" && value.Endpoint.ActualAddress == value.Desired.Address {
		return nil
	}
	ciphertext, err := reconciler.store.Secret(ctx, store.DNSProviderSecretOwnerType, value.Provider.ID, store.DNSProviderTokenSecret)
	if err != nil {
		return reconciler.fail(ctx, value.Endpoint, "DNS provider token is unavailable.", err, now)
	}
	token, err := reconciler.secrets.Decrypt(store.DNSProviderSecretOwnerType, value.Provider.ID, store.DNSProviderTokenSecret, ciphertext)
	if err != nil {
		return reconciler.fail(ctx, value.Endpoint, "DNS provider token cannot be decrypted.", err, now)
	}
	if err := reconciler.provider.Sync(ctx, string(token), value.Endpoint, value.Desired.Address); err != nil {
		return reconciler.fail(ctx, value.Endpoint, err.Error(), err, now)
	}
	if err := reconciler.store.RecordDDNSSync(ctx, value.Endpoint.ID, "synced", value.Desired.Address, "", now, now); err != nil {
		return err
	}
	return reconciler.store.AppendSystemAudit(ctx, store.AuditEntry{
		OccurredAt: now, ActorType: "system", Action: "node_endpoint.ddns.sync", TargetType: "node_endpoint",
		TargetID: value.Endpoint.ID, Outcome: "success", Metadata: map[string]any{
			"node_id": value.Endpoint.NodeID, "provider": value.Provider.Provider, "family": value.Endpoint.SourceFamily,
		},
	})
}

func (reconciler *Reconciler) fail(ctx context.Context, endpoint store.NodeEndpoint, message string, cause error, now time.Time) error {
	if recordErr := reconciler.store.RecordDDNSSync(ctx, endpoint.ID, "failed", endpoint.ActualAddress, message, time.Time{}, now); recordErr != nil {
		return errors.Join(cause, recordErr)
	}
	return cause
}
