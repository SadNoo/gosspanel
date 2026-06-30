package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"
)

type Config struct {
	Server   string
	Token    string
	NodeID   string
	Name     string
	Region   string
	Role     domain.NodeRole
	Interval time.Duration
}

func Run(ctx context.Context, args []string, logger *slog.Logger) error {
	cfg, err := parseArgs(args)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	if err := postJSON(client, cfg, "/api/agent/register", domain.AgentRegisterRequest{
		ID:     cfg.NodeID,
		Name:   cfg.Name,
		Region: cfg.Region,
		Role:   cfg.Role,
	}); err != nil {
		return err
	}
	logger.Info("agent registered", "server", cfg.Server, "node", cfg.NodeID)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		if err := heartbeat(client, cfg, logger); err != nil {
			logger.Warn("agent heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func parseArgs(args []string) (Config, error) {
	hostname, _ := os.Hostname()
	cfg := Config{}
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.StringVar(&cfg.Server, "server", "http://localhost:8080", "goss server url")
	fs.StringVar(&cfg.Token, "token", "dev-agent-token", "agent token")
	fs.StringVar(&cfg.NodeID, "node-id", hostname, "node id")
	fs.StringVar(&cfg.Name, "name", hostname, "node display name")
	fs.StringVar(&cfg.Region, "region", "agent", "node region")
	fs.Func("role", "machine role: client or relay", func(value string) error {
		if value == string(domain.NodeRoleRelay) {
			cfg.Role = domain.NodeRoleRelay
			return nil
		}
		cfg.Role = domain.NodeRoleClient
		return nil
	})
	fs.DurationVar(&cfg.Interval, "interval", 15*time.Second, "heartbeat interval")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.Role == "" {
		cfg.Role = domain.NodeRoleClient
	}
	if cfg.NodeID == "" {
		return cfg, fmt.Errorf("node id is required")
	}
	return cfg, nil
}

func heartbeat(client *http.Client, cfg Config, logger *slog.Logger) error {
	req := domain.AgentHeartbeatRequest{
		ID:       cfg.NodeID,
		Role:     cfg.Role,
		Status:   domain.NodeStatusRunning,
		Load:     "agent",
		Latency:  "-",
		Traffic:  "0 B",
		LastSeen: time.Now().Format("15:04:05"),
	}
	if err := postJSON(client, cfg, "/api/agent/heartbeat", req); err != nil {
		return err
	}
	rules, err := getRules(client, cfg)
	if err != nil {
		return err
	}
	logger.Info("agent heartbeat ok", "rules", len(rules))
	return nil
}

func getRules(client *http.Client, cfg Config) ([]domain.RelayRule, error) {
	req, err := http.NewRequest(http.MethodGet, cfg.Server+"/api/agent/rules?relayNodeId="+cfg.NodeID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Goss-Agent-Token", cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("agent rules failed: %s", resp.Status)
	}
	var rules []domain.RelayRule
	if err := json.NewDecoder(resp.Body).Decode(&rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func postJSON(client *http.Client, cfg Config, path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, cfg.Server+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goss-Agent-Token", cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s failed: %s", path, resp.Status)
	}
	return nil
}
