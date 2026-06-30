package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SadNoo/gosspanel/internal/agent"
	"github.com/SadNoo/gosspanel/internal/config"
	"github.com/SadNoo/gosspanel/internal/httpserver"
	"github.com/SadNoo/gosspanel/internal/relay"
	"github.com/SadNoo/gosspanel/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if len(os.Args) > 1 && os.Args[1] == "agent" {
		if err := agent.Run(ctx, os.Args[2:], logger); err != nil {
			logger.Error("agent failed", "error", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.FromEnv()
	sqlStore, err := store.OpenSQLite(ctx, cfg.DataPath)
	if err != nil {
		logger.Error("store failed", "error", err)
		os.Exit(1)
	}
	defer sqlStore.Close()

	relayMgr := relay.NewManager(sqlStore, logger)
	defer relayMgr.Close()
	if err := relayMgr.Sync(ctx); err != nil {
		logger.Error("relay sync failed", "error", err)
		os.Exit(1)
	}

	server := httpserver.New(cfg, sqlStore, relayMgr, logger)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("goss listening", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("server failed", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
}
