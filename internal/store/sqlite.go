package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SadNoo/gosspanel/internal/domain"

	_ "github.com/mattn/go-sqlite3"
)

type SQLite struct {
	db *sql.DB
}

func OpenSQLite(ctx context.Context, path string, adminUser string, adminPassword string) (*SQLite, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &SQLite{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.seed(ctx, adminUser, adminPassword); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			region TEXT NOT NULL,
			status TEXT NOT NULL,
			load TEXT NOT NULL,
			latency TEXT NOT NULL,
			traffic TEXT NOT NULL,
			last_seen TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			listen TEXT NOT NULL,
			target TEXT NOT NULL,
			protocol TEXT NOT NULL,
			inbound TEXT NOT NULL,
			outbound TEXT NOT NULL,
			strategy TEXT NOT NULL,
			proxy_protocol TEXT NOT NULL,
			traffic TEXT NOT NULL DEFAULT '',
			connections INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS online_ips (
			ip TEXT NOT NULL,
			entry_node TEXT NOT NULL,
			rule_name TEXT NOT NULL,
			connections INTEGER NOT NULL,
			last_active TEXT NOT NULL,
			PRIMARY KEY (ip, entry_node, rule_name)
		)`,
		`CREATE TABLE IF NOT EXISTS certificates (
			id TEXT PRIMARY KEY,
			domain TEXT NOT NULL,
			issuer TEXT NOT NULL,
			days_left INTEGER NOT NULL,
			used_by TEXT NOT NULL,
			auto_renew INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			time TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLite) seed(ctx context.Context, adminUser string, adminPassword string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_settings`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if adminUser == "" {
			adminUser = "admin"
		}
		if adminPassword == "" {
			adminPassword = "admin"
		}
		if err := s.UpdateAdminSettings(ctx, domain.AdminSettings{Username: adminUser, Password: adminPassword}); err != nil {
			return err
		}
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		nodes := []domain.Node{
			{ID: "node-hk5", Name: "HK5", Region: "Hong Kong", Status: domain.NodeStatusWarning, Load: "71%", Latency: "18 ms", Traffic: "1.42 TB", LastSeen: "12 秒前"},
			{ID: "node-sg1", Name: "SG1", Region: "Singapore", Status: domain.NodeStatusRunning, Load: "46%", Latency: "36 ms", Traffic: "812 GB", LastSeen: "4 秒前"},
			{ID: "node-jp2", Name: "JP2", Region: "Tokyo", Status: domain.NodeStatusRunning, Load: "39%", Latency: "42 ms", Traffic: "698 GB", LastSeen: "8 秒前"},
			{ID: "node-us-la", Name: "US-LA", Region: "Los Angeles", Status: domain.NodeStatusRunning, Load: "58%", Latency: "142 ms", Traffic: "1.08 TB", LastSeen: "6 秒前"},
		}
		for _, node := range nodes {
			if err := s.UpsertNode(ctx, node); err != nil {
				return err
			}
		}
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rules`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		seedRules := []domain.RuleInput{
			{
				Name:     "HK 游戏端口段",
				Listen:   "HK5 :20000-20100",
				Target:   "US-LA :443",
				Protocol: "TCP+TLS",
				Inbound:  domain.RelayProtocolTunnelTLS,
				Outbound: domain.RelayProtocolTunnelTLS,
				Strategy: domain.StrategyFallback,
				ProxyProtocol: domain.ProxyProtocolConfig{
					Mode:         domain.ProxyProtocolSend,
					Version:      domain.ProxyProtocolVersion2,
					TrustedCIDRs: []string{},
				},
				Status:  domain.RuleStatusRunning,
				Enabled: true,
			},
			{
				Name:     "本机 TCP 转发示例",
				Listen:   "localhost:18080",
				Target:   "localhost:8080",
				Protocol: "TCP",
				Inbound:  domain.RelayProtocolDirectTCP,
				Outbound: domain.RelayProtocolDirectTCP,
				Strategy: domain.StrategySingle,
				ProxyProtocol: domain.ProxyProtocolConfig{
					Mode:    domain.ProxyProtocolOff,
					Version: domain.ProxyProtocolVersion2,
				},
				Status:  domain.RuleStatusPaused,
				Enabled: false,
			},
		}
		for _, rule := range seedRules {
			if _, err := s.CreateRule(ctx, rule); err != nil {
				return err
			}
		}
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM certificates`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		certs := []domain.Certificate{
			{ID: "cert-relay-example", Domain: "relay.example.com", Issuer: "Let's Encrypt / Cloudflare DNS", DaysLeft: 72, UsedBy: "WSS, TCP+TLS", AutoRenew: true},
			{ID: "cert-edge-wildcard", Domain: "*.edge.example.com", Issuer: "ZeroSSL / DNSPod", DaysLeft: 41, UsedBy: "多入口规则", AutoRenew: true},
		}
		for _, cert := range certs {
			_, err := s.db.ExecContext(ctx, `INSERT INTO certificates (id, domain, issuer, days_left, used_by, auto_renew) VALUES (?, ?, ?, ?, ?, ?)`,
				cert.ID, cert.Domain, cert.Issuer, cert.DaysLeft, cert.UsedBy, boolInt(cert.AutoRenew))
			if err != nil {
				return err
			}
		}
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		events := []domain.Event{
			{Level: "info", Title: "goss 初始化完成", Body: "SQLite 存储和 Web 管理台已就绪", Time: nowLabel()},
			{Level: "info", Title: "转发器等待规则", Body: "创建 direct_tcp 规则后会自动启动监听", Time: nowLabel()},
		}
		for _, event := range events {
			if err := s.AddEvent(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLite) Overview(ctx context.Context) (domain.Overview, error) {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return domain.Overview{}, err
	}
	rules, err := s.Rules(ctx)
	if err != nil {
		return domain.Overview{}, err
	}
	events, err := s.Events(ctx)
	if err != nil {
		return domain.Overview{}, err
	}
	active := 0
	for _, rule := range rules {
		active += rule.Connections
	}
	online := 0
	for _, node := range nodes {
		if node.Status == domain.NodeStatusRunning {
			online++
		}
	}
	return domain.Overview{
		OnlineNodes:       fmt.Sprintf("%d / %d", online, len(nodes)),
		ActiveConnections: active,
		DailyTraffic:      "0 B",
		RealIPCaptureRate: "ready",
		Nodes:             nodes,
		Rules:             rules,
		Events:            events,
	}, nil
}

func (s *SQLite) Nodes(ctx context.Context) ([]domain.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, region, status, load, latency, traffic, last_seen FROM nodes ORDER BY updated_at DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		var node domain.Node
		if err := rows.Scan(&node.ID, &node.Name, &node.Region, &node.Status, &node.Load, &node.Latency, &node.Traffic, &node.LastSeen); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *SQLite) UpsertNode(ctx context.Context, node domain.Node) error {
	if node.ID == "" {
		return errors.New("node id is required")
	}
	if node.Name == "" {
		node.Name = node.ID
	}
	if node.Region == "" {
		node.Region = "unknown"
	}
	if node.Status == "" {
		node.Status = domain.NodeStatusRunning
	}
	if node.LastSeen == "" {
		node.LastSeen = nowLabel()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (id, name, region, status, load, latency, traffic, last_seen, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			region = excluded.region,
			status = excluded.status,
			load = excluded.load,
			latency = excluded.latency,
			traffic = excluded.traffic,
			last_seen = excluded.last_seen,
			updated_at = excluded.updated_at`,
		node.ID, node.Name, node.Region, node.Status, node.Load, node.Latency, node.Traffic, node.LastSeen, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *SQLite) Rules(ctx context.Context) ([]domain.RelayRule, error) {
	return s.queryRules(ctx, `SELECT id, name, listen, target, protocol, inbound, outbound, strategy, proxy_protocol, traffic, connections, status, enabled FROM rules ORDER BY created_at DESC`)
}

func (s *SQLite) EnabledRules(ctx context.Context) ([]domain.RelayRule, error) {
	return s.queryRules(ctx, `SELECT id, name, listen, target, protocol, inbound, outbound, strategy, proxy_protocol, traffic, connections, status, enabled FROM rules WHERE enabled = 1 AND status = 'running' ORDER BY created_at DESC`)
}

func (s *SQLite) Rule(ctx context.Context, id string) (domain.RelayRule, error) {
	rules, err := s.queryRules(ctx, `SELECT id, name, listen, target, protocol, inbound, outbound, strategy, proxy_protocol, traffic, connections, status, enabled FROM rules WHERE id = ?`, id)
	if err != nil {
		return domain.RelayRule{}, err
	}
	if len(rules) == 0 {
		return domain.RelayRule{}, sql.ErrNoRows
	}
	return rules[0], nil
}

func (s *SQLite) CreateRule(ctx context.Context, input domain.RuleInput) (domain.RelayRule, error) {
	rule := normalizeRuleInput(newID("rule"), input)
	proxyJSON, err := json.Marshal(rule.ProxyProtocol)
	if err != nil {
		return domain.RelayRule{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO rules (id, name, listen, target, protocol, inbound, outbound, strategy, proxy_protocol, traffic, connections, status, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Name, rule.Listen, rule.Target, rule.Protocol, rule.Inbound, rule.Outbound, rule.Strategy,
		string(proxyJSON), rule.Traffic, rule.Connections, rule.Status, boolInt(rule.Enabled), now, now)
	if err != nil {
		return domain.RelayRule{}, err
	}
	return rule, nil
}

func (s *SQLite) UpdateRule(ctx context.Context, id string, input domain.RuleInput) (domain.RelayRule, error) {
	if _, err := s.Rule(ctx, id); err != nil {
		return domain.RelayRule{}, err
	}
	rule := normalizeRuleInput(id, input)
	proxyJSON, err := json.Marshal(rule.ProxyProtocol)
	if err != nil {
		return domain.RelayRule{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE rules SET name = ?, listen = ?, target = ?, protocol = ?, inbound = ?, outbound = ?, strategy = ?,
			proxy_protocol = ?, traffic = ?, connections = ?, status = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		rule.Name, rule.Listen, rule.Target, rule.Protocol, rule.Inbound, rule.Outbound, rule.Strategy, string(proxyJSON),
		rule.Traffic, rule.Connections, rule.Status, boolInt(rule.Enabled), time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return domain.RelayRule{}, err
	}
	return rule, nil
}

func (s *SQLite) DeleteRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLite) RecordOnlineIP(ctx context.Context, item domain.OnlineIP) error {
	if item.IP == "" {
		return nil
	}
	if item.LastActive == "" {
		item.LastActive = nowLabel()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO online_ips (ip, entry_node, rule_name, connections, last_active)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(ip, entry_node, rule_name) DO UPDATE SET
			connections = online_ips.connections + excluded.connections,
			last_active = excluded.last_active`,
		item.IP, item.EntryNode, item.RuleName, max(1, item.Connections), item.LastActive)
	return err
}

func (s *SQLite) OnlineIPs(ctx context.Context) ([]domain.OnlineIP, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ip, entry_node, rule_name, connections, last_active FROM online_ips ORDER BY connections DESC, last_active DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.OnlineIP
	for rows.Next() {
		var item domain.OnlineIP
		if err := rows.Scan(&item.IP, &item.EntryNode, &item.RuleName, &item.Connections, &item.LastActive); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLite) Certificates(ctx context.Context) ([]domain.Certificate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, domain, issuer, days_left, used_by, auto_renew FROM certificates ORDER BY days_left ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var certs []domain.Certificate
	for rows.Next() {
		var cert domain.Certificate
		var autoRenew int
		if err := rows.Scan(&cert.ID, &cert.Domain, &cert.Issuer, &cert.DaysLeft, &cert.UsedBy, &autoRenew); err != nil {
			return nil, err
		}
		cert.AutoRenew = autoRenew == 1
		certs = append(certs, cert)
	}
	return certs, rows.Err()
}

func (s *SQLite) Events(ctx context.Context) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT level, title, body, time FROM events ORDER BY id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []domain.Event
	for rows.Next() {
		var event domain.Event
		if err := rows.Scan(&event.Level, &event.Title, &event.Body, &event.Time); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *SQLite) AddEvent(ctx context.Context, event domain.Event) error {
	if event.Time == "" {
		event.Time = nowLabel()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events (level, title, body, time) VALUES (?, ?, ?, ?)`, event.Level, event.Title, event.Body, event.Time)
	return err
}

func (s *SQLite) AdminSettings(ctx context.Context) (domain.AdminSettings, error) {
	var settings domain.AdminSettings
	err := s.db.QueryRowContext(ctx, `SELECT username, password FROM admin_settings WHERE id = 1`).Scan(&settings.Username, &settings.Password)
	return settings, err
}

func (s *SQLite) UpdateAdminSettings(ctx context.Context, settings domain.AdminSettings) error {
	if settings.Username == "" {
		return errors.New("username is required")
	}
	if settings.Password == "" {
		return errors.New("password is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_settings (id, username, password, updated_at)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			password = excluded.password,
			updated_at = excluded.updated_at`,
		settings.Username, settings.Password, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *SQLite) queryRules(ctx context.Context, query string, args ...any) ([]domain.RelayRule, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []domain.RelayRule
	for rows.Next() {
		var rule domain.RelayRule
		var proxyJSON string
		var enabled int
		if err := rows.Scan(&rule.ID, &rule.Name, &rule.Listen, &rule.Target, &rule.Protocol, &rule.Inbound, &rule.Outbound, &rule.Strategy, &proxyJSON, &rule.Traffic, &rule.Connections, &rule.Status, &enabled); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(proxyJSON), &rule.ProxyProtocol); err != nil {
			return nil, err
		}
		rule.Enabled = enabled == 1
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func normalizeRuleInput(id string, input domain.RuleInput) domain.RelayRule {
	if input.Protocol == "" {
		input.Protocol = "TCP"
	}
	if input.Inbound == "" {
		input.Inbound = domain.RelayProtocolDirectTCP
	}
	if input.Outbound == "" {
		input.Outbound = domain.RelayProtocolDirectTCP
	}
	if input.Strategy == "" {
		input.Strategy = domain.StrategySingle
	}
	if input.ProxyProtocol.Version == "" {
		input.ProxyProtocol.Version = domain.ProxyProtocolVersion2
	}
	if input.Status == "" {
		input.Status = domain.RuleStatusRunning
	}
	return domain.RelayRule{
		ID:            id,
		Name:          input.Name,
		Listen:        input.Listen,
		Target:        input.Target,
		Protocol:      input.Protocol,
		Inbound:       input.Inbound,
		Outbound:      input.Outbound,
		Strategy:      input.Strategy,
		ProxyProtocol: input.ProxyProtocol,
		Status:        input.Status,
		Enabled:       input.Enabled,
	}
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func nowLabel() string {
	return time.Now().Format("15:04:05")
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
