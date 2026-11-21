package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const MinPasswordLen = 8

type Server struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Role          string `json:"role"` // "iran" or "foreign"
	Host          string `json:"host"`
	Port          int    `json:"port"`
	Username      string `json:"username"`
	Password      string `json:"password,omitempty"`
	AuthType      string `json:"auth_type"` // "password" or "key"
	HostKey       string `json:"host_key"`  // SHA256 fingerprint pinned on first connect
	LastError     string `json:"last_error"`
	Country       string `json:"country"`
	CountryCode   string `json:"country_code"`
	Datacenter    string `json:"datacenter"`
	CoreInstalled bool   `json:"core_installed"`
	CoreVersion   string `json:"core_version"`
	Status        string `json:"status"` // "online", "offline", "unknown"
	CPU           string `json:"cpu"`
	RAM           string `json:"ram"`
	Uptime         string `json:"uptime"`
	IPv4           string `json:"ipv4"`
	IPv6           string `json:"ipv6"`
	NetRx          string `json:"net_rx"`
	NetTx          string `json:"net_tx"`
	InstalledTools string `json:"installed_tools"`
	AuthMode       string `json:"auth_mode"`
	PeerCode       string `json:"peer_code,omitempty"`
	AgentPort      int    `json:"agent_port"`
	PingMs         string `json:"ping_ms"`
	LastSpeed      string `json:"last_speed"`
	CreatedAt      int64  `json:"created_at"`
}

type SSHProfile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	Port      int    `json:"port"`
	CreatedAt int64  `json:"created_at"`
}

type PortProfile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Ports     string `json:"ports"`
	CreatedAt int64  `json:"created_at"`
}

type BenchmarkRun struct {
	ID          int64  `json:"id"`
	IranName    string `json:"iran_name"`
	ForeignName string `json:"foreign_name"`
	Profile     string `json:"profile"`
	ResultsJSON string `json:"results_json"`
	LogsText    string `json:"logs_text"`
	CreatedAt   int64  `json:"created_at"`
}

type Tunnel struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Engine          string `json:"engine"`
	Transport       string `json:"transport"`
	IranServerID    int64  `json:"iran_server_id"`
	ForeignServerID int64  `json:"foreign_server_id"`
	IranName        string `json:"iran_name"`
	IranHost        string `json:"iran_host"`
	ForeignName     string `json:"foreign_name"`
	ForeignHost     string `json:"foreign_host"`
	TunnelPort      int    `json:"tunnel_port"`
	Token           string `json:"token"`
	Ports           string `json:"ports"`
	Forwarder       string `json:"forwarder"`
	Subnet          string `json:"subnet"`
	VIPTargets      string `json:"vip_targets"`
	LBMode          string `json:"lb_mode"`
	Status          string `json:"status"`
	LastError       string `json:"last_error"`
	CreatedAt       int64  `json:"created_at"`
}

type Settings struct {
	GitHubRepoURL   string `json:"github_repo_url"`
	IranMirrorURL   string `json:"iran_mirror_url"`
	PanelGitHubURL  string `json:"panel_github_url"`
	PanelMirrorURL  string `json:"panel_mirror_url"`
	WebPath         string `json:"web_path"`
	PanelPort       string `json:"panel_port"`
	ServiceName     string `json:"service_name"`
	ProbeInterval   int    `json:"probe_interval"`   // minutes, 0 = manual only
	AllowUnverified bool   `json:"allow_unverified"` // allow updates without a .sha256 file
	Username        string `json:"username"`
	TwoFAEnabled    bool   `json:"twofa_enabled"`
	TwoFASecret     string `json:"twofa_secret,omitempty"`
	DefaultSSHUser  string `json:"default_ssh_user"`
	DefaultSSHPort  int    `json:"default_ssh_port"`
	DefaultSSHPass  string `json:"default_ssh_pass,omitempty"`
	HasDefaultPass  bool   `json:"has_default_pass"`
	SavedPorts      string `json:"saved_ports"`
	PanelName       string `json:"panel_name"`
	ListenDomain    string `json:"listen_domain"`
	StrictDomain    bool   `json:"strict_domain"`
}

