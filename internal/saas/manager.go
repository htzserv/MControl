package saas

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/api"
	"github.com/htzserv/MControl/internal/db"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Tenant struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PassHash     string `json:"-"`
	Active       bool   `json:"active"`
	MaxServers   int    `json:"max_servers"`
	MaxTunnels   int    `json:"max_tunnels"`
	MaxMonitors  int    `json:"max_monitors"`
	ExpiresAt    int64  `json:"expires_at"` // unix timestamp, 0 = unlimited
	TokenVersion int    `json:"-"`
	CreatedAt    int64  `json:"created_at"`
	// Live counts populated on list
	ServerCount  int `json:"server_count"`
	TunnelCount  int `json:"tunnel_count"`
	MonitorCount int `json:"monitor_count"`
}

type tenantInstance struct {
	store   *db.Store
	handler *api.Handler
	mux     *http.ServeMux
}

type Manager struct {
	dataDir    string
	activePort string
	primary    *api.Handler
	masterDB   *sql.DB

	mu        sync.RWMutex
	instances map[int64]*tenantInstance
}

func NewManager(dataDir, activePort string, primary *api.Handler) (*Manager, error) {
	if err := os.MkdirAll(filepath.Join(dataDir, "tenants"), 0o700); err != nil {
		return nil, fmt.Errorf("create tenants dir: %w", err)
	}
	masterPath := filepath.Join(dataDir, "saas_master.db")
	sqlDB, err := sql.Open("sqlite", masterPath)
	if err != nil {
		return nil, fmt.Errorf("open saas_master.db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		_, _ = sqlDB.Exec(p)
	}
	const schema = `
	CREATE TABLE IF NOT EXISTS tenants (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT NOT NULL UNIQUE,
		pass_hash     TEXT NOT NULL,
		active        INTEGER NOT NULL DEFAULT 1,
		max_servers   INTEGER NOT NULL DEFAULT 10,
		max_tunnels   INTEGER NOT NULL DEFAULT 20,
		max_monitors  INTEGER NOT NULL DEFAULT 20,
		expires_at    INTEGER NOT NULL DEFAULT 0,
		token_version INTEGER NOT NULL DEFAULT 1,
		created_at    INTEGER NOT NULL
	);`
	if _, err := sqlDB.Exec(schema); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("init saas schema: %w", err)
	}
	m := &Manager{
		dataDir:    dataDir,
		activePort: activePort,
		primary:    primary,
		masterDB:   sqlDB,
		instances:  make(map[int64]*tenantInstance),
	}
	// Boot daemons for active tenants lazily or at startup so Telegram/Monitor work immediately.
	if list, err := m.ListTenants(false); err == nil {
		for _, t := range list {
			if t.Active && (t.ExpiresAt == 0 || t.ExpiresAt > time.Now().Unix()) {
				_, _ = m.getOrOpenTenant(t.ID)
			}
		}
	}
	return m, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, inst := range m.instances {
		inst.handler.Stop()
		_ = inst.store.Close()
		delete(m.instances, id)
	}
	if m.masterDB != nil {
		return m.masterDB.Close()
	}
	return nil
}

func (m *Manager) tenantDir(id int64) string {
	return filepath.Join(m.dataDir, "tenants", fmt.Sprintf("u_%d", id))
}

func (m *Manager) getOrOpenTenant(id int64) (*tenantInstance, error) {
	if id <= 0 {
		return nil, errors.New("invalid tenant id")
	}
	m.mu.RLock()
	if inst, ok := m.instances[id]; ok {
		m.mu.RUnlock()
		return inst, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.instances[id]; ok {
		return inst, nil
	}

	dir := m.tenantDir(id)
	store, err := db.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("open tenant %d store: %w", id, err)
	}
	if !store.IsSetupDone() {
		_ = store.SetSetting("setup_done", "1")
	}
	h := api.New(store, m.activePort)
	mux := http.NewServeMux()
	h.Register(mux)
	h.StartAutoProbe()
	h.StartTelegramBot()
	h.StartCloudflareAutoSync()
	h.StartTunnelWatchdog()
	h.StartOpsDaemon()
	h.StartMonitorDaemon()
	h.StartProMetricsDaemon()

	inst := &tenantInstance{store: store, handler: h, mux: mux}
	m.instances[id] = inst
	return inst, nil
}

