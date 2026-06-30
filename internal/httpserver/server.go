package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SadNoo/gosspanel/internal/auth"
	"github.com/SadNoo/gosspanel/internal/config"
	"github.com/SadNoo/gosspanel/internal/domain"
	"github.com/SadNoo/gosspanel/internal/relay"
	"github.com/SadNoo/gosspanel/internal/store"
	"github.com/SadNoo/gosspanel/web"
)

type Server struct {
	cfg    config.Config
	store  store.Store
	auth   *auth.Manager
	relay  *relay.Manager
	logger *slog.Logger
}

func New(cfg config.Config, st store.Store, relayMgr *relay.Manager, logger *slog.Logger) *http.Server {
	app := &Server{
		cfg:    cfg,
		store:  st,
		auth:   auth.New(cfg),
		relay:  relayMgr,
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

	mux.HandleFunc("POST /api/agent/register", s.requireAgent(s.agentRegister))
	mux.HandleFunc("POST /api/agent/heartbeat", s.requireAgent(s.agentHeartbeat))
	mux.HandleFunc("GET /api/agent/rules", s.requireAgent(s.agentRules))

	mux.HandleFunc("GET /api/overview", s.requireAuth(s.overview))
	mux.HandleFunc("GET /api/nodes", s.requireAuth(s.nodes))
	mux.HandleFunc("GET /api/rules", s.requireAuth(s.rules))
	mux.HandleFunc("POST /api/rules", s.requireAuth(s.createRule))
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
	if _, ok := s.auth.User(r); ok {
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
	if !s.auth.CheckPassword(req.User, req.Password) {
		writeError(w, http.StatusUnauthorized, errors.New("invalid user or password"))
		return
	}
	s.auth.SetSession(w, req.User)
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
	user, _ := s.auth.User(r)
	writeJSON(w, http.StatusOK, map[string]string{"user": user})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.store.Overview(r.Context())
	writeResult(w, overview, err)
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.store.Nodes(r.Context())
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
		_ = s.relay.Sync(r.Context())
		_ = s.store.AddEvent(r.Context(), domain.Event{Level: "info", Title: "规则已创建", Body: rule.Name, Time: time.Now().Format("15:04:05")})
	}
	writeResultStatus(w, http.StatusCreated, rule, err)
}

func (s *Server) updateRule(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeRuleInput(w, r)
	if !ok {
		return
	}
	rule, err := s.store.UpdateRule(r.Context(), r.PathValue("id"), input)
	if err == nil {
		_ = s.relay.Sync(r.Context())
		_ = s.store.AddEvent(r.Context(), domain.Event{Level: "info", Title: "规则已更新", Body: rule.Name, Time: time.Now().Format("15:04:05")})
	}
	writeResult(w, rule, err)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteRule(r.Context(), r.PathValue("id"))
	if err == nil {
		_ = s.relay.Sync(r.Context())
	}
	writeResult(w, map[string]string{"status": "deleted"}, err)
}

func (s *Server) onlineIPs(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.OnlineIPs(r.Context())
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
	err := s.store.UpsertNode(r.Context(), domain.Node{
		ID:       req.ID,
		Name:     req.ID,
		Region:   "agent",
		Status:   req.Status,
		Load:     req.Load,
		Latency:  req.Latency,
		Traffic:  req.Traffic,
		LastSeen: req.LastSeen,
	})
	writeResult(w, map[string]string{"status": "ok"}, err)
}

func (s *Server) agentRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.EnabledRules(r.Context())
	writeResult(w, rules, err)
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
		if _, ok := s.auth.User(r); !ok {
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

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("request", "method", r.Method, "path", r.URL.Path, "elapsed", time.Since(start))
	})
}

func decodeRuleInput(w http.ResponseWriter, r *http.Request) (domain.RuleInput, bool) {
	var input domain.RuleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return input, false
	}
	if input.Name == "" || input.Listen == "" || input.Target == "" {
		writeError(w, http.StatusBadRequest, errors.New("name, listen and target are required"))
		return input, false
	}
	return input, true
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
