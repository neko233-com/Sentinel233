package api

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/neko233-com/Sentinel233/internal/alert"
	"github.com/neko233-com/Sentinel233/internal/config"
	"github.com/neko233-com/Sentinel233/internal/i18n"
	"github.com/neko233-com/Sentinel233/internal/promql"
	"github.com/neko233-com/Sentinel233/internal/scrape"
	"github.com/neko233-com/Sentinel233/internal/store"
	"github.com/neko233-com/Sentinel233/internal/tsdb"
	"github.com/neko233-com/Sentinel233/internal/version"
)

//go:embed web
var webFS embed.FS

type contextKey string

const tenantIDKey contextKey = "tenant_id"
const userRoleKey contextKey = "user_role"

type Server struct {
	db       *tsdb.DB
	store    *store.Store
	engine   *promql.Engine
	scrape   *scrape.Manager
	alertMgr *alert.Manager
	config   *config.Config
	logger   *slog.Logger
	tokens   map[string]tokenInfo
}

type tokenInfo struct {
	TenantID int64
	Username string
	Role     string
}

func NewServer(db *tsdb.DB, st *store.Store, engine *promql.Engine, scrape *scrape.Manager, alertMgr *alert.Manager, cfg *config.Config, logger *slog.Logger) *Server {
	return &Server{
		db:       db,
		store:    st,
		engine:   engine,
		scrape:   scrape,
		alertMgr: alertMgr,
		config:   cfg,
		logger:   logger,
		tokens:   make(map[string]tokenInfo),
	}
}

func (s *Server) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.loggingMiddleware)
	r.Use(s.corsMiddleware)

	// Public endpoints
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	r.Get("/favicon.ico", s.handleFavicon)
	r.Post("/api/login", s.handleLogin)
	r.Get("/api/i18n/{lang}", s.handleI18n)
	r.Get("/api/v1/status/buildinfo", s.handleStatusBuildinfo)

	// Prometheus ecosystem API (public read for Grafana and Prometheus clients)
	r.Get("/api/v1/query", s.handleQuery)
	r.Post("/api/v1/query", s.handleQuery)
	r.Get("/api/v1/query_range", s.handleQueryRange)
	r.Post("/api/v1/query_range", s.handleQueryRange)
	r.Get("/api/v1/series", s.handleSeries)
	r.Post("/api/v1/series", s.handleSeries)
	r.Get("/api/v1/labels", s.handleLabels)
	r.Post("/api/v1/labels", s.handleLabels)
	r.Get("/api/v1/label/{name}/values", s.handleLabelValues)
	r.Post("/api/v1/label/{name}/values", s.handleLabelValues)
	r.Get("/api/v1/targets", s.handleTargets)
	r.Get("/api/v1/targets/metadata", s.handleTargetsMetadata)
	r.Get("/api/v1/rules", s.handleRules)
	r.Get("/api/v1/alerts", s.handleAlerts)
	r.Get("/api/v1/metadata", s.handleMetadata)
	r.Get("/api/v1/status/tsdb", s.handleStatusTSDB)
	r.Post("/api/v1/write", s.handleRemoteWrite)
	r.Get("/api/v1/alertmanagers", s.handleAlertmanagers)
	r.Get("/api/v1/query_exemplars", s.handleQueryExemplars)
	r.Get("/api/v1/status/config", s.handleStatusConfig)
	r.Get("/api/v1/status/runtime", s.handleStatusRuntime)

	// Sentinel native ingestion API for first-party high-performance clients.
	r.Get("/api/sentinel/v1/capabilities", s.handleSentinelCapabilities)
	r.Post("/api/sentinel/v1/write", s.handleSentinelWrite)

	// Agent-first control plane. Agents register once, then use their issued token for heartbeat and tasks.
	r.Post("/api/agent/v1/register", s.handleAgentRegister)
	r.Route("/api/agent/v1", func(r chi.Router) {
		r.Use(s.agentMiddleware)
		r.Post("/heartbeat", s.handleAgentHeartbeat)
		r.Get("/tasks", s.handleAgentTasks)
		r.Post("/tasks/{id}/complete", s.handleAgentTaskComplete)
	})

	// Loopback-only local agent API for zero-touch dashboard automation.
	r.Route("/api/local/v1", func(r chi.Router) {
		r.Use(s.localAgentMiddleware)
		r.Get("/capabilities", s.handleLocalAgentCapabilities)
		r.Get("/ecosystem/capabilities", s.handleEcosystemCapabilities)
		r.Post("/ecosystem/import", s.handleLocalAgentImportEcosystem)
		r.Get("/dashboards", s.handleListDashboards)
		r.Post("/dashboards", s.handleLocalAgentCreateDashboard)
		r.Post("/dashboards/import", s.handleLocalAgentImportDashboard)
		r.Get("/dashboards/{id}", s.handleGetDashboard)
		r.Put("/dashboards/{id}", s.handleLocalAgentUpdateDashboard)
		r.Post("/dashboards/{id}/panels", s.handleLocalAgentAppendPanel)
	})

	// Tenant-scoped API (requires auth)
	r.Route("/api/tenants", func(r chi.Router) {
		r.Get("/", s.handleListTenants)
		r.Post("/", s.requireRole("admin", s.handleCreateTenant))
		r.Get("/{id}", s.handleGetTenant)
		r.Put("/{id}", s.requireRole("admin", s.handleUpdateTenant))
		r.Delete("/{id}", s.requireRole("admin", s.handleDeleteTenant))
	})

	// Dashboard API (tenant-scoped)
	r.Route("/api/dashboards", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Post("/import", s.requireRole("operator", s.handleImportDashboard))
		r.Get("/", s.handleListDashboards)
		r.Post("/", s.requireRole("operator", s.handleCreateDashboard))
		r.Get("/{id}", s.handleGetDashboard)
		r.Get("/{id}/export", s.requireRole("viewer", s.handleExportDashboard))
		r.Put("/{id}", s.requireRole("operator", s.handleUpdateDashboard))
		r.Delete("/{id}", s.requireRole("admin", s.handleDeleteDashboard))
	})

	// First-class ecosystem import API for Grafana/Prometheus/Alertmanager files.
	r.Route("/api/ecosystem", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/capabilities", s.requireRole("viewer", s.handleEcosystemCapabilities))
		r.Post("/import", s.requireRole("operator", s.handleEcosystemImport))
		r.Post("/alertmanager/webhook", s.handleAlertmanagerWebhookReceiver)
	})

	// Targets API (tenant-scoped)
	r.Route("/api/targets", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/", s.handleGetTargets)
		r.Post("/", s.requireRole("operator", s.handleAddTarget))
		r.Delete("/{id}", s.requireRole("operator", s.handleRemoveTarget))
	})

	r.Route("/api/agents", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/", s.requireRole("viewer", s.handleListAgents))
		r.Post("/{agentID}/tasks", s.requireRole("operator", s.handleCreateAgentTask))
	})

	// Alert rules API (tenant-scoped)
	r.Route("/api/alert-rules", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/", s.handleListAlertRules)
		r.Post("/", s.requireRole("operator", s.handleCreateAlertRule))
		r.Put("/{id}", s.requireRole("operator", s.handleUpdateAlertRule))
		r.Delete("/{id}", s.requireRole("admin", s.handleDeleteAlertRule))
	})

	// Alert history
	r.Get("/api/alerts", s.handleGetAlerts)
	r.Get("/api/alerts/history", s.handleGetAlertHistory)

	// User management API (tenant-scoped)
	r.Route("/api/users", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/", s.requireRole("admin", s.handleListUsers))
		r.Post("/", s.requireRole("admin", s.handleCreateUser))
		r.Put("/{username}/role", s.requireRole("admin", s.handleUpdateUserRole))
		r.Delete("/{username}", s.requireRole("admin", s.handleDeleteUser))
	})

	r.Route("/api/admin", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/config", s.requireRole("admin", s.handleGetAdminConfig))
		r.Put("/config", s.requireRole("admin", s.handleUpdateAdminConfig))
		r.Get("/storage/stats", s.requireRole("admin", s.handleAdminStorageStats))
		r.Get("/storage/export", s.requireRole("admin", s.handleAdminStorageExport))
		r.Post("/storage/import", s.requireRole("admin", s.handleAdminStorageImport))
	})

	// System API
	r.Get("/api/system/stats", s.handleSystemStats)

	// Serve embedded web UI
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		s.logger.Error("failed to load embedded web assets", "err", err)
	} else {
		fileServer := http.FileServer(http.FS(webSub))
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/" || path == "/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				data, err := webSub.(fs.ReadFileFS).ReadFile("index.html")
				if err != nil {
					http.Error(w, "not found", 404)
					return
				}
				w.Write(data)
				return
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	return r
}