type Store struct {
	db      *sql.DB
	encKey  []byte
	signKey []byte
	dbPath  string
	keyPath string
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}

	keyPath := filepath.Join(dataDir, ".secret.key")
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil || len(keyBytes) < 32 {
		keyBytes = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, keyBytes); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath, keyBytes, 0600); err != nil {
			return nil, err
		}
	}
	// encKey is kept identical to previous versions so existing encrypted passwords still decrypt.
	hash := sha256.Sum256(keyBytes)
	// The session-token signing key is derived separately so the two uses never share a key.
	mac := hmac.New(sha256.New, hash[:])
	mac.Write([]byte("mcontrol-token-signing-v1"))
	signKey := mac.Sum(nil)

	dbPath := filepath.Join(dataDir, "mcontrol.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	s := &Store{db: sqlDB, encKey: hash[:], signKey: signKey, dbPath: dbPath, keyPath: keyPath}
	if err := s.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	_ = os.Chmod(dbPath, 0600)
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SignKey() []byte {
	return s.signKey
}

func (s *Store) DBPath() string {
	return s.dbPath
}

func (s *Store) KeyPath() string {
	return s.keyPath
}

func (s *Store) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS servers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'foreign',
		host TEXT NOT NULL,
		port INTEGER NOT NULL DEFAULT 22,
		username TEXT NOT NULL DEFAULT 'root',
		password_enc TEXT NOT NULL,
		country TEXT DEFAULT '',
		country_code TEXT DEFAULT '',
		datacenter TEXT DEFAULT '',
		core_installed INTEGER DEFAULT 0,
		core_version TEXT DEFAULT '',
		status TEXT DEFAULT 'unknown',
		cpu TEXT DEFAULT '0%',
		ram TEXT DEFAULT '0%',
		uptime TEXT DEFAULT '---',
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS ssh_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		username TEXT NOT NULL DEFAULT 'root',
		password_enc TEXT NOT NULL DEFAULT '',
		port INTEGER NOT NULL DEFAULT 22,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS port_profiles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		ports TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS benchmark_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		iran_name TEXT NOT NULL,
		foreign_name TEXT NOT NULL,
		profile TEXT NOT NULL DEFAULT 'balanced',
		results_json TEXT NOT NULL DEFAULT '[]',
		logs_text TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS tunnels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		engine TEXT NOT NULL,
		transport TEXT NOT NULL DEFAULT 'tcp',
		iran_server_id INTEGER NOT NULL,
		foreign_server_id INTEGER NOT NULL,
		tunnel_port INTEGER NOT NULL DEFAULT 3080,
		token TEXT NOT NULL DEFAULT '',
		ports TEXT NOT NULL DEFAULT '',
		forwarder TEXT NOT NULL DEFAULT 'internal',
		subnet TEXT NOT NULL DEFAULT '',
		vip_targets TEXT NOT NULL DEFAULT '',
		lb_mode TEXT NOT NULL DEFAULT 'none',
		status TEXT NOT NULL DEFAULT 'active',
		last_error TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Additive migrations (errors for "duplicate column" are expected on upgraded databases).
	for _, stmt := range []string{
		"ALTER TABLE servers ADD COLUMN auth_type TEXT DEFAULT 'password'",
		"ALTER TABLE servers ADD COLUMN host_key TEXT DEFAULT ''",
		"ALTER TABLE servers ADD COLUMN last_error TEXT DEFAULT ''",
		"ALTER TABLE servers ADD COLUMN ipv4 TEXT DEFAULT ''",
		"ALTER TABLE servers ADD COLUMN ipv6 TEXT DEFAULT ''",
		"ALTER TABLE servers ADD COLUMN net_rx TEXT DEFAULT '0 B/s'",
		"ALTER TABLE servers ADD COLUMN net_tx TEXT DEFAULT '0 B/s'",
		"ALTER TABLE servers ADD COLUMN installed_tools TEXT DEFAULT ''",
		"ALTER TABLE servers ADD COLUMN auth_mode TEXT DEFAULT 'ssh'",
		"ALTER TABLE servers ADD COLUMN peer_code TEXT DEFAULT ''",
		"ALTER TABLE servers ADD COLUMN agent_port INTEGER DEFAULT 2054",
		"ALTER TABLE servers ADD COLUMN ping_ms TEXT DEFAULT '---'",
		"ALTER TABLE servers ADD COLUMN last_speed TEXT DEFAULT ''",
		"ALTER TABLE users ADD COLUMN token_version INTEGER NOT NULL DEFAULT 1",
	} {
		_, _ = s.db.Exec(stmt)
	}

	defaults := [][2]string{
		{"github_repo_url", "https://github.com/htzserv/MControl/releases/latest/download"},
		{"iran_mirror_url", "https://c107328.parspack.net/c107328/MControl"},
		{"panel_github_url", "https://github.com/htzserv/MControl/releases/latest/download"},
		{"panel_mirror_url", "https://c107328.parspack.net/c107328/MControl"},
		{"setup_done", "0"},
		{"web_path", ""},
		{"panel_port", ""},
		{"service_name", "mcontrol-agent"},
		{"probe_interval", "0"},
		{"allow_unverified", "0"},
		{"twofa_enabled", "0"},
		{"twofa_secret", ""},
		{"default_ssh_user", "root"},
		{"default_ssh_port", "22"},
		{"default_ssh_pass_enc", ""},
		{"saved_ports", ""},
		{"panel_name", ""},
		{"listen_domain", ""},
		{"strict_domain", "0"},
	}
	for _, kv := range defaults {
		_, _ = s.db.Exec("INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)", kv[0], kv[1])
	}
	return nil
}

