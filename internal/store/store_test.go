package store

import (
	"os"
	"testing"
)

func TestTenantCRUD(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	// Create default tenant first
	if err := CreateDefaultTenant(s); err != nil {
		t.Fatal(err)
	}

	// Create another tenant
	tenant := &Tenant{Slug: "game-prod", Name: "Game Production", MaxSeries: 50000, Enabled: true}
	if err := s.CreateTenant(tenant); err != nil {
		t.Fatal(err)
	}
	if tenant.ID == 0 {
		t.Fatal("expected non-zero tenant ID")
	}

	// Get by ID
	got, err := s.GetTenant(tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Game Production" {
		t.Fatalf("expected 'Game Production', got '%s'", got.Name)
	}

	// Get by slug
	got2, err := s.GetTenantBySlug("game-prod")
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != tenant.ID {
		t.Fatalf("expected ID %d, got %d", tenant.ID, got2.ID)
	}

	// List
	tenants, err := s.ListTenants()
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(tenants))
	}

	// Update
	tenant.Name = "Game Prod Updated"
	if err := s.UpdateTenant(tenant); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTenant(tenant.ID)
	if got.Name != "Game Prod Updated" {
		t.Fatalf("expected updated name, got '%s'", got.Name)
	}

	// Cannot delete default tenant
	if err := s.DeleteTenant(1); err == nil {
		t.Fatal("should not be able to delete default tenant")
	}

	// Delete
	if err := s.DeleteTenant(tenant.ID); err != nil {
		t.Fatal(err)
	}
	tenants, _ = s.ListTenants()
	if len(tenants) != 1 {
		t.Fatalf("expected 1 tenant after delete, got %d", len(tenants))
	}
}

func TestCreateDefaultRoot(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	CreateDefaultTenant(s)

	if err := CreateDefaultRoot(s); err != nil {
		t.Fatal(err)
	}

	id, hash, role, err := s.GetUser(1, "root")
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}
	if role != "admin" {
		t.Fatalf("expected role 'admin', got '%s'", role)
	}
	if hash == "" || hash == "root" {
		t.Fatal("password should be hashed")
	}

	// Should be idempotent
	if err := CreateDefaultRoot(s); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardTenantScoped(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	CreateDefaultTenant(s)
	s.CreateTenant(&Tenant{Slug: "t2", Name: "Tenant 2", Enabled: true})

	// Create dashboards in different tenants
	s.CreateDashboard(&Dashboard{TenantID: 1, Title: "Dash T1", Panels: "[]"})
	s.CreateDashboard(&Dashboard{TenantID: 2, Title: "Dash T2", Panels: "[]"})
	s.CreateDashboard(&Dashboard{TenantID: 2, Title: "Dash T2-2", Panels: "[]"})

	// List should be tenant-scoped
	d1, _ := s.ListDashboards(1)
	if len(d1) != 1 {
		t.Fatalf("tenant 1: expected 1 dashboard, got %d", len(d1))
	}
	d2, _ := s.ListDashboards(2)
	if len(d2) != 2 {
		t.Fatalf("tenant 2: expected 2 dashboards, got %d", len(d2))
	}

	// Get should be tenant-scoped
	_, err := s.GetDashboard(1, 2)
	if err == nil {
		t.Fatal("tenant 1 should not access tenant 2 dashboard")
	}

	got, err := s.GetDashboard(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Dash T2" {
		t.Fatalf("expected 'Dash T2', got '%s'", got.Title)
	}

	// Delete should be tenant-scoped
	s.DeleteDashboard(1, 2) // wrong tenant
	d2, _ = s.ListDashboards(2)
	if len(d2) != 2 {
		t.Fatalf("tenant 2: expected 2 dashboards still, got %d", len(d2))
	}
}

func TestUserTenantScoped(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	CreateDefaultTenant(s)
	s.CreateTenant(&Tenant{Slug: "t2", Name: "Tenant 2", Enabled: true})

	// Same username in different tenants
	s.CreateUser(&User{TenantID: 1, Username: "alice", PasswordHash: "h1", Role: "admin"})
	s.CreateUser(&User{TenantID: 2, Username: "alice", PasswordHash: "h2", Role: "viewer"})

	_, hash1, role1, _ := s.GetUser(1, "alice")
	_, hash2, role2, _ := s.GetUser(2, "alice")

	if hash1 != "h1" || role1 != "admin" {
		t.Fatalf("tenant 1 alice: hash=%s role=%s", hash1, role1)
	}
	if hash2 != "h2" || role2 != "viewer" {
		t.Fatalf("tenant 2 alice: hash=%s role=%s", hash2, role2)
	}

	// List users per tenant
	u1, _ := s.ListUsers(1)
	u2, _ := s.ListUsers(2)
	if len(u1) != 1 || len(u2) != 1 {
		t.Fatalf("expected 1 user per tenant, got %d/%d", len(u1), len(u2))
	}
}

func TestAlertRulesTenantScoped(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	CreateDefaultTenant(s)

	s.CreateAlertRule(&AlertRule{TenantID: 1, Name: "HighCPU", Expr: "cpu > 90", Severity: "critical", Enabled: true})
	s.CreateAlertRule(&AlertRule{TenantID: 1, Name: "Down", Expr: "up == 0", Severity: "critical", Enabled: true})

	rules, _ := s.ListAlertRules(1)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	s.DeleteAlertRule(1, rules[0].ID)
	rules, _ = s.ListAlertRules(1)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule after delete, got %d", len(rules))
	}
}

func TestScrapeTargetsTenantScoped(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	CreateDefaultTenant(s)

	s.CreateScrapeTarget(&ScrapeTarget{TenantID: 1, Name: "server1", Endpoint: "http://s1:9090/metrics", Labels: map[string]string{"env": "prod"}, Enabled: true})
	s.CreateScrapeTarget(&ScrapeTarget{TenantID: 1, Name: "server2", Endpoint: "http://s2:9090/metrics", Enabled: true})

	targets, _ := s.ListScrapeTargets(1)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].Labels["env"] != "prod" {
		t.Fatalf("expected env=prod, got %v", targets[0].Labels)
	}
}

func TestSettingsTenantScoped(t *testing.T) {
	dir, _ := os.MkdirTemp("", "store-test-*")
	defer os.RemoveAll(dir)

	s, _ := Open(dir)
	defer s.Close()

	CreateDefaultTenant(s)

	s.SetSetting(1, "theme", "dark")
	s.SetSetting(1, "lang", "zh-CN")

	v, _ := s.GetSetting(1, "theme")
	if v != "dark" {
		t.Fatalf("expected dark, got %s", v)
	}

	// Non-existent key
	_, err := s.GetSetting(1, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestMarshalUnmarshalLabels(t *testing.T) {
	labels := map[string]string{"env": "prod", "region": "cn-east"}
	s := marshalLabels(labels)
	got := unmarshalLabels(s)
	if got["env"] != "prod" || got["region"] != "cn-east" {
		t.Fatalf("roundtrip failed: %v -> %s -> %v", labels, s, got)
	}
}
