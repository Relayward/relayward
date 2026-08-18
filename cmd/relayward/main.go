package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Relayward/relayward/internal/agentrelease"
	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/buildinfo"
	"github.com/Relayward/relayward/internal/developmentrelease"
	"github.com/Relayward/relayward/internal/eventprocessor"
	"github.com/Relayward/relayward/internal/eventstore"
	"github.com/Relayward/relayward/internal/githubrelease"
	"github.com/Relayward/relayward/internal/management"
	"github.com/Relayward/relayward/internal/pluginartifact"
	"github.com/Relayward/relayward/internal/pluginruntime"
	"github.com/Relayward/relayward/internal/policycoordinator"
	"github.com/Relayward/relayward/internal/secretbox"
	"github.com/Relayward/relayward/internal/server"
	"github.com/Relayward/relayward/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "plugin-exec":
			if err := pluginruntime.RunLimitedPlugin(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "version":
			fmt.Fprintln(os.Stdout, buildinfo.Version)
			return
		case "admin":
			if err := runAdmin(os.Args[2:], os.Stdin, os.Stdout, os.Stderr); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "healthcheck":
			if err := runHealthcheck(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "serve":
			if err := serve(os.Args[2:], logger); err != nil {
				logger.Error("Relayward stopped", "error", err)
				os.Exit(1)
			}
			return
		}
	}
	if err := serve(os.Args[1:], logger); err != nil {
		logger.Error("Relayward stopped", "error", err)
		os.Exit(1)
	}
}

func runHealthcheck(args []string) error {
	flags := flag.NewFlagSet("relayward healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.String("url", "http://127.0.0.1:8080/healthz", "health endpoint URL")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse healthcheck flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected healthcheck argument %q", flags.Arg(0))
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
		},
	}
	request, err := http.NewRequest(http.MethodGet, *target, nil)
	if err != nil {
		return fmt.Errorf("create healthcheck request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Relayward healthcheck failed: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Relayward healthcheck returned HTTP %d", response.StatusCode)
	}
	return nil
}