// ============ Middleware ============

func (s *Server) tenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := int64(1) // default tenant

		// From X-Tenant-ID header
		if hdr := r.Header.Get("X-Tenant-ID"); hdr != "" {
			if id, err := strconv.ParseInt(hdr, 10, 64); err == nil {
				tenantID = id
			}
		}
		// From query param
		if q := r.URL.Query().Get("tenant_id"); q != "" {
			if id, err := strconv.ParseInt(q, 10, 64); err == nil {
				tenantID = id
			}
		}
		// From token
		if token := extractToken(r); token != "" {
			if info, ok := s.tokens[token]; ok {
				tenantID = info.TenantID
			}
		}

		ctx := context.WithValue(r.Context(), tenantIDKey, tenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireRole(minRole string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			s.jsonError(w, "authentication required", http.StatusUnauthorized)
			return
		}
		info, ok := s.tokens[token]
		if !ok {
			s.jsonError(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if !roleAtLeast(info.Role, minRole) {
			s.jsonError(w, "insufficient permissions", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), tenantIDKey, info.TenantID)
		ctx = context.WithValue(ctx, userRoleKey, info.Role)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *Server) localAgentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config == nil || !s.config.LocalAPI.Enabled {
			s.jsonError(w, "local agent api disabled", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(strings.TrimSpace(host))
		if ip == nil || !ip.IsLoopback() {
			s.jsonError(w, "local agent api is only available from loopback addresses", http.StatusForbidden)
			return
		}
		tenantID := s.config.LocalAPI.TenantID
		if tenantID <= 0 {
			tenantID = 1
		}
		ctx := context.WithValue(r.Context(), tenantIDKey, tenantID)
		ctx = context.WithValue(ctx, userRoleKey, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) agentMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			s.jsonError(w, "agent token required", http.StatusUnauthorized)
			return
		}
		agent, err := s.store.GetAgentByToken(token)
		if err != nil {
			s.jsonError(w, "invalid agent token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), tenantIDKey, agent.TenantID)
		ctx = context.WithValue(ctx, contextKey("agent_id"), agent.AgentID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) getTenantID(r *http.Request) int64 {
	if v, ok := r.Context().Value(tenantIDKey).(int64); ok && v > 0 {
		return v
	}
	return 1
}

func getAgentID(r *http.Request) string {
	if v, ok := r.Context().Value(contextKey("agent_id")).(string); ok {
		return v
	}
	return ""
}

func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func (s *Server) verifyAgentEnrollmentToken(w http.ResponseWriter, r *http.Request, provided string) bool {
	expected := ""
	if s.config != nil {
		expected = strings.TrimSpace(s.config.Agent.EnrollmentToken)
	}
	if expected == "" {
		return true
	}
	candidate := strings.TrimSpace(provided)
	if candidate == "" {
		candidate = strings.TrimSpace(r.Header.Get("X-Sentinel-Agent-Token"))
	}
	if candidate == "" {
		candidate = strings.TrimSpace(r.URL.Query().Get("enrollment_token"))
	}
	if candidate != expected {
		s.jsonError(w, "invalid agent enrollment token", http.StatusUnauthorized)
		return false
	}
	return true
}

func roleAtLeast(userRole, minRole string) bool {
	levels := map[string]int{"viewer": 0, "operator": 1, "admin": 2}
	return levels[userRole] >= levels[minRole]
}

// ============ Auth ============

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#34d399"/><text x="32" y="40" text-anchor="middle" font-family="Arial" font-size="30" font-weight="700" fill="#06110d">S</text></svg>`))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Tenant   string `json:"tenant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}

	tenantID := int64(1)
	if req.Tenant != "" {
		t, err := s.store.GetTenantBySlug(req.Tenant)
		if err != nil {
			s.jsonError(w, "tenant not found", 404)
			return
		}
		tenantID = t.ID
	}

	_, hash, role, err := s.store.GetUser(tenantID, req.Username)
	if err != nil {
		s.jsonError(w, i18n.T("en-US", "auth.login_failed"), 401)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		s.jsonError(w, i18n.T("en-US", "auth.login_failed"), 401)
		return
	}

	token := fmt.Sprintf("s233_%x_%d_%d", time.Now().UnixNano(), tenantID, time.Now().UnixMilli()%10000)
	s.tokens[token] = tokenInfo{TenantID: tenantID, Username: req.Username, Role: role}

	s.jsonOK(w, map[string]interface{}{
		"status":   "success",
		"token":    token,
		"tenantId": tenantID,
		"role":     role,
		"username": req.Username,
	})
}

func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantID        int64             `json:"tenant_id"`
		AgentID         string            `json:"agent_id"`
		Name            string            `json:"name"`
		Hostname        string            `json:"hostname"`
		Version         string            `json:"version"`
		ListenAddr      string            `json:"listen_addr"`
		EnrollmentToken string            `json:"enrollment_token"`
		Labels          map[string]string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid agent registration payload", http.StatusBadRequest)
		return
	}
	if !s.verifyAgentEnrollmentToken(w, r, req.EnrollmentToken) {
		return
	}
	tenantID := req.TenantID
	if tenantID <= 0 {
		tenantID = 1
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		agentID = firstNonEmptyString(req.Hostname, req.Name)
	}
	if agentID == "" {
		s.jsonError(w, "agent_id or hostname is required", http.StatusBadRequest)
		return
	}
	token, err := randomToken("agt_")
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agent := &store.Agent{
		TenantID:   tenantID,
		AgentID:    agentID,
		Name:       firstNonEmptyString(req.Name, agentID),
		Hostname:   req.Hostname,
		Version:    req.Version,
		ListenAddr: req.ListenAddr,
		Token:      token,
		Labels:     req.Labels,
		Status:     "online",
	}
	if err := s.store.UpsertAgent(agent); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{
		"agent": agent,
		"token": token,
		"endpoints": map[string]string{
			"heartbeat": "/api/agent/v1/heartbeat",
			"tasks":     "/api/agent/v1/tasks",
			"write":     "/api/sentinel/v1/write",
		},
	}})
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	agentID := getAgentID(r)
	var req struct {
		Version    string             `json:"version"`
		ListenAddr string             `json:"listen_addr"`
		Labels     map[string]string  `json:"labels"`
		Metrics    map[string]float64 `json:"metrics"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Labels == nil {
		req.Labels = map[string]string{}
	}
	if err := s.store.TouchAgent(tenantID, agentID, req.Version, req.ListenAddr, req.Labels); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for name, value := range req.Metrics {
		labels := map[string]string{"agent_id": agentID, "source": "sentinel_agent"}
		if err := s.db.Append(labelsMapToTSDB(metricLabels(name, labels)), time.Now().UnixMilli(), value); err != nil {
			s.jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"agentId": agentID}})
}

func (s *Server) handleAgentTasks(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	agentID := getAgentID(r)
	limit := intFromParam(r.URL.Query().Get("limit"), 10)
	tasks, err := s.store.ClaimAgentTasks(tenantID, agentID, limit)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": tasks})
}

func (s *Server) handleAgentTaskComplete(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	agentID := getAgentID(r)
	taskID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var req struct {
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid task completion payload", http.StatusBadRequest)
		return
	}
	if err := s.store.CompleteAgentTask(tenantID, agentID, taskID, req.Result, req.Error); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.store.ListAgents(s.getTenantID(r))
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": agents})
}

func (s *Server) handleCreateAgentTask(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	agentID := chi.URLParam(r, "agentID")
	var req struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid task payload", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		s.jsonError(w, "task type is required", http.StatusBadRequest)
		return
	}
	payload := strings.TrimSpace(string(req.Payload))
	if payload == "" {
		payload = "{}"
	}
	task := &store.AgentTask{TenantID: tenantID, AgentID: agentID, Type: req.Type, Payload: payload}
	if err := s.store.CreateAgentTask(task); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": task})
}

func randomToken(prefix string) (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf[:]), nil
}

func metricLabels(metric string, labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out["__name__"] = metric
	return out
}

// ============ Tenant handlers ============

func (s *Server) handleListTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := s.store.ListTenants()
	if err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": tenants})
}

func (s *Server) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	var t store.Tenant
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	if t.Slug == "" || t.Name == "" {
		s.jsonError(w, "slug and name required", 400)
		return
	}
	if err := s.store.CreateTenant(&t); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": t})
}

func (s *Server) handleGetTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	t, err := s.store.GetTenant(id)
	if err != nil {
		s.jsonError(w, "tenant not found", 404)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": t})
}

func (s *Server) handleUpdateTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var t store.Tenant
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	t.ID = id
	if err := s.store.UpdateTenant(&t); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": t})
}

func (s *Server) handleDeleteTenant(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.store.DeleteTenant(id); err != nil {
		s.jsonError(w, err.Error(), 400)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

// ============ Dashboard handlers ============

func (s *Server) handleListDashboards(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	dashboards, err := s.store.ListDashboards(tenantID)
	if err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": dashboards})
}

func (s *Server) handleCreateDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	var d store.Dashboard
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	d.TenantID = tenantID
	normalizeDashboardRecord(&d)
	if err := s.store.CreateDashboard(&d); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
}

func (s *Server) handleImportDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.jsonError(w, "invalid import payload", 400)
		return
	}
	if nested, ok := payload["dashboard"]; ok {
		var nestedMap map[string]json.RawMessage
		if err := json.Unmarshal(nested, &nestedMap); err == nil {
			payload = nestedMap
		}
	}

	source := strings.TrimSpace(strings.ToLower(stringFromRaw(payload["source"])))
	var d *store.Dashboard
	var err error
	if source == "grafana" || looksLikeGrafanaDashboard(payload) {
		d, err = convertGrafanaPayloadToDashboard(payload)
	} else {
		d, err = convertDashboardPayload(payload)
	}
	if err != nil {
		s.jsonError(w, err.Error(), 400)
		return
	}
	d.TenantID = tenantID
	normalizeDashboardRecord(d)
	if err := s.store.CreateDashboard(d); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
}

func (s *Server) handleExportDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	dash, err := s.store.GetDashboard(tenantID, id)
	if err != nil {
		s.jsonError(w, "dashboard not found", 404)
		return
	}

	exported, err := convertDashboardToGrafanaExport(dash)
	if err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": exported})
}

func (s *Server) handleGetDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	d, err := s.store.GetDashboard(tenantID, id)
	if err != nil {
		s.jsonError(w, "dashboard not found", 404)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
}

func (s *Server) handleUpdateDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var d store.Dashboard
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	d.ID = id
	d.TenantID = tenantID
	normalizeDashboardRecord(&d)
	if err := s.store.UpdateDashboard(&d); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
}

func (s *Server) handleLocalAgentCapabilities(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"mode":                    "loopback-only",
			"tenantId":                s.getTenantID(r),
			"dashboardImport":         true,
			"dashboardCreate":         true,
			"dashboardUpdate":         true,
			"dashboardAppendPanel":    true,
			"grafanaIntegration":      true,
			"queryModes":              []string{"promql", "sql"},
			"renderers":               []string{"auto", "chartjs", "echarts", "table"},
			"intendedAutomationUsers": []string{"local-agent", "codex", "assistant-runtime"},
		},
	})
}

func (s *Server) handleLocalAgentCreateDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	d, err := convertDashboardPayload(payload)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	d.TenantID = tenantID
	normalizeDashboardRecord(d)
	if err := s.store.CreateDashboard(d); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
}

func (s *Server) handleLocalAgentImportDashboard(w http.ResponseWriter, r *http.Request) {
	s.handleImportDashboard(w, r)
}

func (s *Server) handleLocalAgentUpdateDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	d, err := convertDashboardPayload(payload)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	d.ID = id
	d.TenantID = tenantID
	normalizeDashboardRecord(d)
	if err := s.store.UpdateDashboard(d); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
}

func (s *Server) handleLocalAgentAppendPanel(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	dash, err := s.store.GetDashboard(tenantID, id)
	if err != nil {
		s.jsonError(w, "dashboard not found", http.StatusNotFound)
		return
	}
	var incoming map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		s.jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	var panels []map[string]interface{}
	if err := json.Unmarshal([]byte(dash.Panels), &panels); err != nil || panels == nil {
		panels = []map[string]interface{}{}
	}
	panel := normalizeDashboardPanel(incoming, len(panels))
	panels = append(panels, panel)
	panelsJSON, err := json.Marshal(panels)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dash.Panels = string(panelsJSON)
	if err := s.store.UpdateDashboard(dash); err != nil {
		s.jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"dashboard": dash,
			"panel":     panel,
		},
	})
}

func (s *Server) handleDeleteDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.store.DeleteDashboard(tenantID, id); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

type grafanaImportTarget map[string]interface{}

type grafanaImportVariable struct {
	Name       string      `json:"name"`
	Label      string      `json:"label"`
	Type       string      `json:"type"`
	Query      string      `json:"query"`
	Current    interface{} `json:"current"`
	Options    interface{} `json:"options"`
	IncludeAll bool        `json:"includeAll"`
	Multi      bool        `json:"multi"`
}

type grafanaImportPanel struct {
	ID              int                    `json:"id"`
	Type            string                 `json:"type"`
	Title           string                 `json:"title"`
	Targets         []grafanaImportTarget  `json:"targets"`
	GridPos         map[string]interface{} `json:"gridPos"`
	Datasource      interface{}            `json:"datasource"`
	FieldConfig     map[string]interface{} `json:"fieldConfig"`
	Options         map[string]interface{} `json:"options"`
	Legend          string                 `json:"legend"`
	Transformations []interface{}          `json:"transformations"`
	Panels          []grafanaImportPanel   `json:"panels"`
}

type grafanaImportPayload struct {
	Title         string                 `json:"title"`
	Description   string                 `json:"description"`
	UID           string                 `json:"uid"`
	SchemaVersion int                    `json:"schemaVersion"`
	Tags          []string               `json:"tags"`
	Time          map[string]interface{} `json:"time"`
	Panels        []grafanaImportPanel   `json:"panels"`
	Templating    struct {
		List []grafanaImportVariable `json:"list"`
	} `json:"templating"`
}

var grafanaPanelTypeMap = map[string]string{
	"graph":      "timeseries",
	"timeseries": "timeseries",
	"stat":       "stat",
	"gauge":      "gauge",
	"table":      "table",
	"barchart":   "bar",
	"bar":        "bar",
	"bargauge":   "bar",
	"heatmap":    "heatmap",
}

var reverseGrafanaTypeMap = map[string]string{
	"timeseries": "timeseries",
	"stat":       "stat",
	"gauge":      "gauge",
	"table":      "table",
	"bar":        "barchart",
	"histogram":  "histogram",
	"heatmap":    "heatmap",
}

func looksLikeGrafanaDashboard(payload map[string]json.RawMessage) bool {
	_, hasSchema := payload["schemaVersion"]
	_, hasTemplating := payload["templating"]
	_, hasPanels := payload["panels"]
	if !hasPanels {
		return false
	}
	if hasSchema || hasTemplating {
		return true
	}
	var panelValue interface{}
	if err := json.Unmarshal(payload["panels"], &panelValue); err == nil {
		if _, ok := panelValue.([]interface{}); ok {
			return true
		}
	}
	return false
}

func convertDashboardPayload(payload map[string]json.RawMessage) (*store.Dashboard, error) {
	title := strings.TrimSpace(stringFromRaw(payload["title"]))
	if title == "" {
		title = "Imported Dashboard"
	}
	description := strings.TrimSpace(stringFromRaw(payload["description"]))
	panelsJSON := ensureJSONString(payload["panels"], "[]")
	layoutJSON := ensureJSONString(payload["layout"], "{}")
	variablesJSON := ensureJSONString(payload["variables"], "[]")
	tagsJSON := ensureJSONString(payload["tags"], "[]")
	return &store.Dashboard{
		Title:       title,
		Description: description,
		Panels:      panelsJSON,
		Layout:      layoutJSON,
		Variables:   variablesJSON,
		Tags:        tagsJSON,
	}, nil
}

func convertGrafanaPayloadToDashboard(payload map[string]json.RawMessage) (*store.Dashboard, error) {
	var gDashboard grafanaImportPayload
	if err := json.Unmarshal(toJSONBytes(payload), &gDashboard); err != nil {
		return nil, err
	}
	if strings.TrimSpace(gDashboard.Title) == "" {
		gDashboard.Title = "Imported Grafana Dashboard"
	}
	integration := buildGrafanaIntegrationReport(gDashboard.Panels)
	integrationByID := make(map[int]map[string]interface{}, len(integration.Panels))
	for _, panel := range integration.Panels {
		integrationByID[panel.ID] = panel.toMap()
	}
	convertedPanels := make([]map[string]interface{}, 0)
	for idx, panel := range flattenGrafanaPanels(gDashboard.Panels) {
		target := firstGrafanaTarget(panel.Targets)
		panelType := mapGrafanaPanelType(panel.Type)
		unit := ""
		panelID := coerceInt(panel.ID, idx+1)
		datasource := cloneObject(panel.Datasource)
		if datasource == nil && target != nil {
			datasource = cloneObject((*target)["datasource"])
		}
		thresholds := interface{}([]interface{}{})
		if panel.FieldConfig != nil {
			if defaults, ok := panel.FieldConfig["defaults"].(map[string]interface{}); ok {
				if v, ok := defaults["unit"].(string); ok {
					unit = v
				}
				if v, ok := defaults["thresholds"].(map[string]interface{}); ok {
					if s, ok := v["steps"]; ok {
						thresholds = s
					}
				}
			}
		}
		legend := panel.Legend
		if legend == "" && target != nil {
			legend = grafanaTargetString(target, "legendFormat", "alias")
		}
		query := ""
		if target != nil {
			query = grafanaTargetString(target, "expr", "query")
		}
		convertedPanels = append(convertedPanels, map[string]interface{}{
			"id":          panelID,
			"title":       panelTitle(panel.Title, "Grafana Panel"),
			"type":        panelType,
			"queryType":   "promql",
			"query":       query,
			"sourceQuery": query,
			"datasource":  datasource,
			"legend":      legend,
			"unit":        unit,
			"thresholds":  thresholds,
			"renderer":    firstNonEmptyString(panelRenderer(panelType), "chartjs"),
			"options":     cloneObject(panel.Options),
			"fieldConfig": cloneObject(panel.FieldConfig),
			"layout":      cloneObject(panel.GridPos),
			"grafana": map[string]interface{}{
				"id":              panelID,
				"type":            panel.Type,
				"targets":         cloneTargets(panel.Targets),
				"transformations": cloneObject(panel.Transformations),
				"integration":     integrationByID[panelID],
			},
		})
	}
	panelsJSON, err := json.Marshal(convertedPanels)
	if err != nil {
		return nil, err
	}
	variables := make([]map[string]interface{}, 0, len(gDashboard.Templating.List))
	for _, variable := range gDashboard.Templating.List {
		variables = append(variables, map[string]interface{}{
			"name":       variable.Name,
			"label":      variable.Label,
			"type":       variable.Type,
			"query":      variable.Query,
			"current":    variable.Current,
			"options":    variable.Options,
			"includeAll": variable.IncludeAll,
			"multi":      variable.Multi,
		})
	}
	variablesJSON, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}
	layout := map[string]interface{}{
		"schemaVersion": gDashboard.SchemaVersion,
		"uid":           gDashboard.UID,
		"time":          gDashboard.Time,
		"integration":   integration.toMap(),
	}
	if len(layout) == 0 || isEmptyLayout(layout) {
		layout = map[string]interface{}{}
	}
	layoutJSON, err := json.Marshal(layout)
	if err != nil {
		return nil, err
	}
	tags := gDashboard.Tags
	if len(tags) == 0 {
		tags = []string{"grafana-import"}
	}
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, err
	}
	description := strings.TrimSpace(gDashboard.Description)
	if description == "" {
		description = "Imported from Grafana dashboard"
	}
	return &store.Dashboard{
		Title:       gDashboard.Title,
		Description: description,
		Panels:      string(panelsJSON),
		Layout:      string(layoutJSON),
		Variables:   string(variablesJSON),
		Tags:        string(tagsJSON),
	}, nil
}

func convertDashboardToGrafanaExport(dash *store.Dashboard) (map[string]interface{}, error) {
	var layout map[string]interface{}
	if err := json.Unmarshal([]byte(dash.Layout), &layout); err != nil || layout == nil {
		layout = map[string]interface{}{}
	}
	var panels []map[string]interface{}
	if err := json.Unmarshal([]byte(dash.Panels), &panels); err != nil {
		panels = []map[string]interface{}{}
	}
	var variables []map[string]interface{}
	if err := json.Unmarshal([]byte(dash.Variables), &variables); err != nil {
		variables = []map[string]interface{}{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(dash.Tags), &tags); err != nil {
		tags = []string{}
	}
	exportPanels := make([]map[string]interface{}, 0, len(panels))
	for index, panel := range panels {
		panelType := panel["grafana"]
		grafanaType := mapGrafanaTypeForExport(panelType, getString(panel["type"]))
		gridPos := panel["layout"]
		query := panelSourceQuery(panel)
		targets := extractGrafanaTargets(panel["grafana"], query)
		if len(targets) == 0 && query != "" {
			targets = append(targets, map[string]interface{}{
				"refId":        "A",
				"expr":         query,
				"legendFormat": getString(panel["legend"]),
			})
		}
		if len(targets) == 0 {
			targets = []map[string]interface{}{{"refId": "A", "expr": query}}
		}
		rawGrafana, _ := panelType.(map[string]interface{})
		transformations := extractTransformations(rawGrafana)
		if transformations == nil {
			transformations = extractTransformations(panel["transformations"])
		}
		exportPanels = append(exportPanels, map[string]interface{}{
			"id":              coerceInt(panel["id"], index+1),
			"title":           getString(panel["title"]),
			"type":            grafanaType,
			"gridPos":         normalizeGridPos(gridPos, index),
			"datasource":      panel["datasource"],
			"fieldConfig":     panelFieldConfig(panel["fieldConfig"], getString(panel["unit"])),
			"options":         cloneObject(panel["options"]),
			"targets":         targets,
			"transformations": transformations,
		})
	}
	export := map[string]interface{}{
		"title":         dash.Title,
		"uid":           getString(layout["uid"]),
		"schemaVersion": layoutInt(layout, "schemaVersion", 39),
		"tags":          coalesceTags(tags),
		"timezone":      "browser",
		"time":          firstNonNil(layout["time"], map[string]interface{}{"from": "now-24h", "to": "now"}),
		"templating":    map[string]interface{}{"list": variables},
		"panels":        exportPanels,
	}
	if getString(export["uid"]) == "" {
		export["uid"] = fmt.Sprintf("sentinel-%d", dash.ID)
	}
	return export, nil
}

func mapGrafanaPanelType(rawType string) string {
	raw := strings.ToLower(strings.TrimSpace(rawType))
	if mapped, ok := grafanaPanelTypeMap[raw]; ok {
		return mapped
	}
	if raw == "" {
		return "timeseries"
	}
	return raw
}

func mapGrafanaTypeForExport(grafanaRaw interface{}, fallback string) string {
	grafanaType := ""
	if m, ok := grafanaRaw.(map[string]interface{}); ok {
		grafanaType = getString(m["type"])
	}
	if grafanaType == "" {
		grafanaType = fallback
	}
	if mapped, ok := reverseGrafanaTypeMap[strings.ToLower(strings.TrimSpace(grafanaType))]; ok {
		return mapped
	}
	return strings.ToLower(strings.TrimSpace(grafanaType))
}

type grafanaIntegrationReport struct {
	TotalPanels      int
	FullySupported   int
	PartiallySupport int
	Unsupported      int
	TotalWarnings    int
	Panels           []grafanaPanelIntegration
}

type grafanaPanelIntegration struct {
	ID          int
	Title       string
	GrafanaType string
	MappedType  string
	Warnings    []string
}

func buildGrafanaIntegrationReport(panels []grafanaImportPanel) grafanaIntegrationReport {
	flattened := flattenGrafanaPanels(panels)
	report := grafanaIntegrationReport{
		TotalPanels: len(flattened),
		Panels:      make([]grafanaPanelIntegration, 0, len(flattened)),
	}
	for idx, panel := range flattened {
		item := buildGrafanaPanelIntegration(panel, idx)
		if len(item.Warnings) == 0 {
			report.FullySupported++
		} else if item.MappedType != "" {
			report.PartiallySupport++
		} else {
			report.Unsupported++
		}
		report.TotalWarnings += len(item.Warnings)
		report.Panels = append(report.Panels, item)
	}
	return report
}

func buildGrafanaPanelIntegration(panel grafanaImportPanel, index int) grafanaPanelIntegration {
	mappedType := mapGrafanaPanelType(panel.Type)
	item := grafanaPanelIntegration{
		ID:          coerceInt(panel.ID, index+1),
		Title:       panelTitle(panel.Title, "Grafana Panel"),
		GrafanaType: firstNonEmptyString(panel.Type, "unknown"),
		MappedType:  mappedType,
		Warnings:    []string{},
	}
	if _, ok := grafanaPanelTypeMap[strings.ToLower(strings.TrimSpace(panel.Type))]; !ok {
		if !isBuiltInSentinelPanelType(mappedType) {
			item.Warnings = append(item.Warnings, fmt.Sprintf("panel type %q requires manual verification", item.GrafanaType))
		}
	}
	if len(panel.Targets) > 1 {
		item.Warnings = append(item.Warnings, "multiple targets detected; Sentinel preserves all targets and renders PromQL targets best-effort")
	}
	if len(panel.Transformations) > 0 {
		item.Warnings = append(item.Warnings, "grafana transformations are preserved as metadata and may need manual recreation")
	}
	if _, ok := panel.Datasource.(map[string]interface{}); ok {
		item.Warnings = append(item.Warnings, "datasource uses a complex object and should be reviewed after import")
	}
	return item
}

func (r grafanaIntegrationReport) toMap() map[string]interface{} {
	panels := make([]map[string]interface{}, 0, len(r.Panels))
	for _, panel := range r.Panels {
		panels = append(panels, panel.toMap())
	}
	return map[string]interface{}{
		"totalPanels":        r.TotalPanels,
		"fullySupported":     r.FullySupported,
		"partiallySupported": r.PartiallySupport,
		"unsupported":        r.Unsupported,
		"totalWarnings":      r.TotalWarnings,
		"panels":             panels,
	}
}

func (p grafanaPanelIntegration) toMap() map[string]interface{} {
	return map[string]interface{}{
		"id":          p.ID,
		"title":       p.Title,
		"grafanaType": p.GrafanaType,
		"mappedType":  p.MappedType,
		"warnings":    p.Warnings,
	}
}

func panelSourceQuery(panel map[string]interface{}) string {
	if strings.EqualFold(getString(panel["queryType"]), "sql") {
		if source := getString(panel["sourceQuery"]); source != "" {
			return source
		}
	}
	if source := getString(panel["sourceQuery"]); source != "" && getString(panel["query"]) == "" {
		return source
	}
	return getString(panel["query"])
}

func panelRenderer(panelType string) string {
	switch strings.ToLower(strings.TrimSpace(panelType)) {
	case "table":
		return "table"
	case "heatmap", "pie", "scatter":
		return "echarts"
	default:
		return "chartjs"
	}
}

func isBuiltInSentinelPanelType(panelType string) bool {
	switch strings.ToLower(strings.TrimSpace(panelType)) {
	case "timeseries", "stat", "gauge", "table", "bar", "pie", "scatter", "heatmap":
		return true
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeDashboardPanel(panel map[string]interface{}, index int) map[string]interface{} {
	if panel == nil {
		panel = map[string]interface{}{}
	}
	queryType := strings.ToLower(strings.TrimSpace(getString(panel["queryType"])))
	if queryType == "" {
		if sourceQuery := getString(panel["sourceQuery"]); sourceQuery != "" && sourceQuery != getString(panel["query"]) {
			queryType = "sql"
		} else {
			queryType = "promql"
		}
	}
	query := getString(panel["query"])
	sourceQuery := getString(panel["sourceQuery"])
	if queryType == "promql" && sourceQuery == "" {
		sourceQuery = query
	}
	if queryType == "sql" && sourceQuery == "" {
		sourceQuery = query
	}
	options, _ := cloneObject(panel["options"]).(map[string]interface{})
	if options == nil {
		options = map[string]interface{}{}
	}
	fieldConfig, _ := cloneObject(panel["fieldConfig"]).(map[string]interface{})
	if fieldConfig == nil {
		fieldConfig = map[string]interface{}{}
	}
	layout, ok := cloneObject(panel["layout"]).(map[string]interface{})
	if !ok || layout == nil {
		layout = map[string]interface{}{
			"x": (index % 2) * 6,
			"y": (index / 2) * 8,
			"w": 6,
			"h": 8,
		}
	}
	grafanaMeta, _ := cloneObject(panel["grafana"]).(map[string]interface{})
	renderer := getString(panel["renderer"])
	if renderer == "" {
		renderer = panelRenderer(getString(panel["type"]))
	}
	return map[string]interface{}{
		"id":          coerceInt(panel["id"], index+1),
		"title":       firstNonEmptyString(getString(panel["title"]), fmt.Sprintf("Panel %d", index+1)),
		"description": getString(panel["description"]),
		"type":        firstNonEmptyString(getString(panel["type"]), "timeseries"),
		"queryType":   firstNonEmptyString(queryType, "promql"),
		"query":       query,
		"sourceQuery": sourceQuery,
		"datasource":  cloneObject(panel["datasource"]),
		"legend":      getString(panel["legend"]),
		"unit":        getString(panel["unit"]),
		"thresholds":  cloneObject(panel["thresholds"]),
		"renderer":    renderer,
		"options":     options,
		"fieldConfig": fieldConfig,
		"layout":      layout,
		"grafana":     grafanaMeta,
	}
}

func normalizeDashboardRecord(d *store.Dashboard) {
	if d == nil {
		return
	}
	var panels []map[string]interface{}
	if err := json.Unmarshal([]byte(d.Panels), &panels); err == nil && panels != nil {
		normalized := make([]map[string]interface{}, 0, len(panels))
		for index, panel := range panels {
			normalized = append(normalized, normalizeDashboardPanel(panel, index))
		}
		if bytes, err := json.Marshal(normalized); err == nil {
			d.Panels = string(bytes)
		}
	}
	if strings.TrimSpace(d.Layout) == "" {
		d.Layout = "{}"
	}
	if strings.TrimSpace(d.Variables) == "" {
		d.Variables = "[]"
	}
	if strings.TrimSpace(d.Tags) == "" {
		d.Tags = "[]"
	}
}

func cloneObject(value interface{}) interface{} {
	if value == nil {
		return nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned interface{}
	if err := json.Unmarshal(bytes, &cloned); err != nil {
		return value
	}
	return cloned
}

func cloneTargets(targets []grafanaImportTarget) []grafanaImportTarget {
	cloned := make([]grafanaImportTarget, 0, len(targets))
	for _, target := range targets {
		clone := grafanaImportTarget{}
		for key, value := range target {
			clone[key] = cloneObject(value)
		}
		cloned = append(cloned, clone)
	}
	return cloned
}

func firstGrafanaTarget(targets []grafanaImportTarget) *grafanaImportTarget {
	if len(targets) == 0 {
		return nil
	}
	return &targets[0]
}

func grafanaTargetString(target *grafanaImportTarget, keys ...string) string {
	if target == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(getString((*target)[key])); value != "" {
			return value
		}
	}
	return ""
}

func panelTitle(title, fallback string) string {
	if strings.TrimSpace(title) == "" {
		return fallback
	}
	return title
}

func flattenGrafanaPanels(panels []grafanaImportPanel) []grafanaImportPanel {
	var result []grafanaImportPanel
	for _, panel := range panels {
		if strings.EqualFold(panel.Type, "row") && len(panel.Panels) > 0 {
			result = append(result, flattenGrafanaPanels(panel.Panels)...)
			continue
		}
		result = append(result, panel)
	}
	return result
}

func extractTransformations(panel interface{}) []map[string]interface{} {
	if panel == nil {
		return nil
	}
	switch source := panel.(type) {
	case map[string]interface{}:
		transformRaw, ok := source["transformations"]
		if !ok {
			return nil
		}
		return parseTransformationsValue(transformRaw)
	default:
		return parseTransformationsValue(source)
	}
}

func parseTransformationsValue(raw interface{}) []map[string]interface{} {
	switch source := raw.(type) {
	case []interface{}:
		result := make([]map[string]interface{}, 0, len(source))
		for _, item := range source {
			transform, ok := item.(map[string]interface{})
			if ok {
				cloned := cloneObject(transform)
				if cloneMap, ok := cloned.(map[string]interface{}); ok {
					result = append(result, cloneMap)
				}
			}
		}
		return result
	default:
		return nil
	}
}

func extractGrafanaTargets(grafanaRaw interface{}, query string) []map[string]interface{} {
	var targets []map[string]interface{}
	grafanaMap, ok := grafanaRaw.(map[string]interface{})
	if !ok || grafanaMap == nil {
		return targets
	}
	raw, ok := grafanaMap["targets"]
	if !ok {
		return targets
	}
	switch source := raw.(type) {
	case []interface{}:
		for _, item := range source {
			if target, ok := item.(map[string]interface{}); ok {
				targets = append(targets, target)
			}
		}
	}
	if len(targets) > 0 {
		return targets
	}
	if strings.TrimSpace(query) != "" {
		targets = append(targets, map[string]interface{}{"expr": query, "refId": "A"})
	}
	return targets
}

func coerceInt(v interface{}, fallback int) int {
	switch num := v.(type) {
	case int:
		return num
	case int8:
		return int(num)
	case int16:
		return int(num)
	case int32:
		return int(num)
	case int64:
		return int(num)
	case uint:
		return int(num)
	case uint8:
		return int(num)
	case uint16:
		return int(num)
	case uint32:
		return int(num)
	case uint64:
		return int(num)
	case float32:
		return int(num)
	case float64:
		return int(num)
	case json.Number:
		if n, err := num.Int64(); err == nil {
			return int(n)
		}
		return fallback
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(num)); err == nil {
			return parsed
		}
		return fallback
	default:
		return fallback
	}
}

func panelFieldConfig(raw interface{}, unit string) map[string]interface{} {
	cfg := cloneObject(raw)
	config, ok := cfg.(map[string]interface{})
	if !ok {
		config = map[string]interface{}{}
	}
	if _, ok := config["defaults"]; !ok {
		if unit != "" {
			config["defaults"] = map[string]interface{}{
				"unit": unit,
			}
		}
	}
	return config
}

func normalizeGridPos(raw interface{}, index int) map[string]interface{} {
	if grid, ok := raw.(map[string]interface{}); ok {
		return grid
	}
	return map[string]interface{}{
		"x": (index % 2) * 12,
		"y": (index / 2) * 8,
		"w": 12,
		"h": 8,
	}
}

func layoutInt(layout map[string]interface{}, key string, defaultValue int) int {
	switch v := layout[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	default:
		return defaultValue
	}
	return defaultValue
}

func coalesceTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{"sentinel-export"}
	}
	return tags
}

func stringFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func ensureJSONString(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 {
		return fallback
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fallback
	}
	switch v := decoded.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return fallback
		}
		return v
	default:
		bytes, err := json.Marshal(decoded)
		if err != nil {
			return fallback
		}
		return string(bytes)
	}
}

func toJSONBytes(payload map[string]json.RawMessage) []byte {
	data, _ := json.Marshal(payload)
	return data
}

func getString(v interface{}) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case int:
		return strconv.Itoa(value)
	default:
		return fmt.Sprint(v)
	}
}

func firstNonNil(v interface{}, fallback map[string]interface{}) map[string]interface{} {
	if value, ok := v.(map[string]interface{}); ok && value != nil {
		return value
	}
	return fallback
}

func isEmptyLayout(layout map[string]interface{}) bool {
	return len(layout) == 0
}

// ============ Targets handlers ============

func (s *Server) handleGetTargets(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	targets, err := s.store.ListScrapeTargets(tenantID)
	if err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	// Merge runtime scrape status
	runtimeTargets := s.scrape.GetTargets()
	statusMap := make(map[string]bool)
	for _, rt := range runtimeTargets {
		statusMap[rt.Endpoint] = rt.Healthy
	}
	var result []map[string]interface{}
	for _, t := range targets {
		healthy := statusMap[t.Endpoint]
		result = append(result, map[string]interface{}{
			"id":       t.ID,
			"name":     t.Name,
			"endpoint": t.Endpoint,
			"labels":   t.Labels,
			"healthy":  healthy,
			"enabled":  t.Enabled,
		})
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": result})
}

func (s *Server) handleAddTarget(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	var t store.ScrapeTarget
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	t.TenantID = tenantID
	t.Enabled = true
	if err := s.store.CreateScrapeTarget(&t); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.scrape.AddTarget(t.Name, t.Endpoint, t.Labels)
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": t})
}

func (s *Server) handleRemoveTarget(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	// Find target endpoint for runtime removal
	targets, _ := s.store.ListScrapeTargets(tenantID)
	for _, t := range targets {
		if t.ID == id {
			s.scrape.RemoveTarget(t.Endpoint)
			break
		}
	}
	if err := s.store.DeleteScrapeTarget(tenantID, id); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

// ============ Alert Rules handlers ============

func (s *Server) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	rules, err := s.store.ListAlertRules(tenantID)
	if err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": rules})
}

func (s *Server) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	var rule store.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	rule.TenantID = tenantID
	rule.Enabled = true
	if err := s.store.CreateAlertRule(&rule); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": rule})
}

func (s *Server) handleUpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var rule store.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	rule.ID = id
	rule.TenantID = tenantID
	if err := s.store.UpdateAlertRule(&rule); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": rule})
}

func (s *Server) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := s.store.DeleteAlertRule(tenantID, id); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

// ============ User handlers ============

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	users, err := s.store.ListUsers(tenantID)
	if err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		s.jsonError(w, "internal error", 500)
		return
	}
	u := &store.User{
		TenantID:     tenantID,
		Username:     req.Username,
		PasswordHash: string(hash),
		Role:         req.Role,
	}
	if err := s.store.CreateUser(u); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{
		"id": u.ID, "username": u.Username, "role": u.Role,
	}})
}

func (s *Server) handleUpdateUserRole(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	username := chi.URLParam(r, "username")
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid request", 400)
		return
	}
	if err := s.store.UpdateUserRole(tenantID, username, req.Role); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	tenantID := s.getTenantID(r)
	username := chi.URLParam(r, "username")
	if err := s.store.DeleteUser(tenantID, username); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success"})
}

// ============ Admin config handlers ============

func (s *Server) handleGetAdminConfig(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"config":         s.config,
			"restartNeeded":  false,
			"persistedToDB":  true,
			"configFileNote": "Runtime settings are applied immediately where supported. Storage engine settings take effect after restart.",
		},
	})
}

func (s *Server) handleUpdateAdminConfig(w http.ResponseWriter, r *http.Request) {
	var next config.Config
	if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
		s.jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if err := validateRuntimeConfig(&next); err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	prev := s.config
	applied := []string{"scrape targets", "scrape timeout", "agent labels"}
	if s.db != nil {
		s.db.SetRetention(time.Duration(next.Storage.RetentionDays) * 24 * time.Hour)
		applied = append(applied, "storage retention")
	}
	s.config = &next
	if s.scrape != nil {
		s.scrape.ApplyConfig(next.Scrape)
	}

	if data, err := json.Marshal(next); err == nil {
		tenantID := s.getTenantID(r)
		if err := s.store.SetSetting(tenantID, "runtime_config", string(data)); err != nil {
			s.logger.Warn("admin config: failed to persist setting", "err", err)
		}
	}

	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"config":        s.config,
			"restartNeeded": restartNeededAfterConfigUpdate(prev, &next),
			"applied":       applied,
		},
	})
}

func restartNeededAfterConfigUpdate(prev, next *config.Config) bool {
	if prev == nil || next == nil {
		return true
	}
	if prev.Server != next.Server {
		return true
	}
	if prev.Storage.DataDir != next.Storage.DataDir ||
		prev.Storage.FlushInterval != next.Storage.FlushInterval ||
		prev.Storage.MaxOpenFiles != next.Storage.MaxOpenFiles ||
		prev.Storage.CompactionEvery != next.Storage.CompactionEvery {
		return true
	}
	return false
}

func (s *Server) handleAdminStorageStats(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.jsonError(w, "tsdb unavailable", http.StatusServiceUnavailable)
		return
	}
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data":   s.db.Stats(),
	})
}

func (s *Server) handleAdminStorageExport(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.jsonError(w, "tsdb unavailable", http.StatusServiceUnavailable)
		return
	}
	name := fmt.Sprintf("sentinel233-tsdb-%s.tar.gz", time.Now().UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	if err := s.db.Export(w); err != nil {
		s.logger.Error("storage export failed", "err", err)
	}
}

func (s *Server) handleAdminStorageImport(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		s.jsonError(w, "tsdb unavailable", http.StatusServiceUnavailable)
		return
	}
	imported, err := s.db.Import(r.Body)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.db.Compact()
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"imported": imported,
			"stats":    s.db.Stats(),
		},
	})
}

func validateRuntimeConfig(cfg *config.Config) error {
	if cfg.Server.Port < 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 0 and 65535")
	}
	if cfg.Storage.RetentionDays <= 0 {
		return fmt.Errorf("retention days must be greater than 0")
	}
	if cfg.Storage.FlushInterval <= 0 {
		return fmt.Errorf("flush interval must be greater than 0")
	}
	if cfg.Scrape.Interval <= 0 {
		return fmt.Errorf("scrape interval must be greater than 0")
	}
	if cfg.Scrape.Timeout <= 0 {
		return fmt.Errorf("scrape timeout must be greater than 0")
	}
	if cfg.LocalAPI.TenantID <= 0 {
		return fmt.Errorf("local api tenant id must be greater than 0")
	}
	for _, target := range cfg.Scrape.Targets {
		if target.Name == "" || target.Endpoint == "" {
			return fmt.Errorf("scrape targets require name and endpoint")
		}
	}
	return nil
}

// ============ Prometheus handlers ============

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	expr := requestParam(r, "query")
	if expr == "" {
		s.jsonError(w, "query parameter required", http.StatusBadRequest)
		return
	}
	ts := time.Now()
	if t := requestParam(r, "time"); t != "" {
		parsed, err := parsePrometheusTime(t, ts)
		if err != nil {
			s.jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		ts = parsed
	}
	result, err := s.engine.EvalInstant(expr, ts)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"resultType": resultTypeStr(result.Type),
			"result":     formatResult(result),
		},
	})
}

func (s *Server) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	expr := requestParam(r, "query")
	if expr == "" {
		s.jsonError(w, "query parameter required", http.StatusBadRequest)
		return
	}
	startStr := requestParam(r, "start")
	endStr := requestParam(r, "end")
	stepStr := requestParam(r, "step")
	start, err := parsePrometheusTime(startStr, time.Time{})
	if err != nil {
		s.jsonError(w, "invalid start", http.StatusBadRequest)
		return
	}
	end, err := parsePrometheusTime(endStr, time.Time{})
	if err != nil {
		s.jsonError(w, "invalid end", http.StatusBadRequest)
		return
	}
	step, err := parsePrometheusDuration(stepStr, 15*time.Second)
	if err != nil || step <= 0 {
		s.jsonError(w, "invalid step", http.StatusBadRequest)
		return
	}
	results, err := s.engine.EvalRange(expr, start, end, step)
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"resultType": "matrix", "result": formatRangeResults(results)},
	})
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	series := s.filteredSeries(r)
	var result []interface{}
	for _, ser := range series {
		lbls := make(map[string]string)
		for _, l := range ser.Labels {
			lbls[l.Name] = l.Value
		}
		result = append(result, lbls)
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": result})
}

func (s *Server) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	series := s.filteredSeries(r)
	seen := make(map[string]bool)
	var values []string
	for _, ser := range series {
		v := ser.Labels.Get(name)
		if v != "" && !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	sort.Strings(values)
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": values})
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	if s.scrape == nil {
		s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"activeTargets": []interface{}{}, "droppedTargets": []interface{}{}}})
		return
	}
	targets := s.scrape.GetTargets()
	var result []map[string]interface{}
	for _, t := range targets {
		result = append(result, map[string]interface{}{
			"name": t.Name, "endpoint": t.Endpoint, "labels": t.Labels,
			"healthy": t.Healthy, "lastScrape": t.LastScrape, "lastError": errorString(t.LastError),
			"discoveredLabels": t.Labels,
			"scrapeUrl":        t.Endpoint,
			"health":           prometheusHealth(t.Healthy),
		})
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"activeTargets": result, "droppedTargets": []interface{}{}}})
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	rules := s.prometheusRuleGroups(s.getTenantID(r))
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"groups": rules}})
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if s.alertMgr == nil {
		s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"alerts": []interface{}{}}})
		return
	}
	alerts := s.alertMgr.GetAlerts()
	var result []map[string]interface{}
	for _, a := range alerts {
		result = append(result, map[string]interface{}{
			"labels": a.Labels, "state": a.State.String(), "value": a.Value,
			"activeAt": a.ActiveAt, "annotations": a.Annotations,
		})
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"alerts": result}})
}

func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request) { s.handleAlerts(w, r) }
func (s *Server) handleGetAlertHistory(w http.ResponseWriter, r *http.Request) {
	history := s.alertMgr.GetHistory()
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": history})
}

type sentinelWriteRequest struct {
	Resource map[string]string       `json:"resource"`
	Samples  []sentinelSamplePayload `json:"samples"`
	Metrics  []sentinelMetricPayload `json:"metrics"`
}

type sentinelMetricPayload struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Unit        string                  `json:"unit"`
	Type        string                  `json:"type"`
	Labels      map[string]string       `json:"labels"`
	Samples     []sentinelSamplePayload `json:"samples"`
}

type sentinelSamplePayload struct {
	Name      string            `json:"name"`
	Timestamp int64             `json:"timestamp"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels"`
}

func (s *Server) handleSentinelCapabilities(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"protocol": "sentinel233-native-json",
			"version":  1,
			"endpoints": map[string]string{
				"write":        "/api/sentinel/v1/write",
				"capabilities": "/api/sentinel/v1/capabilities",
			},
			"sampleTimestamp": "unix milliseconds; unix seconds are accepted and converted",
			"metricTypes":     []string{"counter", "gauge", "histogram", "summary", "runtime"},
			"labelSemantics":  []string{"resource labels", "metric labels", "sample labels", "__name__"},
		},
	})
}

