package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SadNoo/gosspanel/internal/auth"
	"github.com/SadNoo/gosspanel/internal/config"
	"github.com/SadNoo/gosspanel/internal/domain"
	"github.com/SadNoo/gosspanel/internal/store"
	"github.com/SadNoo/gosspanel/web"
)

type Server struct {
	cfg    config.Config
	store  store.Store
	auth   *auth.Manager
	logger *slog.Logger
}

func New(cfg config.Config, st store.Store, logger *slog.Logger) *http.Server {
	app := &Server{
		cfg:    cfg,
		store:  st,
		auth:   auth.New(cfg),
		logger: logger,
	}

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.me))
	mux.HandleFunc("GET /api/settings/account", s.requireAuth(s.accountSettings))
	mux.HandleFunc("PUT /api/settings/account", s.requireAuth(s.updateAccountSettings))
	mux.HandleFunc("GET /api/agent/bootstrap", s.requireAuth(s.agentBootstrap))

	mux.HandleFunc("POST /api/agent/register", s.requireAgent(s.agentRegister))
	mux.HandleFunc("POST /api/agent/heartbeat", s.requireAgent(s.agentHeartbeat))
	mux.HandleFunc("GET /api/agent/rules", s.requireAgent(s.agentRules))
	mux.HandleFunc("POST /api/agent/online-ips", s.requireAgent(s.agentOnlineIPs))

	mux.HandleFunc("GET /api/overview", s.requireAuth(s.overview))
	mux.HandleFunc("GET /api/nodes", s.requireAuth(s.nodes))
	mux.HandleFunc("GET /api/relay-machines", s.requireAuth(s.relayMachines))
	mux.HandleFunc("GET /api/rules", s.requireAuth(s.rules))
	mux.HandleFunc("POST /api/rules", s.requireAuth(s.createRule))
	mux.HandleFunc("GET /api/rules/export", s.requireAuth(s.exportRules))
	mux.HandleFunc("POST /api/rules/import", s.requireAuth(s.importRules))
	mux.HandleFunc("GET /api/rules/{id}", s.requireAuth(s.rule))
	mux.HandleFunc("PUT /api/rules/{id}", s.requireAuth(s.updateRule))
	mux.HandleFunc("DELETE /api/rules/{id}", s.requireAuth(s.deleteRule))
	mux.HandleFunc("GET /api/online-ips", s.requireAuth(s.onlineIPs))
	mux.HandleFunc("GET /api/certificates", s.requireAuth(s.certificates))
	mux.HandleFunc("GET /api/events", s.requireAuth(s.events))
	mux.Handle("GET /", s.requireAuthHandler(s.static()))
	return s.logRequests(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.validSession(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(loginHTML))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		req.User = r.Form.Get("user")
		req.Password = r.Form.Get("password")
	}
	settings, ok := s.checkPassword(r.Context(), req.User, req.Password)
	if !ok {
		writeError(w, http.StatusUnauthorized, errors.New("invalid user or password"))
		return
	}
	s.auth.SetSession(w, settings.Username, settings.UpdatedAt)
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.auth.ClearSession(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, _ := s.validSession(r)
	writeJSON(w, http.StatusOK, map[string]string{"user": user})
}

func (s *Server) accountSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.AdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": settings.Username})
}

func (s *Server) updateAccountSettings(w http.ResponseWriter, r *http.Request) {
	var req domain.AccountUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	current, err := s.store.AdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !auth.VerifyPassword(req.CurrentPassword, current.Password) {
		writeError(w, http.StatusForbidden, errors.New("current password is invalid"))
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeError(w, http.StatusBadRequest, errors.New("username is required"))
		return
	}
	next := domain.AdminSettings{
		Username: strings.TrimSpace(req.Username),
		Password: current.Password,
	}
	if req.NewPassword != "" {
		next.Password = req.NewPassword
	}
	if err := s.store.UpdateAdminSettings(r.Context(), next); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := s.store.AdminSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.auth.SetSession(w, updated.Username, updated.UpdatedAt)
	_ = s.store.AddEvent(r.Context(), domain.Event{Level: "info", Title: "账号设置已更新", Body: next.Username, Time: time.Now().Format("15:04:05")})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "username": next.Username})
}

