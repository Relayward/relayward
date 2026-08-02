package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	centerpluginv1 "github.com/Relayward/relayward-sdk/centerplugin/v1"
	"github.com/Relayward/relayward-sdk/protocol"

	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/store"
)

const (
	defaultHealthInterval       = 30 * time.Second
	healthFailuresBeforeRestart = 3
	stableProcessDuration       = 5 * time.Minute
)

var ErrPluginUnavailable = errors.New("center plugin is unavailable")

type database interface {
	ListPluginInstallations(context.Context) ([]store.PluginInstallation, error)
	PluginVersionByID(context.Context, string, string) (store.PluginVersion, error)
	ListNodes(context.Context) ([]store.Node, error)
	RecordPluginRuntimeStatus(context.Context, string, string, string, uint64, *protocol.Problem, time.Time) error
}

type artifactStore interface {
	Paths(string, string) (pluginartifact.Paths, error)
	DataDirectory(string) (string, error)
	RuntimeDirectory(string) (string, error)
}

type Supervisor struct {
	database  database
	artifacts artifactStore
	logger    *slog.Logger

	mu             sync.Mutex
	actors         map[string]*pluginActor
	ctx            context.Context
	cancel         context.CancelFunc
	started        bool
	stopping       bool
	healthInterval time.Duration
}

type pluginActor struct {
	mu           sync.Mutex
	process      *managedProcess
	version      *store.PluginVersion
	restartCount uint64
	crashStreak  uint
}