// ── Tenant CRUD ──────────────────────────────────────────────────────────────

func scanTenant(row interface{ Scan(...any) error }) (*Tenant, error) {
	var t Tenant
	var act int
	if err := row.Scan(&t.ID, &t.Username, &t.PassHash, &act, &t.MaxServers, &t.MaxTunnels, &t.MaxMonitors, &t.ExpiresAt, &t.TokenVersion, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.Active = act == 1
	return &t, nil
}

const tenantCols = `id, username, pass_hash, active, max_servers, max_tunnels, max_monitors, expires_at, token_version, created_at`

func (m *Manager) GetTenantByID(id int64) (*Tenant, error) {
	return scanTenant(m.masterDB.QueryRow(`SELECT `+tenantCols+` FROM tenants WHERE id = ?`, id))
}

func (m *Manager) GetTenantByUsername(username string) (*Tenant, error) {
	return scanTenant(m.masterDB.QueryRow(`SELECT `+tenantCols+` FROM tenants WHERE LOWER(username) = LOWER(?)`, strings.TrimSpace(username)))
}

func (m *Manager) ListTenants(withCounts bool) ([]Tenant, error) {
	rows, err := m.masterDB.Query(`SELECT ` + tenantCols + ` FROM tenants ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if withCounts {
		for i := range out {
			if inst, err := m.getOrOpenTenant(out[i].ID); err == nil {
				if srvs, err := inst.store.ListServers(); err == nil {
					out[i].ServerCount = len(srvs)
				}
				if tuns, err := inst.store.ListTunnels(); err == nil {
					out[i].TunnelCount = len(tuns)
				}
				if mons, err := inst.store.ListMonitors(); err == nil {
					out[i].MonitorCount = len(mons)
				}
			}
		}
	}
	return out, nil
}

func (m *Manager) CreateTenant(username, password string, maxServers, maxTunnels, maxMonitors, expiresDays int) (*Tenant, error) {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 48 {
		return nil, errors.New("username must be 3-48 characters")
	}
	if len(password) < 6 {
		return nil, errors.New("password must be at least 6 characters")
	}
	// Prevent collision with primary SuperAdmin username
	if m.primary != nil && m.primary.Store() != nil {
		if strings.EqualFold(m.primary.Store().GetSettings().Username, username) {
			return nil, errors.New("username conflicts with SuperAdmin account")
		}
	}
	if maxServers <= 0 {
		maxServers = 10
	}
	if maxTunnels <= 0 {
		maxTunnels = 20
	}
	if maxMonitors <= 0 {
		maxMonitors = 20
	}
	var exp int64
	if expiresDays > 0 {
		exp = time.Now().Add(time.Duration(expiresDays) * 24 * time.Hour).Unix()
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	res, err := m.masterDB.Exec(
		`INSERT INTO tenants (username, pass_hash, active, max_servers, max_tunnels, max_monitors, expires_at, token_version, created_at)
		 VALUES (?, ?, 1, ?, ?, ?, ?, 1, ?)`,
		username, string(hash), maxServers, maxTunnels, maxMonitors, exp, time.Now().Unix(),
	)
	if err != nil {
		return nil, errors.New("username already exists")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err := m.getOrOpenTenant(id); err != nil {
		_, _ = m.masterDB.Exec(`DELETE FROM tenants WHERE id = ?`, id)
		return nil, err
	}
	return m.GetTenantByID(id)
}

func (m *Manager) UpdateTenant(id int64, active *bool, password string, maxServers, maxTunnels, maxMonitors, expiresDays *int) error {
	cur, err := m.GetTenantByID(id)
	if err != nil {
		return errors.New("tenant not found")
	}
	if active != nil {
		cur.Active = *active
	}
	if maxServers != nil && *maxServers > 0 {
		cur.MaxServers = *maxServers
	}
	if maxTunnels != nil && *maxTunnels > 0 {
		cur.MaxTunnels = *maxTunnels
	}
	if maxMonitors != nil && *maxMonitors > 0 {
		cur.MaxMonitors = *maxMonitors
	}
	if expiresDays != nil {
		if *expiresDays <= 0 {
			cur.ExpiresAt = 0
		} else {
			cur.ExpiresAt = time.Now().Add(time.Duration(*expiresDays) * 24 * time.Hour).Unix()
		}
	}
	actInt := 0
	if cur.Active {
		actInt = 1
	}
	if strings.TrimSpace(password) != "" {
		if len(password) < 6 {
			return errors.New("password must be at least 6 characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		_, err = m.masterDB.Exec(
			`UPDATE tenants SET active=?, pass_hash=?, max_servers=?, max_tunnels=?, max_monitors=?, expires_at=?, token_version=token_version+1 WHERE id=?`,
			actInt, string(hash), cur.MaxServers, cur.MaxTunnels, cur.MaxMonitors, cur.ExpiresAt, id,
		)
		return err
	}
	_, err = m.masterDB.Exec(
		`UPDATE tenants SET active=?, max_servers=?, max_tunnels=?, max_monitors=?, expires_at=? WHERE id=?`,
		actInt, cur.MaxServers, cur.MaxTunnels, cur.MaxMonitors, cur.ExpiresAt, id,
	)
	return err
}

func (m *Manager) DeleteTenant(id int64) error {
	m.mu.Lock()
	if inst, ok := m.instances[id]; ok {
		inst.handler.Stop()
		_ = inst.store.Close()
		delete(m.instances, id)
	}
	m.mu.Unlock()

	if _, err := m.masterDB.Exec(`DELETE FROM tenants WHERE id = ?`, id); err != nil {
		return err
	}
	_ = os.RemoveAll(m.tenantDir(id))
	return nil
}

// ── Tenant & Impersonation Token Signing ("sa.<payload>.<hmac>") ─────────────

type saasClaims struct {
	TenantID       int64  `json:"tid"`
	Username       string `json:"u"`
	TokenVersion   int    `json:"tv"`
	ImpersonatedBy string `json:"imp,omitempty"`
	ExpiresAt      int64  `json:"e"`
}

func (m *Manager) signKey() []byte {
	if m.primary != nil && m.primary.Store() != nil {
		return m.primary.Store().SignKey()
	}
	return []byte("mcontrol-saas-fallback-key")
}

func (m *Manager) signPayload(payload string) string {
	mac := hmac.New(sha256.New, m.signKey())
	_, _ = mac.Write([]byte("saas:" + payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) IssueTenantToken(t *Tenant, impersonatedBy string) string {
	c := saasClaims{
		TenantID:       t.ID,
		Username:       t.Username,
		TokenVersion:   t.TokenVersion,
		ImpersonatedBy: impersonatedBy,
		ExpiresAt:      time.Now().Add(12 * time.Hour).Unix(),
	}
	raw, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return "sa." + payload + "." + m.signPayload(payload)
}

func (m *Manager) VerifyTenantToken(token string) (*saasClaims, *Tenant, bool) {
	if !strings.HasPrefix(token, "sa.") {
		return nil, nil, false
	}
	parts := strings.SplitN(token[3:], ".", 2)
	if len(parts) != 2 {
		return nil, nil, false
	}
	if !hmac.Equal([]byte(parts[1]), []byte(m.signPayload(parts[0]))) {
		return nil, nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, nil, false
	}
	var c saasClaims
	if json.Unmarshal(raw, &c) != nil || c.ExpiresAt < time.Now().Unix() {
		return nil, nil, false
	}
	t, err := m.GetTenantByID(c.TenantID)
	if err != nil {
		return nil, nil, false
	}
	// If NOT impersonated by SuperAdmin, enforce active, expiry & token_version
	if c.ImpersonatedBy == "" {
		if !t.Active || t.TokenVersion != c.TokenVersion {
			return nil, nil, false
		}
		if t.ExpiresAt > 0 && time.Now().Unix() > t.ExpiresAt {
			return nil, nil, false
		}
	}
	return &c, t, true
}
