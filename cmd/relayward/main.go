package main

import (
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
	"syscall"
	"time"

	"github.com/Relayward/relayward/internal/auth"
	"github.com/Relayward/relayward/internal/buildinfo"
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
		case "version":
			fmt.Fprintln(os.Stdout, buildinfo.Version)
			return
		case "admin":
			if err := runAdmin(os.Args[2:], os.Stdout, os.Stderr); err != nil {
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

func serve(args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("relayward serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dataDir := flags.String("data", "./data", "persistent data directory")
	webDir := flags.String("web", "./web/dist", "built web asset directory")
	insecureCookie := flags.Bool("insecure-cookie", false, "allow session cookies over plain HTTP for local development")
	eventHotRetention := flags.Duration("event-hot-retention", 24*time.Hour, "hot event retention")
	eventArchiveRetention := flags.Duration("event-archive-retention", 90*24*time.Hour, "access archive retention")
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

	secretCount, err := database.CountSecrets(ctx)
	if err != nil {
		return err
	}
	secrets, err := secretbox.Open(absoluteDataDir, secretCount)
	if err != nil {
		return err
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
	eventProcessor, err := eventprocessor.New(events, database, logger)
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
	artifacts, err := pluginartifact.Open(filepath.Join(absoluteDataDir, "plugins"))
	if err != nil {
		return err
	}
	pluginSupervisor, err := pluginruntime.New(database, artifacts, logger)
	if err != nil {
		return err
	}
	if err := manager.ConfigurePluginLifecycle(githubrelease.NewClient(nil), artifacts, pluginSupervisor); err != nil {
		return err
	}

	signalContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := pluginSupervisor.Start(signalContext); err != nil {
		return fmt.Errorf("start center plugin supervisor: %w", err)
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
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := pluginSupervisor.Close(closeContext); err != nil {
			logger.Error("center plugin shutdown failed", "error", err)
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
			SecureCookie:      !*insecureCookie,
			WebAssets:         os.DirFS(absoluteWebDir),
			PolicyCoordinator: policies,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
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
	err = httpServer.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		stop()
		<-shutdownDone
		return fmt.Errorf("serve HTTP: %w", err)
	}
	<-shutdownDone
	return nil
}

func runAdmin(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "reset-totp" {
		fmt.Fprintln(stderr, "usage: relayward admin reset-totp [-data <directory>]")
		return fmt.Errorf("unknown admin command")
	}
	flags := flag.NewFlagSet("relayward admin reset-totp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "./data", "persistent data directory")
	if err := flags.Parse(args[1:]); err != nil {
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
