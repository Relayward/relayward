package eventprocessor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	agentv1 "github.com/Relayward/relayward-sdk/agent/v1"
	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"

	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/store"
)

const (
	TrafficConsumerID = "standard.traffic.v1"
	PolicyConsumerID  = "standard.policy-status.v1"
	AccessConsumerID  = "standard.access.v1"

	defaultInterval        = 5 * time.Second
	defaultBatchSize       = 200
	maximumBatchesPerCycle = 16
	consumerRetryDelay     = 30 * time.Second
)

var ConsumerIDs = []string{TrafficConsumerID, PolicyConsumerID, AccessConsumerID}

type Processor struct {
	events   *eventstore.Store
	business *store.Store
	features FeatureConsumers
	logger   *slog.Logger
	interval time.Duration
	now      func() time.Time
}

type FeatureConsumers interface {
	FeatureConsumerIDs(context.Context) ([]string, error)
	ConsumeFeatureEvents(context.Context, string, []eventstore.StoredEvent) error
}

func New(events *eventstore.Store, business *store.Store, features FeatureConsumers, logger *slog.Logger) (*Processor, error) {
	if events == nil || business == nil {
		return nil, errors.New("event and business stores are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Processor{
		events: events, business: business, features: features, logger: logger, interval: defaultInterval,
		now: func() time.Time { return time.Now().UTC().Truncate(time.Second) },
	}, nil
}

func (processor *Processor) Run(ctx context.Context) error {
	processor.runCycle(ctx)
	ticker := time.NewTicker(processor.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			processor.runCycle(ctx)
		}
	}
}

func (processor *Processor) RunOnce(ctx context.Context) error {
	var result error
	consumerIDs, err := processor.consumerIDs(ctx)
	if err != nil {
		return err
	}
	for _, consumerID := range consumerIDs {
		if err := processor.runConsumer(ctx, consumerID); err != nil {
			result = errors.Join(result, fmt.Errorf("consumer %s: %w", consumerID, err))
		}
	}
	return result
}

func (processor *Processor) runCycle(ctx context.Context) {
	if err := processor.RunOnce(ctx); err != nil && ctx.Err() == nil {
		processor.logger.Warn("event processing failed", "error", err)
	}
}

func (processor *Processor) runConsumer(ctx context.Context, consumerID string) error {
	if !isStandardConsumer(consumerID) {
		return processor.runFeatureConsumer(ctx, consumerID)
	}
	now := processor.now()
	if err := processor.events.EnsureConsumer(ctx, consumerID, now); err != nil {
		return err
	}
	state, err := processor.events.ConsumerState(ctx, consumerID)
	if err != nil {
		return err
	}
	if state.RetryAfter != nil && state.RetryAfter.After(now) {
		return nil
	}
	for batchIndex := 0; batchIndex < maximumBatchesPerCycle; batchIndex++ {
		batch, err := processor.events.ReadConsumerBatch(ctx, consumerID, defaultBatchSize)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, event := range batch {
			if err := processor.processEvent(ctx, consumerID, event); err != nil {
				now = processor.now()
				if recordErr := processor.events.RecordConsumerFailure(
					ctx, consumerID, event.RowID, err, now.Add(consumerRetryDelay), now,
				); recordErr != nil {
					return errors.Join(err, recordErr)
				}
				return fmt.Errorf("process event row %d kind %s: %w", event.RowID, event.Event.Kind, err)
			}
			if err := processor.events.AdvanceConsumer(ctx, consumerID, event.RowID, processor.now()); err != nil {
				return err
			}
		}
		if len(batch) < defaultBatchSize {
			return nil
		}
	}
	return errors.New("consumer cycle batch limit reached")
}

