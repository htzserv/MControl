package sqlite

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/storage"
	_ "modernc.org/sqlite"
)

type Manager struct {
	baseDir string
	encKey  []byte
	master  *sql.DB

	mu      sync.RWMutex
	tenants map[int64]*sql.DB
}

func dsn(path string) string {
	clean := filepath.ToSlash(path)
	return fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-16000)",
		clean,
	)
}

func NewManager(ctx context.Context, baseDir string, encKey []byte) (*Manager, error) {
	tenantsDir := filepath.Join(baseDir, "tenants")
	if err := os.MkdirAll(tenantsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dirs: %w", err)
	}

	masterPath := filepath.Join(baseDir, "master.db")
	masterDB, err := sql.Open("sqlite", dsn(masterPath))
	if err != nil {
		return nil, fmt.Errorf("open master db: %w", err)
	}
	masterDB.SetMaxOpenConns(4)
	masterDB.SetMaxIdleConns(2)
	masterDB.SetConnMaxLifetime(30 * time.Minute)

	if err := masterDB.PingContext(ctx); err != nil {
		_ = masterDB.Close()
		return nil, fmt.Errorf("ping master db: %w", err)
	}

	m := &Manager{
		baseDir: baseDir,
		encKey:  encKey,
		master:  masterDB,
		tenants: make(map[int64]*sql.DB),
	}
	if err := m.initMasterSchema(ctx); err != nil {
		_ = masterDB.Close()
		return nil, err
	}
	return m, nil
}

func (m *Manager) encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	blk, err := aes.NewCipher(m.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (m *Manager) decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	blk, err := aes.NewCipher(m.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	pt, err := gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func (m *Manager) initMasterSchema(ctx context.Context) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS users (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		username     TEXT NOT NULL UNIQUE,
		pass_hash    TEXT NOT NULL,
		role         TEXT NOT NULL DEFAULT 'tenant',
		active       INTEGER NOT NULL DEFAULT 1,
		max_servers  INTEGER NOT NULL DEFAULT 10,
		max_tunnels  INTEGER NOT NULL DEFAULT 20,
		max_monitors INTEGER NOT NULL DEFAULT 20,
		expires_at   INTEGER,
		created_at   INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS system_settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`
	_, err := m.master.ExecContext(ctx, schema)
	return err
}

func (m *Manager) getTenantDB(ctx context.Context, tenantID int64) (*sql.DB, error) {
	if tenantID <= 0 {
		return nil, errors.New("invalid tenant id")
	}

	m.mu.RLock()
	if db, ok := m.tenants[tenantID]; ok {
		m.mu.RUnlock()
		return db, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.tenants[tenantID]; ok {
		return db, nil
	}

	path := filepath.Join(m.baseDir, "tenants", fmt.Sprintf("u_%d.db", tenantID))
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open tenant %d db: %w", tenantID, err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(10 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping tenant %d db: %w", tenantID, err)
	}
	if err := initTenantSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init tenant %d schema: %w", tenantID, err)
	}
	m.tenants[tenantID] = db
	return db, nil
}

func initTenantSchema(ctx context.Context, db *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS servers (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL,
		role       TEXT NOT NULL DEFAULT 'foreign',
		host       TEXT NOT NULL,
		port       INTEGER NOT NULL DEFAULT 22,
		username   TEXT NOT NULL DEFAULT 'root',
		secret_enc TEXT NOT NULL DEFAULT '',
		auth_type  TEXT NOT NULL DEFAULT 'password',
		country    TEXT NOT NULL DEFAULT '',
		datacenter TEXT NOT NULL DEFAULT '',
		status     TEXT NOT NULL DEFAULT 'unknown',
		cpu        TEXT NOT NULL DEFAULT '0%',
		ram        TEXT NOT NULL DEFAULT '0%',
		uptime     TEXT NOT NULL DEFAULT '—',
		ipv4       TEXT NOT NULL DEFAULT '',
		ipv6       TEXT NOT NULL DEFAULT '',
		ping_ms    TEXT NOT NULL DEFAULT '',
		last_speed TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		sort_order INTEGER NOT NULL DEFAULT 0,
		pos_x      REAL NOT NULL DEFAULT 0,
		pos_y      REAL NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS tunnels (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		name              TEXT NOT NULL,
		engine            TEXT NOT NULL DEFAULT 'backhaul',
		transport         TEXT NOT NULL DEFAULT 'tcp',
		iran_server_id    INTEGER NOT NULL,
		foreign_server_id INTEGER NOT NULL,
		tunnel_port       INTEGER NOT NULL DEFAULT 3080,
		ports             TEXT NOT NULL DEFAULT '443',
		token             TEXT NOT NULL DEFAULT '',
		local_ip          TEXT NOT NULL DEFAULT '',
		remote_ip         TEXT NOT NULL DEFAULT '',
		mtu               INTEGER NOT NULL DEFAULT 1420,
		status            TEXT NOT NULL DEFAULT 'draft',
		last_ping         TEXT NOT NULL DEFAULT '',
		last_log          TEXT NOT NULL DEFAULT '',
		created_at        INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS tenant_settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}
	// Seamless column upgrades if DB was created in v1.0.0 core
	alters := []string{
		`ALTER TABLE servers ADD COLUMN auth_type TEXT NOT NULL DEFAULT 'password'`,
		`ALTER TABLE servers ADD COLUMN datacenter TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN cpu TEXT NOT NULL DEFAULT '0%'`,
		`ALTER TABLE servers ADD COLUMN ram TEXT NOT NULL DEFAULT '0%'`,
		`ALTER TABLE servers ADD COLUMN uptime TEXT NOT NULL DEFAULT '—'`,
		`ALTER TABLE servers ADD COLUMN ipv4 TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ipv6 TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN ping_ms TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN last_speed TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE servers ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tunnels ADD COLUMN transport TEXT NOT NULL DEFAULT 'tcp'`,
		`ALTER TABLE tunnels ADD COLUMN token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tunnels ADD COLUMN local_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tunnels ADD COLUMN remote_ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tunnels ADD COLUMN mtu INTEGER NOT NULL DEFAULT 1420`,
		`ALTER TABLE tunnels ADD COLUMN last_ping TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tunnels ADD COLUMN last_log TEXT NOT NULL DEFAULT ''`,
	}
	for _, q := range alters {
		_, _ = db.ExecContext(ctx, q)
	}
	return nil
}

// ── MasterStore Implementation ───────────────────────────────────────────────

func (m *Manager) IsInitialized(ctx context.Context) (bool, error) {
	var cnt int
	err := m.master.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'superadmin'`).Scan(&cnt)
	return cnt > 0, err
}

func scanUser(row interface{ Scan(...any) error }) (*storage.User, error) {
	var u storage.User
	var role string
	var active int
	var exp sql.NullInt64
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PassHash, &role, &active, &u.MaxServers, &u.MaxTunnels, &u.MaxMonitors, &exp, &created); err != nil {
		return nil, err
	}
	u.Role = storage.Role(role)
	u.Active = active == 1
	if exp.Valid && exp.Int64 > 0 {
		t := time.Unix(exp.Int64, 0).UTC()
		u.ExpiresAt = &t
	}
	u.CreatedAt = time.Unix(created, 0).UTC()
	return &u, nil
}

