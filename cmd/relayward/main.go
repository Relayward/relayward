package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Relayward/relayward/internal/buildinfo"
	"github.com/Relayward/relayward/internal/server"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           server.New(buildinfo.Version),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP shutdown failed", "error", err)
		}
	}()

	logger.Info("Relayward starting", "listen", *listen, "version", buildinfo.Version)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("Relayward stopped unexpectedly", "error", err)
		os.Exit(1)
	}
}