func (processor *Processor) runFeatureConsumer(ctx context.Context, consumerID string) error {
	if processor.features == nil {
		return errors.New("feature event consumer is not configured")
	}
	now := processor.now()
	if err := processor.events.EnsureConsumer(ctx, consumerID, now); err != nil {
		return err
	}
	state, err := processor.events.ConsumerState(ctx, consumerID)
	if err != nil {
		return err
	}
	if state.RetryAfter != nil && state.RetryAfter.After(now) {
		return nil
	}
	for batchIndex := 0; batchIndex < maximumBatchesPerCycle; batchIndex++ {
		batch, err := processor.events.ReadConsumerBatch(ctx, consumerID, centerpluginv1.MaximumEventBatchEvents)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		delivery := featureBatchPrefix(batch)
		if err := processor.features.ConsumeFeatureEvents(ctx, consumerID, delivery); err != nil {
			now = processor.now()
			if recordErr := processor.events.RecordConsumerFailure(
				ctx, consumerID, delivery[0].RowID, err, now.Add(consumerRetryDelay), now,
			); recordErr != nil {
				return errors.Join(err, recordErr)
			}
			return fmt.Errorf("consume event batch starting at row %d: %w", delivery[0].RowID, err)
		}
		if err := processor.events.AdvanceConsumer(ctx, consumerID, delivery[len(delivery)-1].RowID, processor.now()); err != nil {
			return err
		}
		if len(delivery) == len(batch) && len(batch) < centerpluginv1.MaximumEventBatchEvents {
			return nil
		}
	}
	return errors.New("consumer cycle batch limit reached")
}

func featureBatchPrefix(batch []eventstore.StoredEvent) []eventstore.StoredEvent {
	totalBytes := 0
	for index, event := range batch {
		if index > 0 && totalBytes+len(event.Event.Payload) > centerpluginv1.MaximumEventBatchBytes {
			return batch[:index]
		}
		totalBytes += len(event.Event.Payload)
	}
	return batch
}

func (processor *Processor) consumerIDs(ctx context.Context) ([]string, error) {
	values := append([]string(nil), ConsumerIDs...)
	if processor.features == nil {
		return values, nil
	}
	seen := make(map[string]struct{}, len(values))
	for _, consumerID := range values {
		seen[consumerID] = struct{}{}
	}
	featureIDs, err := processor.features.FeatureConsumerIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list feature event consumers: %w", err)
	}
	for _, consumerID := range featureIDs {
		if consumerID == "" {
			return nil, errors.New("feature consumer ID is required")
		}
		if _, exists := seen[consumerID]; exists {
			return nil, fmt.Errorf("duplicate event consumer %q", consumerID)
		}
		seen[consumerID] = struct{}{}
		values = append(values, consumerID)
	}
	return values, nil
}

func isStandardConsumer(consumerID string) bool {
	switch consumerID {
	case TrafficConsumerID, PolicyConsumerID, AccessConsumerID:
		return true
	default:
		return false
	}
}

func (processor *Processor) processEvent(ctx context.Context, consumerID string, source eventstore.StoredEvent) error {
	switch consumerID {
	case TrafficConsumerID:
		if source.Event.Kind != agentv1.EventTrafficSnapshot {
			return nil
		}
		value, err := agentv1.DecodeTrafficSnapshotEvent(source.Event.Payload)
		if err != nil {
			return err
		}
		return processor.business.ApplyTrafficSnapshot(ctx, store.TrafficSnapshotSource{
			NodeID: source.NodeID, StreamID: source.StreamID, Sequence: source.Event.Sequence,
			ObservedAt: source.Event.ObservedAt, ReceivedAt: source.ReceivedAt,
		}, value)
	case PolicyConsumerID:
		if source.Event.Kind != agentv1.EventPolicyStatus {
			return nil
		}
		value, err := agentv1.DecodePolicyStatusEvent(source.Event.Payload)
		if err != nil {
			return err
		}
		return processor.business.RecordAuthorizationPolicyStatus(ctx, source.NodeID, value, source.Event.ObservedAt, source.ReceivedAt)
	case AccessConsumerID:
		if source.Event.Kind != agentv1.EventAccess {
			return nil
		}
		value, err := agentv1.DecodeAccessEvent(source.Event.Payload)
		if err != nil {
			return err
		}
		known, err := processor.business.AccessEventReferenceKnown(ctx, source.NodeID, value)
		if err != nil {
			return err
		}
		if !known {
			return nil
		}
		return processor.events.StoreAccessEvent(ctx, source, value)
	default:
		return fmt.Errorf("unknown consumer %q", consumerID)
	}
}