func serve(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("relayward serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dataDir := flags.String("data", "./data", "persistent data directory")
	webDir := flags.String("web", "./web/dist", "built web asset directory")
	eventHotRetention := flags.Duration("event-hot-retention", 24*time.Hour, "hot event retention")
	eventArchiveRetention := flags.Duration("event-archive-retention", 90*24*time.Hour, "access archive retention")
	developmentPluginRelease := flags.String("development-plugin-release", "", "local development plugin release directory")
	developmentPluginRepository := flags.String("development-plugin-repository", "", "canonical GitHub repository for the development plugin")
	developmentPublicURL := flags.String("development-public-url", "", "public HTTPS origin used by development Agents")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse serve flags: %w", err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected serve argument %q", flags.Arg(0))
	}

	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	absoluteWebDir, err := filepath.Abs(*webDir)
	if err != nil {
		return fmt.Errorf("resolve web asset directory: %w", err)
	}
	indexInfo, err := os.Stat(filepath.Join(absoluteWebDir, "index.html"))
	if err != nil {
		return fmt.Errorf("open web entrypoint: %w", err)
	}
	if !indexInfo.Mode().IsRegular() {
		return fmt.Errorf("web entrypoint is not a regular file: %s", filepath.Join(absoluteWebDir, "index.html"))
	}
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(absoluteDataDir, "relayward.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	events, err := eventstore.Open(ctx, filepath.Join(absoluteDataDir, "events.db"))
	if err != nil {
		return err
	}
	defer events.Close()
	if err := database.DeleteExpiredSessions(ctx, time.Now()); err != nil {
		return err
	}

	secretRecords, err := database.ListSecrets(ctx)
	if err != nil {
		return err
	}
	secrets, err := secretbox.Open(absoluteDataDir, len(secretRecords))
	if err != nil {
		return err
	}
	for _, record := range secretRecords {
		if err := secrets.Verify(record.OwnerType, record.OwnerID, record.Name, record.Ciphertext); err != nil {
			break
		}
	}
	if !secrets.Available() {
		logger.Warn("encrypted secrets unavailable", "error", secrets.Status())
	}
	authentication, err := auth.NewService(database, secrets)
	if err != nil {
		return err
	}
	manager := management.NewService(database, secrets)
	policies, err := policycoordinator.New(database, logger)
	if err != nil {
		return err
	}
	artifacts, err := pluginartifact.Open(filepath.Join(absoluteDataDir, "plugins"))
	if err != nil {
		return err
	}
	pluginSupervisor, err := pluginruntime.New(database, artifacts, events, manager, logger)
	if err != nil {
		return err
	}
	eventProcessor, err := eventprocessor.New(events, database, pluginSupervisor, logger)
	if err != nil {
		return err
	}
	eventArchiver, err := eventprocessor.NewArchiver(events, logger, eventprocessor.ArchiveOptions{
		Directory: filepath.Join(absoluteDataDir, "event-archive"), HotRetention: *eventHotRetention,
		ArchiveRetention: *eventArchiveRetention,
	})
	if err != nil {
		return err
	}
	releaseClient := githubrelease.NewClient(nil)
	var developmentReleases *developmentrelease.Client
	developmentEnabled := *developmentPluginRelease != "" || *developmentPluginRepository != "" || *developmentPublicURL != ""
	if developmentEnabled {
		if *developmentPluginRelease == "" || *developmentPluginRepository == "" || *developmentPublicURL == "" {
			return errors.New("development plugin release, repository, and public URL must be configured together")
		}
		developmentReleases, err = developmentrelease.Open(*developmentPluginRelease, *developmentPluginRepository, releaseClient)
		if err != nil {
			return err
		}
		if err := manager.ConfigurePluginLifecycle(developmentReleases, artifacts, pluginSupervisor); err != nil {
			return err
		}
	} else {
		if err := manager.ConfigurePluginLifecycle(releaseClient, artifacts, pluginSupervisor); err != nil {
			return err
		}
	}
	agentReleases, err := agentrelease.New(releaseClient)
	if err != nil {
		return err
	}
	if err := manager.ConfigureAgentReleases(agentReleases); err != nil {
		return err
	}

	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := pluginSupervisor.Start(signalContext); err != nil {
		return fmt.Errorf("start center plugin supervisor: %w", err)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := pluginSupervisor.Close(closeContext); err != nil {
			logger.Error("center plugin shutdown failed", "error", err)
		}
	}()
	if developmentReleases != nil {
		if err := manager.ConfigureDevelopmentPluginRelease(developmentReleases.Release(), *developmentPublicURL); err != nil {
			return err
		}
		if _, err := manager.EnsureDevelopmentPluginRelease(signalContext); err != nil {
			return fmt.Errorf("activate development plugin release: %w", err)
		}
		logger.Info("development plugin release active", "plugin_id", developmentReleases.Release().Manifest.ID,
			"version", developmentReleases.Release().Manifest.Version)
	}
	go func() {
		if err := policies.Run(signalContext); err != nil && signalContext.Err() == nil {
			logger.Error("policy coordinator stopped", "error", err)
		}
	}()
	go func() {
		if err := eventProcessor.Run(signalContext); err != nil && signalContext.Err() == nil {
			logger.Error("event processor stopped", "error", err)
		}
	}()
	go func() {
		if err := eventArchiver.Run(signalContext); err != nil && signalContext.Err() == nil {
			logger.Error("event archiver stopped", "error", err)
		}
	}()
	httpServer := &http.Server{
		Addr: *listen,
		Handler: server.New(server.Options{
			Version:           buildinfo.Version,
			Store:             database,
			EventStore:        events,
			Auth:              authentication,
			Management:        manager,
			Secrets:           secrets,
			Logger:            logger,
			WebAssets:         os.DirFS(absoluteWebDir),
			PolicyCoordinator: policies,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      11 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-signalContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			logger.Error("HTTP shutdown failed", "error", err)
		}
	}()

	logger.Info("Relayward starting", "listen", *listen, "version", buildinfo.Version, "data_dir", absoluteDataDir)
	if developmentReleases != nil {
		go reconcileDevelopmentNodes(signalContext, manager, logger)
	}
	err = httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		stop()
		<-shutdownDone
		return fmt.Errorf("serve HTTP: %w", err)
	}
	<-shutdownDone
	return nil
}

func reconcileDevelopmentNodes(ctx context.Context, manager *management.Service, logger *slog.Logger) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			updated, err := manager.ReconcileDevelopmentNodePlugins(ctx)
			if err == nil {
				logger.Info("development node plugins reconciled", "updated", updated)
				return
			}
			logger.Warn("development node plugin reconciliation will retry", "error", err)
			timer.Reset(5 * time.Second)
		}
	}
}