const userSelect = `SELECT id, username, pass_hash, role, active, max_servers, max_tunnels, max_monitors, expires_at, created_at FROM users`

func (m *Manager) GetUserByUsername(ctx context.Context, username string) (*storage.User, error) {
	row := m.master.QueryRowContext(ctx, userSelect+` WHERE LOWER(username) = LOWER(?)`, strings.TrimSpace(username))
	return scanUser(row)
}

func (m *Manager) GetUserByID(ctx context.Context, id int64) (*storage.User, error) {
	row := m.master.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id)
	return scanUser(row)
}

func (m *Manager) ListUsers(ctx context.Context) ([]storage.User, error) {
	rows, err := m.master.QueryContext(ctx, userSelect+` ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, nil
}

func (m *Manager) CreateUser(ctx context.Context, u *storage.User) (int64, error) {
	active := 0
	if u.Active {
		active = 1
	}
	var exp any = nil
	if u.ExpiresAt != nil {
		exp = u.ExpiresAt.Unix()
	}
	res, err := m.master.ExecContext(ctx,
		`INSERT INTO users (username, pass_hash, role, active, max_servers, max_tunnels, max_monitors, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(u.Username), u.PassHash, string(u.Role), active,
		u.MaxServers, u.MaxTunnels, u.MaxMonitors, exp, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, _ = m.getTenantDB(ctx, id)
	return id, nil
}

func (m *Manager) UpdateUser(ctx context.Context, u *storage.User, newPassHash string) error {
	active := 0
	if u.Active {
		active = 1
	}
	var exp any = nil
	if u.ExpiresAt != nil {
		exp = u.ExpiresAt.Unix()
	}
	if newPassHash != "" {
		_, err := m.master.ExecContext(ctx,
			`UPDATE users SET active=?, max_servers=?, max_tunnels=?, max_monitors=?, expires_at=?, pass_hash=? WHERE id=?`,
			active, u.MaxServers, u.MaxTunnels, u.MaxMonitors, exp, newPassHash, u.ID)
		return err
	}
	_, err := m.master.ExecContext(ctx,
		`UPDATE users SET active=?, max_servers=?, max_tunnels=?, max_monitors=?, expires_at=? WHERE id=?`,
		active, u.MaxServers, u.MaxTunnels, u.MaxMonitors, exp, u.ID)
	return err
}

func (m *Manager) DeleteUser(ctx context.Context, id int64) error {
	m.mu.Lock()
	if db, ok := m.tenants[id]; ok {
		_ = db.Close()
		delete(m.tenants, id)
	}
	m.mu.Unlock()

	if _, err := m.master.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	base := filepath.Join(m.baseDir, "tenants", fmt.Sprintf("u_%d.db", id))
	_ = os.Remove(base)
	_ = os.Remove(base + "-wal")
	_ = os.Remove(base + "-shm")
	return nil
}

func (m *Manager) GetBranding(ctx context.Context) (storage.Branding, error) {
	b := storage.Branding{
		AppName:  "MControl",
		Subtitle: "Multi-Tenant Infrastructure Cloud",
		Accent:   "#6366f1",
	}
	var raw string
	if err := m.master.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = 'branding'`).Scan(&raw); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &b)
	}
	return b, nil
}

func (m *Manager) SaveBranding(ctx context.Context, b storage.Branding) error {
	if strings.TrimSpace(b.AppName) == "" {
		b.AppName = "MControl"
	}
	if strings.TrimSpace(b.Accent) == "" {
		b.Accent = "#6366f1"
	}
	raw, _ := json.Marshal(b)
	_, err := m.master.ExecContext(ctx, `INSERT INTO system_settings (key, value) VALUES ('branding', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw))
	return err
}

func (m *Manager) GetSystemStats(ctx context.Context) (storage.SystemStats, error) {
	var st storage.SystemStats
	users, err := m.ListUsers(ctx)
	if err != nil {
		return st, err
	}
	st.TotalUsers = len(users)
	for _, u := range users {
		if u.Role == storage.RoleTenant && u.Active {
			st.ActiveTenants++
		}
		if db, err := m.getTenantDB(ctx, u.ID); err == nil {
			var sc, tc int
			_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers`).Scan(&sc)
			_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tunnels`).Scan(&tc)
			st.TotalServers += sc
			st.TotalTunnels += tc
		}
	}
	return st, nil
}