func (s *Server) agentBootstrap(w http.ResponseWriter, r *http.Request) {
	panelURL := externalPanelURL(r)
	writeJSON(w, http.StatusOK, domain.AgentBootstrapCommands{
		PanelURL:        panelURL,
		Panel:           panelInstallCommand(),
		PanelHTTPS:      panelHTTPSInstallCommand(),
		PanelHTTPSLocal: panelHTTPSLocalInstallCommand(),
		Relay:           agentInstallCommand(panelURL, s.cfg.AgentToken, domain.NodeRoleRelay),
		Client:          agentInstallCommand(panelURL, s.cfg.AgentToken, domain.NodeRoleClient),
	})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.store.Overview(r.Context())
	writeResult(w, overview, err)
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.NodesByRole(r.Context(), domain.NodeRoleClient)
	writeResult(w, nodes, err)
}

func (s *Server) relayMachines(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.NodesByRole(r.Context(), domain.NodeRoleRelay)
	writeResult(w, nodes, err)
}

func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.Rules(r.Context())
	writeResult(w, rules, err)
}

func (s *Server) rule(w http.ResponseWriter, r *http.Request) {
	rule, err := s.store.Rule(r.Context(), r.PathValue("id"))
	writeResult(w, rule, err)
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeRuleInput(w, r)
	if !ok {
		return
	}
	rule, err := s.store.CreateRule(r.Context(), input)
	if err == nil {
		_ = s.store.AddEvent(r.Context(), domain.Event{Level: "info", Title: "规则已创建", Body: "等待中转机器拉取执行: " + rule.Name, Time: time.Now().Format("15:04:05")})
	}
	writeResultStatus(w, http.StatusCreated, rule, err)
}