func runAdmin(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printAdminUsage(stderr)
		return fmt.Errorf("unknown admin command")
	}
	switch args[0] {
	case "reset-totp":
		return runAdminResetTOTP(args[1:], stdout, stderr)
	case "reset-password":
		return runAdminResetPassword(args[1:], stdin, stdout, stderr)
	case "recover-secrets":
		return runAdminRecoverSecrets(args[1:], stdout, stderr)
	default:
		printAdminUsage(stderr)
		return fmt.Errorf("unknown admin command")
	}
}

func runAdminResetPassword(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("relayward admin reset-password", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "./data", "persistent data directory")
	passwordStdin := flags.Bool("password-stdin", false, "read the new password from standard input")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected reset-password argument %q", flags.Arg(0))
	}
	if !*passwordStdin {
		return fmt.Errorf("reset-password requires -password-stdin")
	}
	reader := bufio.NewReader(io.LimitReader(stdin, 2049))
	password, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read password from standard input: %w", err)
	}
	password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
	if reader.Buffered() > 0 || len(password) > 1024 {
		return fmt.Errorf("password input is too long")
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	database, err := store.Open(context.Background(), filepath.Join(absoluteDataDir, "relayward.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	initialized, err := database.HasAdministrator(context.Background())
	if err != nil {
		return err
	}
	if !initialized {
		return fmt.Errorf("Relayward is not initialized")
	}
	if err := database.ResetAdministratorPassword(context.Background(), passwordHash, time.Now()); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Administrator password reset; all administrator sessions were revoked.")
	return nil
}

func runAdminResetTOTP(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("relayward admin reset-totp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "./data", "persistent data directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected reset-totp argument %q", flags.Arg(0))
	}
	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	database, err := store.Open(context.Background(), filepath.Join(absoluteDataDir, "relayward.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	initialized, err := database.HasAdministrator(context.Background())
	if err != nil {
		return err
	}
	if !initialized {
		return fmt.Errorf("Relayward is not initialized")
	}
	if err := database.ResetTOTP(context.Background(), "local_admin", time.Now()); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "TOTP reset; all administrator sessions were revoked.")
	return nil
}

func runAdminRecoverSecrets(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("relayward admin recover-secrets", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "./data", "persistent data directory")
	confirmed := flags.Bool("confirm-discard-encrypted-secrets", false, "confirm deletion of ciphertext that cannot be recovered")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected recover-secrets argument %q", flags.Arg(0))
	}
	if !*confirmed {
		return fmt.Errorf("recover-secrets requires -confirm-discard-encrypted-secrets")
	}
	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	database, err := store.Open(context.Background(), filepath.Join(absoluteDataDir, "relayward.db"))
	if err != nil {
		return err
	}
	defer database.Close()
	initialized, err := database.HasAdministrator(context.Background())
	if err != nil {
		return err
	}
	if !initialized {
		return fmt.Errorf("Relayward is not initialized")
	}
	records, err := database.ListSecrets(context.Background())
	if err != nil {
		return err
	}
	manager, err := secretbox.Open(absoluteDataDir, len(records))
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := manager.Verify(record.OwnerType, record.OwnerID, record.Name, record.Ciphertext); err != nil {
			break
		}
	}
	if manager.Available() {
		return fmt.Errorf("instance key is available; refusing to discard encrypted secrets")
	}
	result, err := database.DiscardUnrecoverableSecrets(context.Background(), time.Now())
	if err != nil {
		return err
	}
	if err := secretbox.DiscardKey(absoluteDataDir); err != nil {
		return err
	}
	replacement, err := secretbox.Open(absoluteDataDir, 0)
	if err != nil {
		return err
	}
	if !replacement.Available() {
		return fmt.Errorf("replacement instance key is unavailable: %w", replacement.Status())
	}
	fmt.Fprintf(stdout,
		"Instance key replaced; discarded %d encrypted secrets, expired %d pending commands, and marked %d plugin configurations for re-entry. All administrator sessions were revoked.\n",
		result.DiscardedSecrets, result.ExpiredCommands, result.PluginsRequiringConfiguration,
	)
	return nil
}

func printAdminUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: relayward admin reset-totp [-data <directory>]")
	fmt.Fprintln(output, "       relayward admin reset-password [-data <directory>] -password-stdin")
	fmt.Fprintln(output, "       relayward admin recover-secrets [-data <directory>] -confirm-discard-encrypted-secrets")
}
