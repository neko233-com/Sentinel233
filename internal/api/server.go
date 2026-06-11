package api

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
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
	r.Post("/api/login", s.handleLogin)
	r.Get("/api/i18n/{lang}", s.handleI18n)
	r.Get("/api/v1/status/buildinfo", s.handleStatusBuildinfo)

	// Prometheus-compatible API (public read for compatibility)
	r.Get("/api/v1/query", s.handleQuery)
	r.Get("/api/v1/query_range", s.handleQueryRange)
	r.Get("/api/v1/series", s.handleSeries)
	r.Get("/api/v1/label/{name}/values", s.handleLabelValues)
	r.Get("/api/v1/targets", s.handleTargets)
	r.Get("/api/v1/rules", s.handleRules)
	r.Get("/api/v1/alerts", s.handleAlerts)
	r.Post("/api/v1/write", s.handleRemoteWrite)
	r.Get("/api/v1/status/config", s.handleStatusConfig)
	r.Get("/api/v1/status/runtime", s.handleStatusRuntime)

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
		r.Get("/", s.handleListDashboards)
		r.Post("/", s.requireRole("operator", s.handleCreateDashboard))
		r.Get("/{id}", s.handleGetDashboard)
		r.Put("/{id}", s.requireRole("operator", s.handleUpdateDashboard))
		r.Delete("/{id}", s.requireRole("admin", s.handleDeleteDashboard))
	})

	// Targets API (tenant-scoped)
	r.Route("/api/targets", func(r chi.Router) {
		r.Use(s.tenantMiddleware)
		r.Get("/", s.handleGetTargets)
		r.Post("/", s.requireRole("operator", s.handleAddTarget))
		r.Delete("/{id}", s.requireRole("operator", s.handleRemoveTarget))
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

func (s *Server) getTenantID(r *http.Request) int64 {
	if v, ok := r.Context().Value(tenantIDKey).(int64); ok && v > 0 {
		return v
	}
	return 1
}

func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func roleAtLeast(userRole, minRole string) bool {
	levels := map[string]int{"viewer": 0, "operator": 1, "admin": 2}
	return levels[userRole] >= levels[minRole]
}

// ============ Auth ============

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
	if err := s.store.CreateDashboard(&d); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
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
	if err := s.store.UpdateDashboard(&d); err != nil {
		s.jsonError(w, err.Error(), 500)
		return
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": d})
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

// ============ Prometheus handlers ============

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	expr := r.URL.Query().Get("query")
	if expr == "" {
		s.jsonError(w, "query parameter required", http.StatusBadRequest)
		return
	}
	ts := time.Now()
	if t := r.URL.Query().Get("time"); t != "" {
		if parsed, err := strconv.ParseFloat(t, 64); err == nil {
			ts = time.Unix(int64(parsed), 0)
		}
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
	expr := r.URL.Query().Get("query")
	if expr == "" {
		s.jsonError(w, "query parameter required", http.StatusBadRequest)
		return
	}
	startStr := r.URL.Query().Get("start")
	endStr := r.URL.Query().Get("end")
	stepStr := r.URL.Query().Get("step")
	start, err := strconv.ParseFloat(startStr, 64)
	if err != nil {
		s.jsonError(w, "invalid start", http.StatusBadRequest)
		return
	}
	end, err := strconv.ParseFloat(endStr, 64)
	if err != nil {
		s.jsonError(w, "invalid end", http.StatusBadRequest)
		return
	}
	step, err := strconv.ParseFloat(stepStr, 64)
	if err != nil {
		step = 15
	}
	results, err := s.engine.EvalRange(expr, time.Unix(int64(start), 0), time.Unix(int64(end), 0), time.Duration(step*float64(time.Second)))
	if err != nil {
		s.jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resultData []interface{}
	for _, res := range results {
		resultData = append(resultData, formatResult(res))
	}
	s.jsonOK(w, map[string]interface{}{
		"status": "success",
		"data":   map[string]interface{}{"resultType": "matrix", "result": resultData},
	})
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	series := s.db.AllSeries()
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
	series := s.db.AllSeries()
	seen := make(map[string]bool)
	var values []string
	for _, ser := range series {
		v := ser.Labels.Get(name)
		if v != "" && !seen[v] {
			seen[v] = true
			values = append(values, v)
		}
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": values})
}

func (s *Server) handleTargets(w http.ResponseWriter, r *http.Request) {
	targets := s.scrape.GetTargets()
	var result []map[string]interface{}
	for _, t := range targets {
		result = append(result, map[string]interface{}{
			"name": t.Name, "endpoint": t.Endpoint, "labels": t.Labels,
			"healthy": t.Healthy, "lastScrape": t.LastScrape, "lastError": "",
		})
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": result})
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": map[string]interface{}{"groups": []interface{}{}}})
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := s.alertMgr.GetAlerts()
	var result []map[string]interface{}
	for _, a := range alerts {
		result = append(result, map[string]interface{}{
			"labels": a.Labels, "state": a.State.String(), "value": a.Value,
			"activeAt": a.ActiveAt, "annotations": a.Annotations,
		})
	}
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": result})
}

func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request)  { s.handleAlerts(w, r) }
func (s *Server) handleGetAlertHistory(w http.ResponseWriter, r *http.Request) {
	history := s.alertMgr.GetHistory()
	s.jsonOK(w, map[string]interface{}{"status": "success", "data": history})
}

func (s *Server) handleRemoteWrite(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
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
		return []interface{}{[]interface{}{float64(time.Now().Unix()), fmt.Sprintf("%f", r.Scalar)}}
	}
	var result []interface{}
	for _, s := range r.Vector {
		metric := make(map[string]string)
		for _, l := range s.Labels {
			metric[l.Name] = l.Value
		}
		valStr := strconv.FormatFloat(s.Point.Value, 'f', -1, 64)
		tsStr := strconv.FormatInt(s.Point.Timestamp/1000, 10)
		result = append(result, []interface{}{metric, []interface{}{tsStr, valStr}})
	}
	return result
}