// Snapshot writes a consistent copy of the live database to dst (dst must not exist).
func (s *Store) Snapshot(dst string) error {
	q := "VACUUM INTO '" + strings.ReplaceAll(dst, "'", "''") + "'"
	_, err := s.db.Exec(q)
	return err
}

// ReplaceFiles swaps in a restored database (and optionally its encryption key) and closes the store.
// The caller is expected to exit the process afterwards so the service manager restarts it.
func (s *Store) ReplaceFiles(newDB string, key []byte) error {
	_ = s.db.Close()
	if len(key) >= 32 {
		if err := os.WriteFile(s.keyPath, key, 0600); err != nil {
			return err
		}
	}
	if err := os.Rename(newDB, s.dbPath); err != nil {
		return err
	}
	_ = os.Remove(s.dbPath + "-wal")
	_ = os.Remove(s.dbPath + "-shm")
	return nil
}

func (s *Store) IsSetupDone() bool {
	var done string
	if err := s.db.QueryRow("SELECT value FROM settings WHERE key='setup_done'").Scan(&done); err != nil || done != "1" {
		return false
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil || count == 0 {
		return false
	}
	return true
}

func (s *Store) GetPanelConfig(defaultPort string) (setupDone bool, webPath string, port string) {
	setupDone = s.IsSetupDone()
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key='web_path'").Scan(&webPath)
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key='panel_port'").Scan(&port)
	webPath = strings.Trim(webPath, "/")
	if port == "" {
		port = defaultPort
	}
	return setupDone, webPath, port
}

func (s *Store) CompleteSetup(username, password, webPath string, port int) error {
	username = strings.TrimSpace(username)
	webPath = strings.Trim(strings.TrimSpace(webPath), "/")
	if username == "" || len(username) > 64 || len(password) < MinPasswordLen {
		return errors.New("username required and password must be at least 8 characters")
	}
	if port < 1 || port > 65535 {
		return errors.New("invalid port number")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM users"); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)", username, string(hash), time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('web_path', ?)", webPath); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('panel_port', ?)", strconv.Itoa(port)); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('setup_done', '1')"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (s *Store) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
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

func (s *Store) VerifyUser(username, password string) bool {
	var hash string
	err := s.db.QueryRow("SELECT password_hash FROM users WHERE username = ?", username).Scan(&hash)
	if err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// PasswordTag is a short fingerprint of the current password hash. It is embedded in session
// tokens so that changing the password immediately invalidates every previously issued token.
func (s *Store) PasswordTag(username string) (string, bool) {
	var hash string
	var tv int
	if err := s.db.QueryRow("SELECT password_hash, COALESCE(token_version, 1) FROM users WHERE username = ?", username).Scan(&hash, &tv); err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(hash + ":" + strconv.Itoa(tv)))
	return hex.EncodeToString(sum[:8]), true
}

func (s *Store) BumpTokenVersion(username string) error {
	_, err := s.db.Exec("UPDATE users SET token_version = COALESCE(token_version, 1) + 1 WHERE username = ?", username)
	return err
}

func (s *Store) UnpairServer(id int64) error {
	_, err := s.db.Exec("UPDATE servers SET auth_mode='ssh', peer_code='' WHERE id=?", id)
	return err
}

func (s *Store) UpdateUserPassword(username, newPassword string) error {
	if len(newPassword) < MinPasswordLen {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE users SET password_hash = ? WHERE username = ?", string(hash), username)
	return err
}

func (s *Store) AddServer(srv *Server) (int64, error) {
	encSecret, err := s.Encrypt(srv.Password)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	installedInt := 0
	if srv.CoreInstalled {
		installedInt = 1
	}
	if srv.AuthType != "key" {
		srv.AuthType = "password"
	}
	res, err := s.db.Exec(`
		INSERT INTO servers (name, role, host, port, username, password_enc, auth_type, host_key, last_error,
			country, country_code, datacenter, core_installed, core_version, status, cpu, ram, uptime,
			ipv4, ipv6, net_rx, net_tx, installed_tools, ping_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		srv.Name, srv.Role, srv.Host, srv.Port, srv.Username, encSecret, srv.AuthType, srv.HostKey, srv.LastError,
		srv.Country, srv.CountryCode, srv.Datacenter, installedInt, srv.CoreVersion,
		srv.Status, srv.CPU, srv.RAM, srv.Uptime,
		srv.IPv4, srv.IPv6, srv.NetRx, srv.NetTx, srv.InstalledTools, srv.PingMs, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateServer edits a server. An empty secret keeps the stored one.
func (s *Store) UpdateServer(id int64, name, role, host string, port int, username, authType, secret string, resetHostKey bool) error {
	q := "UPDATE servers SET name=?, role=?, host=?, port=?, username=?, auth_type=?"
	args := []any{name, role, host, port, username, authType}
	if secret != "" {
		enc, err := s.Encrypt(secret)
		if err != nil {
			return err
		}
		q += ", password_enc=?"
		args = append(args, enc)
	}
	if resetHostKey {
		q += ", host_key=''"
	}
	q += " WHERE id=?"
	args = append(args, id)
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) SetGeo(id int64, country, code, datacenter string) error {
	_, err := s.db.Exec("UPDATE servers SET country=?, country_code=?, datacenter=? WHERE id=?", country, code, datacenter, id)
	return err
}

func (s *Store) SetHostKey(id int64, fingerprint string) error {
	_, err := s.db.Exec("UPDATE servers SET host_key=? WHERE id=?", fingerprint, id)
	return err
}

func (s *Store) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`
		SELECT id, name, role, host, port, username,
		       COALESCE(auth_type,'password'), COALESCE(host_key,''), COALESCE(last_error,''),
		       country, country_code, datacenter,
		       core_installed, core_version, status, cpu, ram, uptime,
		       COALESCE(ipv4,''), COALESCE(ipv6,''), COALESCE(net_rx,'0 B/s'), COALESCE(net_tx,'0 B/s'), COALESCE(installed_tools,''),
		       COALESCE(auth_mode,'ssh'), COALESCE(agent_port,2054), COALESCE(ping_ms,'---'), COALESCE(last_speed,''), created_at
		FROM servers ORDER BY role ASC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Server
	for rows.Next() {
		var srv Server
		var inst int
		if err := rows.Scan(
			&srv.ID, &srv.Name, &srv.Role, &srv.Host, &srv.Port, &srv.Username,
			&srv.AuthType, &srv.HostKey, &srv.LastError,
			&srv.Country, &srv.CountryCode, &srv.Datacenter,
			&inst, &srv.CoreVersion, &srv.Status, &srv.CPU, &srv.RAM, &srv.Uptime,
			&srv.IPv4, &srv.IPv6, &srv.NetRx, &srv.NetTx, &srv.InstalledTools,
			&srv.AuthMode, &srv.AgentPort, &srv.PingMs, &srv.LastSpeed, &srv.CreatedAt,
		); err != nil {
			return nil, err
		}
		srv.CoreInstalled = inst == 1
		list = append(list, srv)
	}
	if list == nil {
		list = []Server{}
	}
	return list, nil
}

func (s *Store) GetServerWithSecret(id int64) (*Server, error) {
	var srv Server
	var encPass string
	var inst int
	err := s.db.QueryRow(`
		SELECT id, name, role, host, port, username, password_enc,
		       COALESCE(auth_type,'password'), COALESCE(host_key,''), COALESCE(last_error,''),
		       country, country_code, datacenter,
		       core_installed, core_version, status, cpu, ram, uptime,
		       COALESCE(ipv4,''), COALESCE(ipv6,''), COALESCE(net_rx,'0 B/s'), COALESCE(net_tx,'0 B/s'), COALESCE(installed_tools,''),
		       COALESCE(auth_mode,'ssh'), COALESCE(peer_code,''), COALESCE(agent_port,2054), COALESCE(ping_ms,'---'), COALESCE(last_speed,''), created_at
		FROM servers WHERE id = ?`, id).Scan(
		&srv.ID, &srv.Name, &srv.Role, &srv.Host, &srv.Port, &srv.Username, &encPass,
		&srv.AuthType, &srv.HostKey, &srv.LastError,
		&srv.Country, &srv.CountryCode, &srv.Datacenter,
		&inst, &srv.CoreVersion, &srv.Status, &srv.CPU, &srv.RAM, &srv.Uptime,
		&srv.IPv4, &srv.IPv6, &srv.NetRx, &srv.NetTx, &srv.InstalledTools,
		&srv.AuthMode, &srv.PeerCode, &srv.AgentPort, &srv.PingMs, &srv.LastSpeed, &srv.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	srv.CoreInstalled = inst == 1
	if encPass != "" {
		pass, err := s.Decrypt(encPass)
		if err == nil {
			srv.Password = pass
		}
	}
	return &srv, nil
}

func (s *Store) UpdateServerRuntime(id int64, status, cpu, ram, uptime, ipv4, ipv6, netRx, netTx, tools, pingMs string, coreInstalled bool, coreVer, lastErr string) error {
	inst := 0
	if coreInstalled {
		inst = 1
	}
	_, err := s.db.Exec(`
		UPDATE servers SET status=?, cpu=?, ram=?, uptime=?, ipv4=?, ipv6=?, net_rx=?, net_tx=?, installed_tools=?, ping_ms=?, core_installed=?, core_version=?, last_error=?
		WHERE id=?`, status, cpu, ram, uptime, ipv4, ipv6, netRx, netTx, tools, pingMs, inst, coreVer, lastErr, id)
	return err
}

func (s *Store) UpdatePanelAddress(webPath string, port int, listenDomain string, strictDomain bool) error {
	webPath = strings.Trim(strings.TrimSpace(webPath), "/")
	listenDomain = strings.ToLower(strings.TrimSpace(listenDomain))
	if port < 1 || port > 65535 {
		return errors.New("invalid port number")
	}
	if err := s.setSetting("web_path", webPath); err != nil {
		return err
	}
	if err := s.setSetting("listen_domain", listenDomain); err != nil {
		return err
	}
	sd := "0"
	if strictDomain {
		sd = "1"
	}
	if err := s.setSetting("strict_domain", sd); err != nil {
		return err
	}
	return s.setSetting("panel_port", strconv.Itoa(port))
}

func (s *Store) SetListenDomain(domain string) error {
	return s.setSetting("listen_domain", strings.ToLower(strings.TrimSpace(domain)))
}

func (s *Store) GetDomainConfig() (domain string, strict bool) {
	return strings.ToLower(strings.TrimSpace(s.getSetting("listen_domain"))), s.getSetting("strict_domain") == "1"
}

func (s *Store) UpdateUsername(oldUser, newUser string) error {
	newUser = strings.TrimSpace(newUser)
	if len(newUser) < 3 || len(newUser) > 64 {
		return errors.New("username must be 3-64 characters")
	}
	_, err := s.db.Exec("UPDATE users SET username = ? WHERE username = ?", newUser, oldUser)
	return err
}

func (s *Store) Set2FA(enabled bool, secret string) error {
	val := "0"
	if enabled {
		val = "1"
	}
	if err := s.setSetting("twofa_enabled", val); err != nil {
		return err
	}
	if secret != "" {
		return s.setSetting("twofa_secret", secret)
	}
	return nil
}

func (s *Store) DeleteServer(id int64) error {
	_, err := s.db.Exec("DELETE FROM servers WHERE id = ?", id)
	return err
}

func (s *Store) getSetting(key string) string {
	var v string
	_ = s.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&v)
	return v
}

func (s *Store) setSetting(key, value string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", key, value)
	return err
}

func (s *Store) GetSettings() Settings {
	st := Settings{
		GitHubRepoURL:   s.getSetting("github_repo_url"),
		IranMirrorURL:   s.getSetting("iran_mirror_url"),
		PanelGitHubURL:  s.getSetting("panel_github_url"),
		PanelMirrorURL:  s.getSetting("panel_mirror_url"),
		WebPath:         s.getSetting("web_path"),
		PanelPort:       s.getSetting("panel_port"),
		ServiceName:     s.getSetting("service_name"),
		AllowUnverified: s.getSetting("allow_unverified") == "1",
	}
	st.ProbeInterval, _ = strconv.Atoi(s.getSetting("probe_interval"))
	if st.ServiceName == "" {
		st.ServiceName = "mcontrol-agent"
	}
	_ = s.db.QueryRow("SELECT username FROM users LIMIT 1").Scan(&st.Username)
	st.TwoFAEnabled = s.getSetting("twofa_enabled") == "1"
	st.TwoFASecret = s.getSetting("twofa_secret")
	st.DefaultSSHUser = s.getSetting("default_ssh_user")
	if st.DefaultSSHUser == "" {
		st.DefaultSSHUser = "root"
	}
	st.DefaultSSHPort, _ = strconv.Atoi(s.getSetting("default_ssh_port"))
	if st.DefaultSSHPort <= 0 {
		st.DefaultSSHPort = 22
	}
	if enc := s.getSetting("default_ssh_pass_enc"); enc != "" {
		st.HasDefaultPass = true
		if dec, err := s.Decrypt(enc); err == nil {
			st.DefaultSSHPass = dec
		}
	}
	st.SavedPorts = s.getSetting("saved_ports")
	st.PanelName = s.getSetting("panel_name")
	st.ListenDomain = s.getSetting("listen_domain")
	st.StrictDomain = s.getSetting("strict_domain") == "1"
	return st
}

// SaveSettings stores the editable settings. Empty URL values are ignored (kept as-is).
func (s *Store) SaveSettings(st Settings) error {
	urls := [][2]string{
		{"github_repo_url", st.GitHubRepoURL},
		{"iran_mirror_url", st.IranMirrorURL},
		{"panel_github_url", st.PanelGitHubURL},
		{"panel_mirror_url", st.PanelMirrorURL},
	}
	for _, kv := range urls {
		if kv[1] != "" {
			if err := s.setSetting(kv[0], kv[1]); err != nil {
				return err
			}
		}
	}
	if st.ServiceName != "" {
		if err := s.setSetting("service_name", st.ServiceName); err != nil {
			return err
		}
	}
	if err := s.setSetting("probe_interval", strconv.Itoa(st.ProbeInterval)); err != nil {
		return err
	}
	unv := "0"
	if st.AllowUnverified {
		unv = "1"
	}
	return s.setSetting("allow_unverified", unv)
}

func (s *Store) SetServerPeerMode(id int64, peerCode string, agentPort int) error {
	_, err := s.db.Exec("UPDATE servers SET auth_mode='peer', peer_code=?, agent_port=?, password_enc='', core_installed=1 WHERE id=?", peerCode, agentPort, id)
	return err
}

func (s *Store) SaveDefaults(user string, port int, pass string, clearPass bool, savedPorts string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		user = "root"
	}
	if port < 1 || port > 65535 {
		port = 22
	}
	if err := s.setSetting("default_ssh_user", user); err != nil {
		return err
	}
	if err := s.setSetting("default_ssh_port", strconv.Itoa(port)); err != nil {
		return err
	}
	if clearPass {
		if err := s.setSetting("default_ssh_pass_enc", ""); err != nil {
			return err
		}
	} else if pass != "" {
		enc, err := s.Encrypt(pass)
		if err != nil {
			return err
		}
		if err := s.setSetting("default_ssh_pass_enc", enc); err != nil {
			return err
		}
	}
	return s.setSetting("saved_ports", savedPorts)
}

func (s *Store) SetServerSpeed(id int64, speedStr string) error {
	_, err := s.db.Exec("UPDATE servers SET last_speed=? WHERE id=?", speedStr, id)
	return err
}

func (s *Store) ListSSHProfiles() ([]SSHProfile, error) {
	rows, err := s.db.Query("SELECT id, name, username, port, created_at FROM ssh_profiles ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []SSHProfile
	for rows.Next() {
		var p SSHProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.Username, &p.Port, &p.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	if list == nil {
		list = []SSHProfile{}
	}
	return list, nil
}

func (s *Store) GetSSHProfileWithSecret(id int64) (*SSHProfile, error) {
	var p SSHProfile
	var enc string
	err := s.db.QueryRow("SELECT id, name, username, password_enc, port, created_at FROM ssh_profiles WHERE id=?", id).Scan(&p.ID, &p.Name, &p.Username, &enc, &p.Port, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	if enc != "" {
		if dec, err := s.Decrypt(enc); err == nil {
			p.Password = dec
		}
	}
	return &p, nil
}

func (s *Store) AddSSHProfile(name, username, password string, port int) (int64, error) {
	enc, err := s.Encrypt(password)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec("INSERT INTO ssh_profiles (name, username, password_enc, port, created_at) VALUES (?, ?, ?, ?, ?)", name, username, enc, port, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteSSHProfile(id int64) error {
	_, err := s.db.Exec("DELETE FROM ssh_profiles WHERE id=?", id)
	return err
}

func (s *Store) ListPortProfiles() ([]PortProfile, error) {
	rows, err := s.db.Query("SELECT id, name, ports, created_at FROM port_profiles ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []PortProfile
	for rows.Next() {
		var p PortProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.Ports, &p.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	if list == nil {
		list = []PortProfile{}
	}
	return list, nil
}

func (s *Store) AddPortProfile(name, ports string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO port_profiles (name, ports, created_at) VALUES (?, ?, ?)", name, ports, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeletePortProfile(id int64) error {
	_, err := s.db.Exec("DELETE FROM port_profiles WHERE id=?", id)
	return err
}

func (s *Store) DataDir() string {
	return filepath.Dir(s.dbPath)
}

func (s *Store) UpdateSSHProfile(id int64, name, username, password string, port int) error {
	if password != "" {
		enc, err := s.Encrypt(password)
		if err != nil {
			return err
		}
		_, err = s.db.Exec("UPDATE ssh_profiles SET name=?, username=?, password_enc=?, port=? WHERE id=?", name, username, enc, port, id)
		return err
	}
	_, err := s.db.Exec("UPDATE ssh_profiles SET name=?, username=?, port=? WHERE id=?", name, username, port, id)
	return err
}

func (s *Store) UpdatePortProfile(id int64, name, ports string) error {
	_, err := s.db.Exec("UPDATE port_profiles SET name=?, ports=? WHERE id=?", name, ports, id)
	return err
}

func (s *Store) SetPanelName(name string) error {
	return s.setSetting("panel_name", strings.TrimSpace(name))
}

func (s *Store) ListTunnels() ([]Tunnel, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.name, t.engine, t.transport, t.iran_server_id, t.foreign_server_id,
			COALESCE(si.name, 'Deleted'), COALESCE(si.host, ''),
			COALESCE(sf.name, 'Deleted'), COALESCE(sf.host, ''),
			t.tunnel_port, t.token, t.ports, t.forwarder, t.subnet, t.vip_targets, t.lb_mode, t.status, t.last_error, t.created_at
		FROM tunnels t
		LEFT JOIN servers si ON si.id = t.iran_server_id
		LEFT JOIN servers sf ON sf.id = t.foreign_server_id
		ORDER BY t.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Tunnel
	for rows.Next() {
		var t Tunnel
		if err := rows.Scan(&t.ID, &t.Name, &t.Engine, &t.Transport, &t.IranServerID, &t.ForeignServerID,
			&t.IranName, &t.IranHost, &t.ForeignName, &t.ForeignHost,
			&t.TunnelPort, &t.Token, &t.Ports, &t.Forwarder, &t.Subnet, &t.VIPTargets, &t.LBMode, &t.Status, &t.LastError, &t.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	if list == nil {
		list = []Tunnel{}
	}
	return list, nil
}

func (s *Store) GetTunnel(id int64) (*Tunnel, error) {
	var t Tunnel
	err := s.db.QueryRow(`
		SELECT t.id, t.name, t.engine, t.transport, t.iran_server_id, t.foreign_server_id,
			COALESCE(si.name, 'Deleted'), COALESCE(si.host, ''),
			COALESCE(sf.name, 'Deleted'), COALESCE(sf.host, ''),
			t.tunnel_port, t.token, t.ports, t.forwarder, t.subnet, t.vip_targets, t.lb_mode, t.status, t.last_error, t.created_at
		FROM tunnels t
		LEFT JOIN servers si ON si.id = t.iran_server_id
		LEFT JOIN servers sf ON sf.id = t.foreign_server_id
		WHERE t.id = ?`, id).Scan(&t.ID, &t.Name, &t.Engine, &t.Transport, &t.IranServerID, &t.ForeignServerID,
		&t.IranName, &t.IranHost, &t.ForeignName, &t.ForeignHost,
		&t.TunnelPort, &t.Token, &t.Ports, &t.Forwarder, &t.Subnet, &t.VIPTargets, &t.LBMode, &t.Status, &t.LastError, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) AddTunnel(t *Tunnel) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO tunnels (name, engine, transport, iran_server_id, foreign_server_id, tunnel_port, token, ports, forwarder, subnet, vip_targets, lb_mode, status, last_error, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.Engine, t.Transport, t.IranServerID, t.ForeignServerID, t.TunnelPort, t.Token, t.Ports, t.Forwarder, t.Subnet, t.VIPTargets, t.LBMode, t.Status, t.LastError, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateTunnelStatus(id int64, status, lastErr string) error {
	_, err := s.db.Exec("UPDATE tunnels SET status=?, last_error=? WHERE id=?", status, lastErr, id)
	return err
}

func (s *Store) DeleteTunnel(id int64) error {
	_, err := s.db.Exec("DELETE FROM tunnels WHERE id=?", id)
	return err
}

func (s *Store) AddBenchmarkRun(r *BenchmarkRun) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO benchmark_runs (iran_name, foreign_name, profile, results_json, logs_text, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		r.IranName, r.ForeignName, r.Profile, r.ResultsJSON, r.LogsText, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListBenchmarkRuns(limit int) ([]BenchmarkRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		"SELECT id, iran_name, foreign_name, profile, results_json, logs_text, created_at FROM benchmark_runs ORDER BY id DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []BenchmarkRun
	for rows.Next() {
		var r BenchmarkRun
		if err := rows.Scan(&r.ID, &r.IranName, &r.ForeignName, &r.Profile, &r.ResultsJSON, &r.LogsText, &r.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if list == nil {
		list = []BenchmarkRun{}
	}
	return list, nil
}