func (s *Server) handleSentinelWrite(w http.ResponseWriter, r *http.Request) {
	var req sentinelWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonError(w, "invalid native write request", http.StatusBadRequest)
		return
	}

	written := 0
	appendSample := func(metric sentinelMetricPayload, sample sentinelSamplePayload) error {
		name := strings.TrimSpace(sample.Name)
		if name == "" {
			name = strings.TrimSpace(metric.Name)
		}
		if name == "" {
			return fmt.Errorf("sample metric name required")
		}
		ts := sample.Timestamp
		if ts == 0 {
			ts = time.Now().UnixMilli()
		} else if ts < 100000000000 {
			ts *= 1000
		}

		labels := make(map[string]string, len(req.Resource)+len(metric.Labels)+len(sample.Labels)+4)
		labels["__name__"] = name
		labels["source"] = "sentinel_native"
		if metric.Type != "" {
			labels["metric_type"] = metric.Type
		}
		if metric.Unit != "" {
			labels["unit"] = metric.Unit
		}
		for k, v := range req.Resource {
			labels[k] = v
		}
		for k, v := range metric.Labels {
			labels[k] = v
		}
		for k, v := range sample.Labels {
			labels[k] = v
		}
		if err := s.db.Append(labelsMapToTSDB(labels), ts, sample.Value); err != nil {
			return err
		}
		written++
		return nil
	}

	for _, sample := range req.Samples {
		if err := appendSample(sentinelMetricPayload{}, sample); err != nil {
			s.jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	for _, metric := range req.Metrics {
		for _, sample := range metric.Samples {
			if err := appendSample(metric, sample); err != nil {
				s.jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	if written == 0 {
		s.jsonError(w, "no samples provided", http.StatusBadRequest)
		return
	}

	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"written": written,
		},
	})
}

func labelsMapToTSDB(labels map[string]string) tsdb.Labels {
	result := make(tsdb.Labels, 0, len(labels))
	for name, value := range labels {
		if name == "" {
			continue
		}
		result = append(result, tsdb.Label{Name: name, Value: value})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func (s *Server) handleStatusConfig(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": s.config})
}

func (s *Server) handleStatusBuildinfo(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"version": version.Version, "commit": version.Commit, "buildDate": version.Date},
	})
}

