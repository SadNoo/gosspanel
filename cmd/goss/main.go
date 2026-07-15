package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/SadNoo/gosspanel/internal/agent"
	"github.com/SadNoo/gosspanel/internal/config"
	"github.com/SadNoo/gosspanel/internal/domain"
	"github.com/SadNoo/gosspanel/internal/httpserver"
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
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := runAdmin(ctx, os.Args[2:], logger); err != nil {
			logger.Error("admin command failed", "error", err)
			os.Exit(1)
		}
		return
	}

	cfg := config.FromEnv()
	sqlStore, err := store.OpenSQLite(ctx, cfg.DataPath, cfg.AdminUser, cfg.AdminPassword)
	if err != nil {
		logger.Error("store failed", "error", err)
		os.Exit(1)
	}
	defer sqlStore.Close()

	server := httpserver.New(cfg, sqlStore, logger)

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

func runAdmin(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 0 {
		return errors.New("usage: goss admin reset -data /var/lib/goss/goss.db -user admin -password-stdin")
	}
	switch args[0] {
	case "reset":
		return runAdminReset(ctx, args[1:], logger)
	default:
		return fmt.Errorf("unknown admin command %q", args[0])
	}
}

func runAdminReset(ctx context.Context, args []string, logger *slog.Logger) error {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("admin reset", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataPath := cfg.DataPath
	username := cfg.AdminUser
	password := ""
	passwordStdin := false
	fs.StringVar(&dataPath, "data", dataPath, "sqlite database path")
	fs.StringVar(&username, "user", username, "admin username")
	fs.BoolVar(&passwordStdin, "password-stdin", false, "read admin password from stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username is required")
	}
	if passwordStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		password = strings.TrimRight(line, "\r\n")
	} else if password == "" {
		password = os.Getenv("GOSS_ADMIN_PASSWORD")
	}
	if password == "" {
		return errors.New("password is required, use -password-stdin or GOSS_ADMIN_PASSWORD")
	}

	sqlStore, err := store.OpenSQLite(ctx, dataPath, username, password)
	if err != nil {
		return err
	}
	defer sqlStore.Close()

	if err := sqlStore.UpdateAdminSettings(ctx, domain.AdminSettings{Username: username, Password: password}); err != nil {
		return err
	}
	if err := sqlStore.AddEvent(ctx, domain.Event{Level: "warning", Title: "管理员账号已重置", Body: username, Time: time.Now().Format("15:04:05")}); err != nil {
		logger.Warn("admin reset event failed", "error", err)
	}
	fmt.Fprintf(os.Stdout, "admin account reset for %q\n", username)
	return nil
}