// ── Per-Tenant Isolated Store Implementation ─────────────────────────────────

const serverSelectCols = `id, name, role, host, port, username, secret_enc, auth_type, country, datacenter, status, cpu, ram, uptime, ipv4, ipv6, ping_ms, last_speed, last_error, sort_order, pos_x, pos_y, created_at`

func scanServer(row interface{ Scan(...any) error }) (*storage.Server, error) {
	var s storage.Server
	var secEnc string
	var ts int64
	if err := row.Scan(
		&s.ID, &s.Name, &s.Role, &s.Host, &s.Port, &s.Username, &secEnc, &s.AuthType,
		&s.Country, &s.Datacenter, &s.Status, &s.CPU, &s.RAM, &s.Uptime,
		&s.IPv4, &s.IPv6, &s.PingMs, &s.LastSpeed, &s.LastError,
		&s.SortOrder, &s.PosX, &s.PosY, &ts,
	); err != nil {
		return nil, err
	}
	s.HasSecret = secEnc != ""
	s.Secret = secEnc
	s.CreatedAt = time.Unix(ts, 0).UTC()
	return &s, nil
}

func (m *Manager) ListServers(ctx context.Context, tenantID int64) ([]storage.Server, error) {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+serverSelectCols+` FROM servers ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		s.Secret = "" // Never leak ciphertext to list API
		out = append(out, *s)
	}
	return out, nil
}

func (m *Manager) GetServerWithSecret(ctx context.Context, tenantID, serverID int64) (*storage.Server, error) {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx, `SELECT `+serverSelectCols+` FROM servers WHERE id = ?`, serverID)
	s, err := scanServer(row)
	if err != nil {
		return nil, err
	}
	if s.Secret != "" {
		plain, err := m.decrypt(s.Secret)
		if err != nil {
			return nil, err
		}
		s.Secret = plain
	}
	return s, nil
}

func (m *Manager) AddServer(ctx context.Context, tenantID int64, s *storage.Server) (int64, error) {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	secEnc, err := m.encrypt(s.Secret)
	if err != nil {
		return 0, err
	}
	var nextOrder int
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), -1) + 1 FROM servers`).Scan(&nextOrder)

	posX, posY := s.PosX, s.PosY
	if posX == 0 && posY == 0 {
		if s.Role == "iran" {
			posX = 80
		} else {
			posX = 460
		}
		posY = float64(50 + nextOrder*105)
	}
	if s.Port <= 0 {
		s.Port = 22
	}
	if strings.TrimSpace(s.Username) == "" {
		s.Username = "root"
	}
	if s.AuthType != "key" {
		s.AuthType = "password"
	}
	if strings.TrimSpace(s.Status) == "" {
		s.Status = "unknown"
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO servers (name, role, host, port, username, secret_enc, auth_type, country, datacenter, status, sort_order, pos_x, pos_y, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(s.Name), s.Role, strings.TrimSpace(s.Host), s.Port, strings.TrimSpace(s.Username),
		secEnc, s.AuthType, strings.TrimSpace(s.Country), strings.TrimSpace(s.Datacenter), s.Status, nextOrder, posX, posY, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (m *Manager) UpdateServer(ctx context.Context, tenantID int64, s *storage.Server) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	if s.Port <= 0 {
		s.Port = 22
	}
	if strings.TrimSpace(s.Username) == "" {
		s.Username = "root"
	}
	if s.AuthType != "key" {
		s.AuthType = "password"
	}
	if strings.TrimSpace(s.Secret) != "" {
		secEnc, err := m.encrypt(s.Secret)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx,
			`UPDATE servers SET name=?, role=?, host=?, port=?, username=?, secret_enc=?, auth_type=?, country=?, datacenter=? WHERE id=?`,
			strings.TrimSpace(s.Name), s.Role, strings.TrimSpace(s.Host), s.Port, strings.TrimSpace(s.Username),
			secEnc, s.AuthType, strings.TrimSpace(s.Country), strings.TrimSpace(s.Datacenter), s.ID,
		)
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE servers SET name=?, role=?, host=?, port=?, username=?, auth_type=?, country=?, datacenter=? WHERE id=?`,
		strings.TrimSpace(s.Name), s.Role, strings.TrimSpace(s.Host), s.Port, strings.TrimSpace(s.Username),
		s.AuthType, strings.TrimSpace(s.Country), strings.TrimSpace(s.Datacenter), s.ID,
	)
	return err
}

func (m *Manager) UpdateServerRuntime(ctx context.Context, tenantID, serverID int64, status, cpu, ram, uptime, ipv4, ipv6, pingMs, lastErr string) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		`UPDATE servers SET status=?, cpu=?, ram=?, uptime=?, ipv4=?, ipv6=?, ping_ms=?, last_error=? WHERE id=?`,
		status, cpu, ram, uptime, ipv4, ipv6, pingMs, lastErr, serverID,
	)
	return err
}

func (m *Manager) UpdateServerSpeed(ctx context.Context, tenantID, serverID int64, lastSpeed string) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE servers SET last_speed=? WHERE id=?`, lastSpeed, serverID)
	return err
}