func (s *Server) handleStatusRuntime(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"series": s.db.SeriesCount(), "samples": s.db.TotalSamples(), "uptime": time.Since(startTime).String()},
	})
}

var startTime = time.Now()

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"series": s.db.SeriesCount(), "samples": s.db.TotalSamples(),
			"targets": len(s.scrape.GetTargets()), "activeAlerts": len(s.alertMgr.GetAlerts()),
		},
	})
}

func (s *Server) handleI18n(w http.ResponseWriter, r *http.Request) {
	lang := chi.URLParam(r, "lang")
	if lang == "" {
		lang = i18n.DetectLang(r.Header.Get("Accept-Language"))
	}
	keys := []string{
		"app.title", "nav.dashboard", "nav.metrics", "nav.alerts", "nav.targets", "nav.settings",
		"nav.login", "nav.logout", "dashboard.title", "dashboard.new", "dashboard.edit",
		"dashboard.delete", "dashboard.save", "dashboard.add_panel", "dashboard.no_dashboards",
		"panel.type.timeseries", "panel.type.gauge", "panel.type.stat", "panel.type.table",
		"metrics.title", "metrics.query_placeholder", "metrics.execute",
		"alerts.title", "alerts.active", "alerts.history", "alerts.no_alerts",
		"alerts.state.inactive", "alerts.state.pending", "alerts.state.firing", "alerts.state.resolved",
		"targets.title", "targets.add", "targets.endpoint", "targets.name", "targets.labels",
		"targets.status", "targets.healthy", "targets.unhealthy",
		"settings.title", "settings.language", "settings.save", "settings.saved",
		"auth.login", "auth.username", "auth.password", "auth.login_btn", "auth.login_failed",
		"common.confirm", "common.cancel", "common.loading", "common.error", "common.success",
		"common.refresh", "overview.title", "overview.total_series", "overview.total_samples",
		"overview.active_targets", "overview.active_alerts",
	}
	translations := make(map[string]string, len(keys))
	for _, key := range keys {
		translations[key] = i18n.T(lang, key)
	}
	s.jsonOK(w, translations)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.logger.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String())
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Tenant-ID")
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "error", "error": msg})
}

func resultTypeStr(t promql.ValueType) string {
	switch t {
	case promql.ValueScalar:
		return "scalar"
	case promql.ValueInstantVector:
		return "vector"
	case promql.ValueRangeVector:
		return "matrix"
	default:
		return "unknown"
	}
}

func formatResult(r promql.Result) interface{} {
	if r.Type == promql.ValueScalar {
		return []interface{}{float64(time.Now().Unix()), strconv.FormatFloat(r.Scalar, 'f', -1, 64)}
	}
	var result []interface{}
	for _, s := range r.Vector {
		metric := make(map[string]string)
		for _, l := range s.Labels {
			metric[l.Name] = l.Value
		}
		valStr := strconv.FormatFloat(s.Point.Value, 'f', -1, 64)
		ts := float64(s.Point.Timestamp) / 1000
		result = append(result, map[string]interface{}{"metric": metric, "value": []interface{}{ts, valStr}})
	}
	return result
}
