package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Tenant struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MaxSeries   int       `json:"max_series"`
	MaxRetention int      `json:"max_retention_days"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Dashboard struct {
	ID          int64     `json:"id"`
	TenantID    int64     `json:"tenant_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Panels      string    `json:"panels"`
	Layout      string    `json:"layout"`
	Variables   string    `json:"variables"`
	Tags        string    `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type User struct {
	ID           int64     `json:"id"`
	TenantID     int64     `json:"tenant_id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type AlertRule struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	Name       string `json:"name"`
	Expr       string `json:"expr"`
	Duration   string `json:"duration"`
	Severity   string `json:"severity"`
	NotifyURL  string `json:"notify_url"`
	Enabled    bool   `json:"enabled"`
}

type ScrapeTarget struct {
	ID       int64             `json:"id"`
	TenantID int64             `json:"tenant_id"`
	Name     string            `json:"name"`
	Endpoint string            `json:"endpoint"`
	Labels   map[string]string `json:"labels"`
	Enabled  bool              `json:"enabled"`
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "sentinel233.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS tenants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			max_series INTEGER DEFAULT 100000,
			max_retention_days INTEGER DEFAULT 15,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS dashboards (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			panels TEXT DEFAULT '[]',
			layout TEXT DEFAULT '{}',
			variables TEXT DEFAULT '[]',
			tags TEXT DEFAULT '[]',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS dashboard_panels (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			dashboard_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			type TEXT NOT NULL DEFAULT 'timeseries',
			query TEXT NOT NULL DEFAULT '',
			position TEXT DEFAULT '{}',
			options TEXT DEFAULT '{}',
			FOREIGN KEY (dashboard_id) REFERENCES dashboards(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT DEFAULT 'viewer',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(tenant_id, username),
			FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS alert_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			name TEXT NOT NULL,
			expr TEXT NOT NULL,
			duration TEXT DEFAULT '0s',
			severity TEXT DEFAULT 'warning',
			notify_url TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS scrape_targets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			name TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			labels TEXT DEFAULT '{}',
			enabled INTEGER DEFAULT 1,
			FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
		);

		CREATE TABLE IF NOT EXISTS settings (
			tenant_id INTEGER NOT NULL DEFAULT 1,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, key)
		);

		CREATE INDEX IF NOT EXISTS idx_dashboards_tenant ON dashboards(tenant_id);
		CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
		CREATE INDEX IF NOT EXISTS idx_alert_rules_tenant ON alert_rules(tenant_id);
		CREATE INDEX IF NOT EXISTS idx_scrape_targets_tenant ON scrape_targets(tenant_id);
		CREATE INDEX IF NOT EXISTS idx_settings_tenant ON settings(tenant_id);
	`)
	return err
}

// ============ Tenant CRUD ============

func (s *Store) CreateTenant(t *Tenant) error {
	res, err := s.db.Exec(
		"INSERT INTO tenants (slug, name, description, max_series, max_retention_days, enabled) VALUES (?, ?, ?, ?, ?, ?)",
		t.Slug, t.Name, t.Description, t.MaxSeries, t.MaxRetention, t.Enabled,
	)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetTenant(id int64) (*Tenant, error) {
	t := &Tenant{}
	var enabled int
	err := s.db.QueryRow(
		"SELECT id, slug, name, description, max_series, max_retention_days, enabled, created_at, updated_at FROM tenants WHERE id=?",
		id,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Description, &t.MaxSeries, &t.MaxRetention, &enabled, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.Enabled = enabled == 1
	return t, nil
}

func (s *Store) GetTenantBySlug(slug string) (*Tenant, error) {
	t := &Tenant{}
	var enabled int
	err := s.db.QueryRow(
		"SELECT id, slug, name, description, max_series, max_retention_days, enabled, created_at, updated_at FROM tenants WHERE slug=?",
		slug,
	).Scan(&t.ID, &t.Slug, &t.Name, &t.Description, &t.MaxSeries, &t.MaxRetention, &enabled, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.Enabled = enabled == 1
	return t, nil
}

func (s *Store) ListTenants() ([]Tenant, error) {
	rows, err := s.db.Query("SELECT id, slug, name, description, max_series, max_retention_days, enabled, created_at, updated_at FROM tenants ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Tenant
	for rows.Next() {
		var t Tenant
		var enabled int
		if err := rows.Scan(&t.ID, &t.Slug, &t.Name, &t.Description, &t.MaxSeries, &t.MaxRetention, &enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.Enabled = enabled == 1
		result = append(result, t)
	}
	return result, nil
}

func (s *Store) UpdateTenant(t *Tenant) error {
	_, err := s.db.Exec(
		"UPDATE tenants SET name=?, description=?, max_series=?, max_retention_days=?, enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		t.Name, t.Description, t.MaxSeries, t.MaxRetention, t.Enabled, t.ID,
	)
	return err
}

func (s *Store) DeleteTenant(id int64) error {
	if id == 1 {
		return fmt.Errorf("cannot delete default tenant")
	}
	_, err := s.db.Exec("DELETE FROM tenants WHERE id=?", id)
	return err
}

// ============ Dashboard CRUD (tenant-scoped) ============

func (s *Store) CreateDashboard(d *Dashboard) error {
	res, err := s.db.Exec(
		"INSERT INTO dashboards (tenant_id, title, description, panels, layout, variables, tags) VALUES (?, ?, ?, ?, ?, ?, ?)",
		d.TenantID, d.Title, d.Description, d.Panels, d.Layout, d.Variables, d.Tags,
	)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) GetDashboard(tenantID, id int64) (*Dashboard, error) {
	d := &Dashboard{}
	err := s.db.QueryRow(
		"SELECT id, tenant_id, title, description, panels, layout, variables, tags, created_at, updated_at FROM dashboards WHERE id=? AND tenant_id=?",
		id, tenantID,
	).Scan(&d.ID, &d.TenantID, &d.Title, &d.Description, &d.Panels, &d.Layout, &d.Variables, &d.Tags, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDashboards(tenantID int64) ([]Dashboard, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, title, description, panels, layout, variables, tags, created_at, updated_at FROM dashboards WHERE tenant_id=? ORDER BY updated_at DESC", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Dashboard
	for rows.Next() {
		var d Dashboard
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Title, &d.Description, &d.Panels, &d.Layout, &d.Variables, &d.Tags, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}

func (s *Store) UpdateDashboard(d *Dashboard) error {
	_, err := s.db.Exec(
		"UPDATE dashboards SET title=?, description=?, panels=?, layout=?, variables=?, tags=?, updated_at=CURRENT_TIMESTAMP WHERE id=? AND tenant_id=?",
		d.Title, d.Description, d.Panels, d.Layout, d.Variables, d.Tags, d.ID, d.TenantID,
	)
	return err
}

func (s *Store) DeleteDashboard(tenantID, id int64) error {
	_, err := s.db.Exec("DELETE FROM dashboards WHERE id=? AND tenant_id=?", id, tenantID)
	return err
}

// ============ User CRUD (tenant-scoped) ============

func (s *Store) GetUser(tenantID int64, username string) (int64, string, string, error) {
	var id int64
	var hash, role string
	err := s.db.QueryRow("SELECT id, password_hash, role FROM users WHERE tenant_id=? AND username=?", tenantID, username).Scan(&id, &hash, &role)
	return id, hash, role, err
}

func (s *Store) CreateUser(u *User) error {
	res, err := s.db.Exec("INSERT INTO users (tenant_id, username, password_hash, role) VALUES (?, ?, ?, ?)", u.TenantID, u.Username, u.PasswordHash, u.Role)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return err
}

func (s *Store) ListUsers(tenantID int64) ([]User, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, username, role, created_at FROM users WHERE tenant_id=? ORDER BY id", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, nil
}

func (s *Store) UpdateUserRole(tenantID int64, username, role string) error {
	_, err := s.db.Exec("UPDATE users SET role=? WHERE tenant_id=? AND username=?", role, tenantID, username)
	return err
}

func (s *Store) DeleteUser(tenantID int64, username string) error {
	_, err := s.db.Exec("DELETE FROM users WHERE tenant_id=? AND username=?", tenantID, username)
	return err
}

// ============ Alert Rules (tenant-scoped) ============

func (s *Store) CreateAlertRule(r *AlertRule) error {
	res, err := s.db.Exec(
		"INSERT INTO alert_rules (tenant_id, name, expr, duration, severity, notify_url, enabled) VALUES (?, ?, ?, ?, ?, ?, ?)",
		r.TenantID, r.Name, r.Expr, r.Duration, r.Severity, r.NotifyURL, r.Enabled,
	)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) ListAlertRules(tenantID int64) ([]AlertRule, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, name, expr, duration, severity, notify_url, enabled FROM alert_rules WHERE tenant_id=? ORDER BY id", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AlertRule
	for rows.Next() {
		var r AlertRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Name, &r.Expr, &r.Duration, &r.Severity, &r.NotifyURL, &enabled); err != nil {
			return nil, err
		}
		r.Enabled = enabled == 1
		result = append(result, r)
	}
	return result, nil
}

func (s *Store) UpdateAlertRule(r *AlertRule) error {
	_, err := s.db.Exec(
		"UPDATE alert_rules SET name=?, expr=?, duration=?, severity=?, notify_url=?, enabled=? WHERE id=? AND tenant_id=?",
		r.Name, r.Expr, r.Duration, r.Severity, r.NotifyURL, r.Enabled, r.ID, r.TenantID,
	)
	return err
}

func (s *Store) DeleteAlertRule(tenantID, id int64) error {
	_, err := s.db.Exec("DELETE FROM alert_rules WHERE id=? AND tenant_id=?", id, tenantID)
	return err
}

// ============ Scrape Targets (tenant-scoped) ============

func (s *Store) CreateScrapeTarget(t *ScrapeTarget) error {
	labelsJSON := "{}"
	if t.Labels != nil {
		labelsJSON = marshalLabels(t.Labels)
	}
	res, err := s.db.Exec(
		"INSERT INTO scrape_targets (tenant_id, name, endpoint, labels, enabled) VALUES (?, ?, ?, ?, ?)",
		t.TenantID, t.Name, t.Endpoint, labelsJSON, t.Enabled,
	)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) ListScrapeTargets(tenantID int64) ([]ScrapeTarget, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, name, endpoint, labels, enabled FROM scrape_targets WHERE tenant_id=? ORDER BY id", tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ScrapeTarget
	for rows.Next() {
		var t ScrapeTarget
		var labelsJSON string
		var enabled int
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Name, &t.Endpoint, &labelsJSON, &enabled); err != nil {
			return nil, err
		}
		t.Labels = unmarshalLabels(labelsJSON)
		t.Enabled = enabled == 1
		result = append(result, t)
	}
	return result, nil
}

func (s *Store) UpdateScrapeTarget(t *ScrapeTarget) error {
	labelsJSON := "{}"
	if t.Labels != nil {
		labelsJSON = marshalLabels(t.Labels)
	}
	_, err := s.db.Exec(
		"UPDATE scrape_targets SET name=?, endpoint=?, labels=?, enabled=? WHERE id=? AND tenant_id=?",
		t.Name, t.Endpoint, labelsJSON, t.Enabled, t.ID, t.TenantID,
	)
	return err
}

func (s *Store) DeleteScrapeTarget(tenantID, id int64) error {
	_, err := s.db.Exec("DELETE FROM scrape_targets WHERE id=? AND tenant_id=?", id, tenantID)
	return err
}

// ============ Settings (tenant-scoped) ============

func (s *Store) SetSetting(tenantID int64, key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO settings (tenant_id, key, value, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP) ON CONFLICT(tenant_id, key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
		tenantID, key, value,
	)
	return err
}

func (s *Store) GetSetting(tenantID int64, key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM settings WHERE tenant_id=? AND key=?", tenantID, key).Scan(&val)
	return val, err
}

// ============ Default Data ============

func CreateDefaultTenant(s *Store) error {
	_, err := s.GetTenant(1)
	if err == nil {
		return nil // already exists
	}
	t := &Tenant{
		ID:              1,
		Slug:            "default",
		Name:            "Default",
		Description:     "Default tenant",
		MaxSeries:       100000,
		MaxRetention:    15,
		Enabled:         true,
	}
	return s.CreateTenant(t)
}

func CreateDefaultRoot(s *Store) error {
	hash, err := bcrypt.GenerateFromPassword([]byte("root"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	// Check if root user exists in tenant 1
	_, _, _, err = s.GetUser(1, "root")
	if err == nil {
		return nil // already exists
	}
	u := &User{
		TenantID:     1,
		Username:     "root",
		PasswordHash: string(hash),
		Role:         "admin",
	}
	return s.CreateUser(u)
}

// ============ Helpers ============

func marshalLabels(m map[string]string) string {
	s := "{"
	first := true
	for k, v := range m {
		if !first {
			s += ","
		}
		s += `"` + k + `":"` + v + `"`
		first = false
	}
	s += "}"
	return s
}

func unmarshalLabels(s string) map[string]string {
	if s == "" || s == "{}" {
		return make(map[string]string)
	}
	result := make(map[string]string)
	// Simple parser for {"k":"v",...}
	s = s[1 : len(s)-1] // remove { }
	pairs := splitPairs(s)
	for _, p := range pairs {
		kv := splitKV(p)
		if len(kv) == 2 {
			key := trimQuotes(kv[0])
			val := trimQuotes(kv[1])
			result[key] = val
		}
	}
	return result
}

func splitPairs(s string) []string {
	var result []string
	current := ""
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
			current += string(ch)
		} else if ch == ',' && !inQuote {
			result = append(result, current)
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func splitKV(s string) []string {
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
		} else if ch == ':' && !inQuote {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func (s *Store) Close() error {
	return s.db.Close()
}