func (s *Server) exportRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.Rules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	items := make([]domain.RuleInput, 0, len(rules))
	for _, rule := range rules {
		items = append(items, ruleInputFromRule(rule))
	}
	payload := domain.RuleExport{
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Count:      len(items),
		Rules:      items,
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="goss-rules-%s.json"`, time.Now().Format("20060102-150405")))
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) importRules(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRuleImportRequest(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.applyRuleImport(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !req.DryRun {
		_ = s.store.AddEvent(r.Context(), domain.Event{
			Level: "info",
			Title: "规则已导入",
			Body:  fmt.Sprintf("新增 %d，更新 %d，副本 %d，跳过 %d", result.Created, result.Updated, result.Copies, result.Skipped),
			Time:  time.Now().Format("15:04:05"),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeRuleInput(w, r)
	if !ok {
		return
	}
	rule, err := s.store.UpdateRule(r.Context(), r.PathValue("id"), input)
	if err == nil {
		_ = s.store.AddEvent(r.Context(), domain.Event{Level: "info", Title: "规则已更新", Body: "等待中转机器拉取执行: " + rule.Name, Time: time.Now().Format("15:04:05")})
	}
	writeResult(w, rule, err)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteRule(r.Context(), r.PathValue("id"))
	if err == nil {
		_ = s.store.AddEvent(r.Context(), domain.Event{Level: "info", Title: "规则已删除", Body: r.PathValue("id"), Time: time.Now().Format("15:04:05")})
	}
	writeResult(w, map[string]string{"status": "deleted"}, err)
}

func (s *Server) applyRuleImport(ctx context.Context, req domain.RuleImportRequest) (domain.RuleImportResult, error) {
	mode := normalizeImportMode(req.Mode)
	result := domain.RuleImportResult{Mode: mode, DryRun: req.DryRun, Total: len(req.Rules)}
	existing, err := s.store.Rules(ctx)
	if err != nil {
		return result, err
	}
	byListen := make(map[string]domain.RelayRule, len(existing))
	for _, rule := range existing {
		byListen[ruleConflictKey(rule.RelayNodeID, rule.Listen)] = rule
	}
	for index, input := range req.Rules {
		input = normalizeImportedRule(input)
		if err := validateRuleInput(input); err != nil {
			result.Issues = append(result.Issues, domain.RuleImportIssue{Index: index, Name: input.Name, Listen: input.Listen, Message: err.Error()})
			result.Skipped++
			continue
		}
		key := ruleConflictKey(input.RelayNodeID, input.Listen)
		current, conflict := byListen[key]
		switch {
		case !conflict:
			if !req.DryRun {
				created, err := s.store.CreateRule(ctx, input)
				if err != nil {
					return result, err
				}
				byListen[key] = created
			}
			result.Created++
		case mode == "replace":
			if !req.DryRun {
				updated, err := s.store.UpdateRule(ctx, current.ID, input)
				if err != nil {
					return result, err
				}
				byListen[key] = updated
			}
			result.Updated++
		case mode == "copy":
			input.Name = input.Name + "（导入副本）"
			input.Status = domain.RuleStatusPaused
			input.Enabled = false
			if !req.DryRun {
				if _, err := s.store.CreateRule(ctx, input); err != nil {
					return result, err
				}
			}
			result.Copies++
			result.Issues = append(result.Issues, domain.RuleImportIssue{Index: index, Name: input.Name, Listen: input.Listen, Message: "监听地址已存在，已作为停用副本导入"})
		default:
			result.Skipped++
			result.Issues = append(result.Issues, domain.RuleImportIssue{Index: index, Name: input.Name, Listen: input.Listen, Message: "监听地址已存在，已跳过"})
		}
	}
	return result, nil
}

func (s *Server) onlineIPs(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.OnlineIPs(r.Context(), r.URL.Query().Get("range"))
	writeResult(w, items, err)
}

func (s *Server) certificates(w http.ResponseWriter, r *http.Request) {
	certs, err := s.store.Certificates(r.Context())
	writeResult(w, certs, err)
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.Events(r.Context())
	writeResult(w, events, err)
}

func (s *Server) agentRegister(w http.ResponseWriter, r *http.Request) {
	var req domain.AgentRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err := s.store.UpsertNode(r.Context(), domain.Node{
		ID:       req.ID,
		Name:     req.Name,
		Region:   req.Region,
		Role:     normalizeNodeRole(req.Role),
		Status:   domain.NodeStatusRunning,
		Load:     "0%",
		Latency:  "-",
		Traffic:  "0 B",
		LastSeen: time.Now().Format("15:04:05"),
	})
	writeResult(w, map[string]string{"status": "registered"}, err)
}

func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req domain.AgentHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Status == "" {
		req.Status = domain.NodeStatusRunning
	}
	if req.LastSeen == "" {
		req.LastSeen = time.Now().Format("15:04:05")
	}
	role := normalizeNodeRole(req.Role)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.ID
	}
	region := strings.TrimSpace(req.Region)
	if region == "" {
		region = string(role)
	}
	err := s.store.UpsertNode(r.Context(), domain.Node{
		ID:       req.ID,
		Name:     name,
		Region:   region,
		Role:     role,
		Status:   req.Status,
		Load:     req.Load,
		Latency:  req.Latency,
		Traffic:  req.Traffic,
		LastSeen: req.LastSeen,
	})
	if err == nil && len(req.Metrics) > 0 {
		err = s.store.UpdateRuleMetrics(r.Context(), req.Metrics)
	}
	writeResult(w, map[string]string{"status": "ok"}, err)
}

func (s *Server) agentRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.EnabledRules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	relayNodeID := r.URL.Query().Get("relayNodeId")
	clientNodeID := r.URL.Query().Get("clientNodeId")
	if relayNodeID == "" {
		relayNodeID = r.Header.Get("X-Goss-Node-ID")
	}
	if relayNodeID == "" && clientNodeID == "" {
		writeJSON(w, http.StatusOK, rules)
		return
	}
	filtered := make([]domain.RelayRule, 0, len(rules))
	for _, rule := range rules {
		if relayNodeID != "" && rule.RelayNodeID == relayNodeID {
			filtered = append(filtered, rule)
			continue
		}
		if clientNodeID != "" && rule.ClientNodeID == clientNodeID {
			filtered = append(filtered, rule)
		}
	}
	writeJSON(w, http.StatusOK, filtered)
}

func (s *Server) agentOnlineIPs(w http.ResponseWriter, r *http.Request) {
	var req domain.AgentOnlineIPReport
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	for _, item := range req.Items {
		if item.EntryNode == "" {
			item.EntryNode = req.NodeID
		}
		if err := s.store.RecordOnlineIP(r.Context(), item); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func normalizeNodeRole(role domain.NodeRole) domain.NodeRole {
	if role == domain.NodeRoleRelay {
		return domain.NodeRoleRelay
	}
	return domain.NodeRoleClient
}

func (s *Server) static() http.Handler {
	staticRoot, err := fs.Sub(web.StaticFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(staticRoot))
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.validSession(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, errors.New("login required"))
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requireAuth(next.ServeHTTP)(w, r)
	})
}

func (s *Server) requireAgent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.CheckAgentToken(r) {
			writeError(w, http.StatusUnauthorized, errors.New("invalid agent token"))
			return
		}
		next(w, r)
	}
}

func (s *Server) validSession(r *http.Request) (string, bool) {
	user, version, ok := s.auth.Session(r)
	if !ok {
		return "", false
	}
	settings, err := s.store.AdminSettings(r.Context())
	if err != nil {
		return "", false
	}
	if user != settings.Username || version != settings.UpdatedAt {
		return "", false
	}
	return user, true
}

func (s *Server) checkPassword(ctx context.Context, user string, password string) (domain.AdminSettings, bool) {
	settings, err := s.store.AdminSettings(ctx)
	if err != nil {
		return domain.AdminSettings{}, false
	}
	if user != settings.Username || !auth.VerifyPassword(password, settings.Password) {
		return domain.AdminSettings{}, false
	}
	if auth.PasswordNeedsRehash(settings.Password) {
		if err := s.store.UpdateAdminSettings(ctx, domain.AdminSettings{Username: settings.Username, Password: password}); err != nil {
			s.logger.Warn("admin password rehash failed", "error", err)
		} else if updated, err := s.store.AdminSettings(ctx); err == nil {
			settings = updated
		}
	}
	return settings, true
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "elapsed", time.Since(start))
	})
}

func decodeRuleImportRequest(w http.ResponseWriter, r *http.Request) (domain.RuleImportRequest, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		return domain.RuleImportRequest{}, err
	}
	var req domain.RuleImportRequest
	if err := json.Unmarshal(body, &req); err == nil && len(req.Rules) > 0 {
		req.Mode = normalizeImportMode(req.Mode)
		return req, nil
	}
	var export domain.RuleExport
	if err := json.Unmarshal(body, &export); err == nil && len(export.Rules) > 0 {
		return domain.RuleImportRequest{Mode: "skip", Rules: export.Rules}, nil
	}
	var rules []domain.RuleInput
	if err := json.Unmarshal(body, &rules); err == nil && len(rules) > 0 {
		return domain.RuleImportRequest{Mode: "skip", Rules: rules}, nil
	}
	return domain.RuleImportRequest{}, errors.New("rules import payload is empty or invalid")
}

func decodeRuleInput(w http.ResponseWriter, r *http.Request) (domain.RuleInput, bool) {
	var input domain.RuleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return input, false
	}
	input = normalizeImportedRule(input)
	if err := validateRuleInput(input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return input, false
	}
	return input, true
}

func validateRuleInput(input domain.RuleInput) error {
	if input.Name == "" || input.RelayNodeID == "" || input.Listen == "" || input.Target == "" {
		return errors.New("name, relayNodeId, listen and target are required")
	}
	if (isTunnelProtocol(input.Inbound) || isGostProtocol(input.Inbound)) && strings.TrimSpace(input.TunnelEndpoint) == "" {
		return errors.New("tunnelEndpoint is required for tunnel protocol")
	}
	return nil
}

func normalizeImportedRule(input domain.RuleInput) domain.RuleInput {
	input.Name = strings.TrimSpace(input.Name)
	input.RelayNodeID = strings.TrimSpace(input.RelayNodeID)
	input.ClientNodeID = strings.TrimSpace(input.ClientNodeID)
	input.Listen = strings.TrimSpace(input.Listen)
	input.Target = strings.TrimSpace(input.Target)
	input.TunnelEndpoint = strings.TrimSpace(input.TunnelEndpoint)
	if input.Protocol == "" {
		input.Protocol = string(input.Inbound)
	}
	if input.Inbound == "" {
		input.Inbound = domain.RelayProtocolDirectTCP
	}
	if input.Outbound == "" {
		input.Outbound = input.Inbound
	}
	if input.Strategy == "" {
		input.Strategy = domain.StrategySingle
	}
	if input.ProxyProtocol.Mode == "" {
		input.ProxyProtocol.Mode = domain.ProxyProtocolOff
	}
	if input.ProxyProtocol.Version == "" {
		input.ProxyProtocol.Version = domain.ProxyProtocolVersion2
	}
	if input.Status == "" {
		input.Status = domain.RuleStatusRunning
	}
	return input
}

func ruleInputFromRule(rule domain.RelayRule) domain.RuleInput {
	return domain.RuleInput{
		Name:           rule.Name,
		RelayNodeID:    rule.RelayNodeID,
		ClientNodeID:   rule.ClientNodeID,
		Listen:         rule.Listen,
		Target:         rule.Target,
		TunnelEndpoint: rule.TunnelEndpoint,
		Protocol:       rule.Protocol,
		Inbound:        rule.Inbound,
		Outbound:       rule.Outbound,
		Strategy:       rule.Strategy,
		ProxyProtocol:  rule.ProxyProtocol,
		Status:         rule.Status,
		Enabled:        rule.Enabled,
	}
}

func normalizeImportMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "replace":
		return "replace"
	case "copy":
		return "copy"
	default:
		return "skip"
	}
}

func ruleConflictKey(relayNodeID string, listen string) string {
	return strings.TrimSpace(relayNodeID) + "\x00" + strings.TrimSpace(listen)
}

func isTunnelProtocol(protocol domain.RelayProtocol) bool {
	switch protocol {
	case domain.RelayProtocolTunnelTCP, domain.RelayProtocolTunnelTLS, domain.RelayProtocolTLS, domain.RelayProtocolWS, domain.RelayProtocolWSS:
		return true
	default:
		return false
	}
}

func isGostProtocol(protocol domain.RelayProtocol) bool {
	switch protocol {
	case domain.RelayProtocolGOSTTCP, domain.RelayProtocolGOSTWS, domain.RelayProtocolGOSTWSS:
		return true
	default:
		return false
	}
}

func writeResult(w http.ResponseWriter, value any, err error) {
	writeResultStatus(w, http.StatusOK, value, err)
}

func writeResultStatus(w http.ResponseWriter, status int, value any, err error) {
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status, value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func externalPanelURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = strings.Split(forwarded, ",")[0]
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = strings.Split(forwarded, ",")[0]
	}
	return strings.TrimSpace(scheme) + "://" + strings.TrimSpace(host)
}

func agentInstallCommand(panelURL string, token string, role domain.NodeRole) string {
	nodeSuffix := "client"
	displayName := "Goss Client"
	region := "Client"
	if role == domain.NodeRoleRelay {
		nodeSuffix = "relay"
		displayName = "Goss Relay"
		region = "Relay"
	}
	return fmt.Sprintf(
		"curl -fsSL https://raw.githubusercontent.com/SadNoo/gosspanel/main/scripts/install-agent.sh | bash -s -- --role %s --server %s --token %s --node-id \"$(hostname)-%s\" --name \"%s $(hostname)\" --region %s --interval 5s%s",
		role,
		shellQuote(panelURL),
		shellQuote(token),
		nodeSuffix,
		displayName,
		shellQuote(region),
		agentTLSInstallFlag(panelURL),
	)
}

func agentTLSInstallFlag(panelURL string) string {
	parsed, err := url.Parse(panelURL)
	if err != nil || parsed.Scheme != "https" {
		return ""
	}
	host := parsed.Hostname()
	if net.ParseIP(host) == nil {
		return ""
	}
	return " --insecure-tls"
}

func panelInstallCommand() string {
	return "curl -fsSL https://raw.githubusercontent.com/SadNoo/gosspanel/main/scripts/install-panel.sh | bash"
}

func panelHTTPSInstallCommand() string {
	return "GOSS_HTTPS=1 GOSS_DOMAIN=panel.example.com curl -fsSL https://raw.githubusercontent.com/SadNoo/gosspanel/main/scripts/install-panel.sh | bash"
}

func panelHTTPSLocalInstallCommand() string {
	return "GOSS_HTTPS=1 curl -fsSL https://raw.githubusercontent.com/SadNoo/gosspanel/main/scripts/install-panel.sh | bash"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

const loginHTML = `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>goss 登录</title>
    <style>
      body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f4f7fb;color:#17202a;font-family:Inter,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
      form{width:min(380px,calc(100vw - 32px));padding:24px;border:1px solid #dde5ee;border-radius:8px;background:white;box-shadow:0 18px 50px rgba(29,41,57,.12)}
      h1{margin:0 0 8px;font-size:30px}p{margin:0 0 22px;color:#667085}label{display:grid;gap:7px;margin:12px 0;color:#667085;font-size:13px;font-weight:700}
      input{height:42px;padding:0 12px;border:1px solid #dde5ee;border-radius:8px;font:inherit}button{width:100%;height:42px;margin-top:12px;border:0;border-radius:8px;background:#2563eb;color:white;font:inherit;font-weight:800;cursor:pointer}
    </style>
  </head>
  <body>
    <form method="post" action="/api/auth/login">
      <h1>goss</h1>
      <p>登录中转管理台</p>
      <label>用户名<input name="user" value="admin" autocomplete="username"></label>
      <label>密码<input name="password" type="password" autocomplete="current-password" autofocus></label>
      <button type="submit">登录</button>
    </form>
  </body>
</html>`