func (m *Manager) DeleteServer(ctx context.Context, tenantID, serverID int64) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	_, _ = db.ExecContext(ctx, `DELETE FROM tunnels WHERE iran_server_id = ? OR foreign_server_id = ?`, serverID, serverID)
	_, err = db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, serverID)
	return err
}

const tunnelSelectCols = `id, name, engine, transport, iran_server_id, foreign_server_id, tunnel_port, ports, token, local_ip, remote_ip, mtu, status, last_ping, last_log, created_at`

func scanTunnel(row interface{ Scan(...any) error }) (*storage.Tunnel, error) {
	var t storage.Tunnel
	var ts int64
	if err := row.Scan(
		&t.ID, &t.Name, &t.Engine, &t.Transport, &t.IranServerID, &t.ForeignServerID,
		&t.TunnelPort, &t.Ports, &t.Token, &t.LocalIP, &t.RemoteIP, &t.MTU,
		&t.Status, &t.LastPing, &t.LastLog, &ts,
	); err != nil {
		return nil, err
	}
	t.CreatedAt = time.Unix(ts, 0).UTC()
	return &t, nil
}

func (m *Manager) ListTunnels(ctx context.Context, tenantID int64) ([]storage.Tunnel, error) {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+tunnelSelectCols+` FROM tunnels ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Tunnel{}
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

func (m *Manager) GetTunnel(ctx context.Context, tenantID, tunnelID int64) (*storage.Tunnel, error) {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return scanTunnel(db.QueryRowContext(ctx, `SELECT `+tunnelSelectCols+` FROM tunnels WHERE id = ?`, tunnelID))
}

func (m *Manager) AddTunnel(ctx context.Context, tenantID int64, t *storage.Tunnel) (int64, error) {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	if t.Engine == "" {
		t.Engine = "backhaul"
	}
	if t.Transport == "" {
		t.Transport = "tcp"
	}
	if t.TunnelPort <= 0 {
		t.TunnelPort = 3080
	}
	if strings.TrimSpace(t.Ports) == "" {
		t.Ports = "443"
	}
	if t.MTU <= 0 {
		t.MTU = 1420
	}
	if strings.TrimSpace(t.Token) == "" {
		b := make([]byte, 12)
		_, _ = io.ReadFull(rand.Reader, b)
		t.Token = fmt.Sprintf("%x", b)
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO tunnels (name, engine, transport, iran_server_id, foreign_server_id, tunnel_port, ports, token, local_ip, remote_ip, mtu, status, last_ping, last_log, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'draft', '', '', ?)`,
		strings.TrimSpace(t.Name), t.Engine, t.Transport, t.IranServerID, t.ForeignServerID,
		t.TunnelPort, strings.TrimSpace(t.Ports), t.Token, strings.TrimSpace(t.LocalIP), strings.TrimSpace(t.RemoteIP), t.MTU, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (m *Manager) UpdateTunnelRuntime(ctx context.Context, tenantID, tunnelID int64, status, lastLog, lastPing string) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	if lastLog != "" && lastPing != "" {
		_, err = db.ExecContext(ctx, `UPDATE tunnels SET status=?, last_log=?, last_ping=? WHERE id=?`, status, lastLog, lastPing, tunnelID)
		return err
	}
	if lastLog != "" {
		_, err = db.ExecContext(ctx, `UPDATE tunnels SET status=?, last_log=? WHERE id=?`, status, lastLog, tunnelID)
		return err
	}
	if lastPing != "" {
		_, err = db.ExecContext(ctx, `UPDATE tunnels SET last_ping=? WHERE id=?`, lastPing, tunnelID)
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE tunnels SET status=? WHERE id=?`, status, tunnelID)
	return err
}

func (m *Manager) DeleteTunnel(ctx context.Context, tenantID, tunnelID int64) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, tunnelID)
	return err
}

func (m *Manager) SaveLayout(ctx context.Context, tenantID int64, items []storage.LayoutItem) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, it := range items {
		switch it.Section {
		case "servers":
			_, err = tx.ExecContext(ctx, `UPDATE servers SET sort_order = ? WHERE id = ?`, it.SortIndex, it.ItemID)
		case "topology":
			_, err = tx.ExecContext(ctx, `UPDATE servers SET pos_x = ?, pos_y = ? WHERE id = ?`, it.PosX, it.PosY, it.ItemID)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Manager) GetThemePrefs(ctx context.Context, tenantID int64) (storage.ThemePrefs, error) {
	p := storage.ThemePrefs{Preset: "mdesign", Mode: "dark", Accent: "#6366f1", Radius: 20, Lang: "fa"}
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return p, err
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT value FROM tenant_settings WHERE key = 'theme'`).Scan(&raw); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	return p, nil
}

func (m *Manager) SaveThemePrefs(ctx context.Context, tenantID int64, prefs storage.ThemePrefs) error {
	db, err := m.getTenantDB(ctx, tenantID)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(prefs)
	_, err = db.ExecContext(ctx, `INSERT INTO tenant_settings (key, value) VALUES ('theme', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw))
	return err
}

func (m *Manager) MigrateLegacyV1(legacyDBPath string, targetTenantID int64) (bool, error) {
	if _, err := os.Stat(legacyDBPath); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	dstPath := filepath.Join(m.baseDir, "tenants", fmt.Sprintf("u_%d_v1_backup.db", targetTenantID))
	if _, err := os.Stat(dstPath); err == nil {
		return false, nil
	}
	in, err := os.Open(legacyDBPath)
	if err != nil {
		return false, err
	}
	defer in.Close()
	out, err := os.Create(dstPath)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return false, err
	}
	_ = out.Sync()
	_ = out.Close()
	return true, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, db := range m.tenants {
		_ = db.Close()
		delete(m.tenants, id)
	}
	if m.master != nil {
		return m.master.Close()
	}
	return nil
}
