package policycoordinator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	"github.com/google/uuid"

	"github.com/Relayward/relayward/internal/store"
)

const (
	defaultInterval       = 15 * time.Second
	policyCommandLifetime = 30 * time.Minute
)

type Coordinator struct {
	store    *store.Store
	logger   *slog.Logger
	interval time.Duration
	now      func() time.Time
}

func New(database *store.Store, logger *slog.Logger) (*Coordinator, error) {
	if database == nil {
		return nil, errors.New("policy coordinator database is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Coordinator{
		store: database, logger: logger, interval: defaultInterval,
		now: func() time.Time { return time.Now().UTC().Truncate(time.Second) },
	}, nil
}

func (coordinator *Coordinator) Run(ctx context.Context) error {
	coordinator.runCycle(ctx)
	ticker := time.NewTicker(coordinator.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			coordinator.runCycle(ctx)
		}
	}
}

func (coordinator *Coordinator) ReconcileNode(ctx context.Context, nodeID string) (bool, error) {
	now := coordinator.now()
	snapshot, err := coordinator.store.BuildNodePolicySnapshot(ctx, nodeID, now)
	if err != nil {
		return false, err
	}
	if snapshot.AgentStartedAt == nil {
		return false, nil
	}
	for _, capability := range []string{
		agentv1.CapabilityControlCommands,
		agentv1.CapabilityEventQueue,
		agentv1.CapabilityPolicyEnforcement,
	} {
		if !hasCapability(snapshot.AgentCapabilities, capability) {
			message := fmt.Sprintf("Agent does not support required capability %s", capability)
			return false, coordinator.store.MarkPolicyUnsupported(ctx, nodeID, message, now)
		}
	}
	_, created, err := coordinator.store.StagePolicyReconcile(
		ctx, snapshot, uuid.NewString(), now, policyCommandLifetime,
	)
	return created, err
}

func (coordinator *Coordinator) runCycle(ctx context.Context) {
	nodes, err := coordinator.store.ListNodes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			coordinator.logger.Error("policy coordinator could not list nodes", "error", err)
		}
		return
	}
	for _, node := range nodes {
		if _, err := coordinator.ReconcileNode(ctx, node.ID); err != nil && ctx.Err() == nil {
			coordinator.logger.Warn("policy reconciliation failed", "node_id", node.ID, "error", err)
		}
	}
}

func hasCapability(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
