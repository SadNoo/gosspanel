package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"
	"github.com/SadNoo/gosspanel/internal/relay"
)

type Config struct {
	Server      string
	Token       string
	NodeID      string
	Name        string
	Region      string
	Role        domain.NodeRole
	Interval    time.Duration
	ReportIP    string
	InsecureTLS bool
}

type panelRecorder struct {
	client *http.Client
	cfg    Config
}

func Run(ctx context.Context, args []string, logger *slog.Logger) error {
	cfg, err := parseArgs(args)
	if err != nil {
		return err
	}
	client := newHTTPClient(cfg)
	recorder := panelRecorder{client: client, cfg: cfg}
	var relayManager *relay.Manager
	var tunnelManager *relay.TunnelManager
	if cfg.Role == domain.NodeRoleRelay {
		relayManager = relay.NewManager(logger, recorder)
		defer relayManager.Close()
		logger.Info("relay backend enabled", "node", cfg.NodeID)
	} else {
		tunnelManager = relay.NewTunnelManager(logger)
		defer tunnelManager.Close()
		logger.Info("client backend enabled", "node", cfg.NodeID)
	}
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
		var metrics []domain.RuleMetric
		if relayManager != nil {
			metrics = relayManager.Metrics()
		}
		rules, err := heartbeat(client, cfg, logger, metrics)
		if err != nil {
			logger.Warn("agent heartbeat failed", "error", err)
		} else if relayManager != nil {
			if err := relayManager.SyncRules(rules); err != nil {
				logger.Warn("relay sync failed", "error", err)
			}
		} else if tunnelManager != nil {
			if err := tunnelManager.SyncRules(rules); err != nil {
				logger.Warn("client tunnel sync failed", "error", err)
			}
			logger.Info("client rules synced", "rules", len(rules))
		} else {
			logger.Info("client rules synced", "rules", len(rules))
		}
		if cfg.ReportIP != "" {
			if err := recorder.RecordOnlineIP(ctx, domain.OnlineIP{
				IP:          cfg.ReportIP,
				EntryNode:   cfg.NodeID,
				RuleName:    "client-report",
				Connections: 1,
				LastActive:  time.Now().Format("15:04:05"),
			}); err != nil {
				logger.Warn("client real ip report failed", "error", err)
			}
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
	fs.StringVar(&cfg.ReportIP, "report-ip", "", "real ip to report to the panel")
	fs.BoolVar(&cfg.InsecureTLS, "insecure-tls", false, "skip panel TLS certificate verification")
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

func newHTTPClient(cfg Config) *http.Client {
	transport := http.DefaultTransport
	if cfg.InsecureTLS {
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		transport = base
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport}
}

func heartbeat(client *http.Client, cfg Config, logger *slog.Logger, metrics []domain.RuleMetric) ([]domain.RelayRule, error) {
	req := domain.AgentHeartbeatRequest{
		ID:       cfg.NodeID,
		Name:     cfg.Name,
		Region:   cfg.Region,
		Role:     cfg.Role,
		Status:   domain.NodeStatusRunning,
		Load:     "agent",
		Latency:  "-",
		Traffic:  "0 B",
		LastSeen: time.Now().Format("15:04:05"),
		Metrics:  metrics,
	}
	if err := postJSON(client, cfg, "/api/agent/heartbeat", req); err != nil {
		return nil, err
	}
	rules, err := getRules(client, cfg)
	if err != nil {
		return nil, err
	}
	logger.Info("agent heartbeat ok", "rules", len(rules))
	return rules, nil
}

func getRules(client *http.Client, cfg Config) ([]domain.RelayRule, error) {
	param := "clientNodeId"
	if cfg.Role == domain.NodeRoleRelay {
		param = "relayNodeId"
	}
	req, err := http.NewRequest(http.MethodGet, cfg.Server+"/api/agent/rules?"+param+"="+cfg.NodeID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Goss-Agent-Token", cfg.Token)
	req.Header.Set("X-Goss-Node-ID", cfg.NodeID)
	req.Header.Set("X-Goss-Node-Role", string(cfg.Role))
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

func (r panelRecorder) RecordOnlineIP(ctx context.Context, item domain.OnlineIP) error {
	if strings.TrimSpace(item.IP) == "" {
		return nil
	}
	return postJSONContext(ctx, r.client, r.cfg, "/api/agent/online-ips", domain.AgentOnlineIPReport{
		NodeID: r.cfg.NodeID,
		Role:   r.cfg.Role,
		Items:  []domain.OnlineIP{item},
	})
}

func postJSON(client *http.Client, cfg Config, path string, value any) error {
	return postJSONContext(context.Background(), client, cfg, path, value)
}

func postJSONContext(ctx context.Context, client *http.Client, cfg Config, path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Server+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goss-Agent-Token", cfg.Token)
	req.Header.Set("X-Goss-Node-ID", cfg.NodeID)
	req.Header.Set("X-Goss-Node-Role", string(cfg.Role))
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