func New(database database, artifacts artifactStore, logger *slog.Logger) (*Supervisor, error) {
	if database == nil || artifacts == nil {
		return nil, errors.New("plugin runtime database and artifact store are required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Supervisor{
		database: database, artifacts: artifacts, logger: logger,
		actors: make(map[string]*pluginActor), healthInterval: defaultHealthInterval,
	}, nil
}

func (supervisor *Supervisor) Start(parent context.Context) error {
	supervisor.mu.Lock()
	if supervisor.started {
		supervisor.mu.Unlock()
		return errors.New("center plugin supervisor already started")
	}
	supervisor.ctx, supervisor.cancel = context.WithCancel(parent)
	supervisor.started = true
	supervisor.stopping = false
	supervisor.mu.Unlock()

	installations, err := supervisor.database.ListPluginInstallations(parent)
	if err != nil {
		supervisor.mu.Lock()
		if supervisor.cancel != nil {
			supervisor.cancel()
		}
		supervisor.started = false
		supervisor.stopping = false
		supervisor.mu.Unlock()
		return err
	}
	for _, installation := range installations {
		if installation.ActiveVersion == "" || (installation.State != "active" && installation.State != "failed") {
			continue
		}
		version, err := supervisor.database.PluginVersionByID(parent, installation.PluginID, installation.ActiveVersion)
		if err != nil {
			supervisor.recordFailure(installation.PluginID, 0, "installed plugin version is missing")
			continue
		}
		actor := supervisor.actor(installation.PluginID)
		actor.mu.Lock()
		actor.restartCount = installation.RestartCount
		process, err := supervisor.startProcess(parent, version)
		if err != nil {
			actor.version = cloneVersion(&version)
			actor.crashStreak = 1
			supervisor.recordFailure(version.PluginID, actor.restartCount, "center plugin recovery failed")
			supervisor.logger.Error("recover center plugin", "plugin_id", version.PluginID, "error", err)
			actor.mu.Unlock()
			go supervisor.retry(version.PluginID, actor)
			continue
		}
		actor.process = process
		actor.version = cloneVersion(&version)
		actor.crashStreak = 0
		supervisor.recordHealthy(version.PluginID, actor.restartCount)
		supervisor.watch(version.PluginID, actor, process)
		actor.mu.Unlock()
	}
	return nil
}

func (supervisor *Supervisor) Switch(ctx context.Context, version store.PluginVersion) error {
	if !supervisor.isRunning() {
		return ErrPluginUnavailable
	}
	actor := supervisor.actor(version.PluginID)
	actor.mu.Lock()
	defer actor.mu.Unlock()
	if !supervisor.isRunning() {
		return ErrPluginUnavailable
	}
	previous := cloneVersion(actor.version)
	if err := supervisor.detachAndStop(ctx, actor); err != nil {
		return fmt.Errorf("stop previous center plugin: %w", err)
	}
	process, err := supervisor.startProcess(ctx, version)
	if err != nil {
		restoreErr := supervisor.restoreLocked(ctx, actor, previous)
		if restoreErr != nil {
			return errors.Join(fmt.Errorf("activate center plugin: %w", err), fmt.Errorf("restore previous center plugin: %w", restoreErr))
		}
		return fmt.Errorf("activate center plugin: %w", err)
	}
	actor.process = process
	actor.version = cloneVersion(&version)
	actor.restartCount = 0
	actor.crashStreak = 0
	supervisor.watch(version.PluginID, actor, process)
	return nil
}

func (supervisor *Supervisor) Rollback(ctx context.Context, pluginID string, previous *store.PluginVersion) error {
	actor := supervisor.actor(pluginID)
	actor.mu.Lock()
	defer actor.mu.Unlock()
	if err := supervisor.detachAndStop(ctx, actor); err != nil {
		return err
	}
	return supervisor.restoreLocked(ctx, actor, cloneVersion(previous))
}

func (supervisor *Supervisor) StopPlugin(ctx context.Context, pluginID string) error {
	actor := supervisor.actor(pluginID)
	actor.mu.Lock()
	defer actor.mu.Unlock()
	if err := supervisor.detachAndStop(ctx, actor); err != nil {
		return err
	}
	actor.version = nil
	actor.restartCount = 0
	actor.crashStreak = 0
	return nil
}

func (supervisor *Supervisor) InvokeUI(ctx context.Context, pluginID, method string, raw []byte) ([]byte, error) {
	request := &centerpluginv1.InvokeUIRequest{Method: method, Json: raw}
	if err := centerpluginv1.ValidateInvokeUIRequest(request); err != nil {
		return nil, err
	}
	actor := supervisor.actor(pluginID)
	actor.mu.Lock()
	process := actor.process
	actor.mu.Unlock()
	if process == nil || process.exited() {
		return nil, ErrPluginUnavailable
	}
	callContext, cancel := context.WithTimeout(ctx, pluginRPCTimeout)
	defer cancel()
	response, err := process.client.InvokeUI(callContext, request)
	if err != nil {
		return nil, fmt.Errorf("invoke center plugin UI: %w", err)
	}
	if err := centerpluginv1.ValidateInvokeUIResponse(response); err != nil {
		return nil, fmt.Errorf("validate center plugin UI response: %w", err)
	}
	return append([]byte(nil), response.Json...), nil
}

func (supervisor *Supervisor) Close(ctx context.Context) error {
	supervisor.mu.Lock()
	if !supervisor.started || supervisor.stopping {
		supervisor.mu.Unlock()
		return nil
	}
	supervisor.stopping = true
	if supervisor.cancel != nil {
		supervisor.cancel()
	}
	actors := make([]*pluginActor, 0, len(supervisor.actors))
	for _, actor := range supervisor.actors {
		actors = append(actors, actor)
	}
	supervisor.mu.Unlock()
	var result error
	for _, actor := range actors {
		actor.mu.Lock()
		result = errors.Join(result, supervisor.detachAndStop(ctx, actor))
		actor.mu.Unlock()
	}
	return result
}

func (supervisor *Supervisor) restoreLocked(ctx context.Context, actor *pluginActor, previous *store.PluginVersion) error {
	actor.version = cloneVersion(previous)
	if previous == nil {
		actor.process = nil
		return nil
	}
	process, err := supervisor.startProcess(ctx, *previous)
	if err != nil {
		actor.process = nil
		actor.crashStreak++
		supervisor.recordFailure(previous.PluginID, actor.restartCount, "previous center plugin recovery failed")
		go supervisor.retry(previous.PluginID, actor)
		return err
	}
	actor.process = process
	actor.crashStreak = 0
	supervisor.watch(previous.PluginID, actor, process)
	return nil
}

func (supervisor *Supervisor) detachAndStop(ctx context.Context, actor *pluginActor) error {
	process := actor.process
	actor.process = nil
	if process == nil {
		return nil
	}
	if err := process.stop(ctx); err != nil {
		if !process.exited() {
			actor.process = process
		}
		return err
	}
	return nil
}

func (supervisor *Supervisor) watch(pluginID string, actor *pluginActor, process *managedProcess) {
	go func() {
		interval := supervisor.healthInterval
		if interval <= 0 {
			interval = defaultHealthInterval
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		failures := 0
		for {
			reason := "center plugin process exited unexpectedly"
			select {
			case <-process.done:
			case <-ticker.C:
				healthContext, cancel := context.WithTimeout(supervisor.context(), pluginRPCTimeout)
				err := process.checkHealthy(healthContext)
				cancel()
				if err == nil {
					failures = 0
					continue
				}
				failures++
				if failures < healthFailuresBeforeRestart {
					continue
				}
				reason = "center plugin health checks failed"
			}

			actor.mu.Lock()
			if actor.process != process {
				actor.mu.Unlock()
				return
			}
			actor.process = nil
			_ = process.stop(context.Background())
			if time.Since(process.startedAt) >= stableProcessDuration {
				actor.crashStreak = 0
			}
			actor.crashStreak++
			actor.restartCount++
			supervisor.recordFailure(pluginID, actor.restartCount, reason)
			actor.mu.Unlock()
			go supervisor.retry(pluginID, actor)
			return
		}
	}()
}

func (supervisor *Supervisor) retry(pluginID string, actor *pluginActor) {
	for supervisor.isRunning() {
		actor.mu.Lock()
		streak := actor.crashStreak
		if streak == 0 {
			streak = 1
		}
		actor.mu.Unlock()
		delay := time.Second << min(streak-1, 6)
		timer := time.NewTimer(delay)
		select {
		case <-supervisor.context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		actor.mu.Lock()
		if actor.process != nil || actor.version == nil {
			actor.mu.Unlock()
			return
		}
		process, err := supervisor.startProcess(supervisor.context(), *actor.version)
		if err == nil {
			actor.process = process
			actor.crashStreak = 0
			supervisor.recordHealthy(pluginID, actor.restartCount)
			supervisor.watch(pluginID, actor, process)
			actor.mu.Unlock()
			return
		}
		actor.crashStreak++
		supervisor.recordFailure(pluginID, actor.restartCount, "center plugin restart failed")
		actor.mu.Unlock()
	}
}

func (supervisor *Supervisor) recordHealthy(pluginID string, restartCount uint64) {
	err := supervisor.database.RecordPluginRuntimeStatus(
		context.Background(), pluginID, "active", "healthy", restartCount, nil, time.Now().UTC(),
	)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		supervisor.logger.Error("record center plugin health", "plugin_id", pluginID, "error", err)
	}
}

func (supervisor *Supervisor) recordFailure(pluginID string, restartCount uint64, message string) {
	problem := &protocol.Problem{Code: protocol.ErrorUnavailable, Message: message, Retryable: true}
	err := supervisor.database.RecordPluginRuntimeStatus(
		context.Background(), pluginID, "failed", "unhealthy", restartCount, problem, time.Now().UTC(),
	)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		supervisor.logger.Error("record center plugin failure", "plugin_id", pluginID, "error", err)
	}
}

func (supervisor *Supervisor) actor(pluginID string) *pluginActor {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	actor := supervisor.actors[pluginID]
	if actor == nil {
		actor = &pluginActor{}
		supervisor.actors[pluginID] = actor
	}
	return actor
}

func (supervisor *Supervisor) isRunning() bool {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return supervisor.started && !supervisor.stopping
}

func (supervisor *Supervisor) context() context.Context {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	if supervisor.ctx == nil {
		return context.Background()
	}
	return supervisor.ctx
}

func cloneVersion(value *store.PluginVersion) *store.PluginVersion {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.ApprovedPermissions = append([]string(nil), value.ApprovedPermissions...)
	return &cloned
}

func min(left, right uint) uint {
	if left < right {
		return left
	}
	return right
}
