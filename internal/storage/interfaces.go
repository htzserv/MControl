package storage

import (
	"context"
	"time"
)

type Role string

const (
	RoleSuperAdmin Role = "superadmin"
	RoleTenant     Role = "tenant"
)

type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	PassHash    string     `json:"-"`
	Role        Role       `json:"role"`
	Active      bool       `json:"active"`
	MaxServers  int        `json:"max_servers"`
	MaxTunnels  int        `json:"max_tunnels"`
	MaxMonitors int        `json:"max_monitors"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Branding struct {
	AppName  string `json:"app_name"`
	Subtitle string `json:"subtitle"`
	Accent   string `json:"accent"`
}

type Server struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Role       string    `json:"role"` // iran | foreign
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Username   string    `json:"username"`
	Secret     string    `json:"secret,omitempty"`
	AuthType   string    `json:"auth_type"` // password | key
	HasSecret  bool      `json:"has_secret"`
	Country    string    `json:"country"`
	Datacenter string    `json:"datacenter"`
	Status     string    `json:"status"` // online | offline | unknown
	CPU        string    `json:"cpu"`
	RAM        string    `json:"ram"`
	Uptime     string    `json:"uptime"`
	IPv4       string    `json:"ipv4"`
	IPv6       string    `json:"ipv6"`
	PingMs     string    `json:"ping_ms"`
	LastSpeed  string    `json:"last_speed"`
	LastError  string    `json:"last_error"`
	SortOrder  int       `json:"sort_order"`
	PosX       float64   `json:"pos_x"`
	PosY       float64   `json:"pos_y"`
	CreatedAt  time.Time `json:"created_at"`
}

type Tunnel struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Engine          string    `json:"engine"`    // backhaul | paqet | rathole | gre | ip6gre | vxlan
	Transport       string    `json:"transport"` // tcp | ws | tcpmux | udp
	IranServerID    int64     `json:"iran_server_id"`
	ForeignServerID int64     `json:"foreign_server_id"`
	TunnelPort      int       `json:"tunnel_port"`
	Ports           string    `json:"ports"`
	Token           string    `json:"token"`
	LocalIP         string    `json:"local_ip"`
	RemoteIP        string    `json:"remote_ip"`
	MTU             int       `json:"mtu"`
	Status          string    `json:"status"` // draft | active | stopped | error
	LastPing        string    `json:"last_ping"`
	LastLog         string    `json:"last_log"`
	CreatedAt       time.Time `json:"created_at"`
}

type LayoutItem struct {
	Section   string  `json:"section"` // servers | topology
	ItemID    string  `json:"item_id"`
	SortIndex int     `json:"sort_index"`
	PosX      float64 `json:"pos_x"`
	PosY      float64 `json:"pos_y"`
}

type ThemePrefs struct {
	Preset string `json:"preset"`
	Mode   string `json:"mode"` // dark | light
	Accent string `json:"accent"`
	Radius int    `json:"radius"`
	Lang   string `json:"lang"` // fa | en
}


type Monitor struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`   // http | tcp
	Target      string    `json:"target"` // https://... or host:port
	IntervalSec int       `json:"interval_sec"`
	Status      string    `json:"status"` // up | down | unknown
	LatencyMs   int       `json:"latency_ms"`
	UptimePct   float64   `json:"uptime_pct"`
	LastError   string    `json:"last_error"`
	Public      bool      `json:"public"`
	History     []int     `json:"history"` // 1=up, 0=down (last 30 checks)
	CreatedAt   time.Time `json:"created_at"`
}

type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type DNSRule struct {
	ID         int64     `json:"id"`
	Provider   string    `json:"provider"` // cloudflare | arvancloud
	Domain     string    `json:"domain"`
	ZoneID     string    `json:"zone_id"`
	RecordID   string    `json:"record_id"`
	RecordName string    `json:"record_name"`
	APIToken   string    `json:"api_token,omitempty"`
	HasToken   bool      `json:"has_token"`
	PrimaryIP  string    `json:"primary_ip"`
	BackupIP   string    `json:"backup_ip"`
	ActiveIP   string    `json:"active_ip"`
	Status     string    `json:"status"`
	LastSwitch string    `json:"last_switch"`
	CreatedAt  time.Time `json:"created_at"`
}

type CloudAccount struct {
	ID        int64     `json:"id"`
	Provider  string    `json:"provider"` // hetzner | digitalocean | vultr
	Label     string    `json:"label"`
	APIToken  string    `json:"api_token,omitempty"`
	HasToken  bool      `json:"has_token"`
	CreatedAt time.Time `json:"created_at"`
}

type SystemStats struct {
	TotalUsers    int `json:"total_users"`
	ActiveTenants int `json:"active_tenants"`
	TotalServers  int `json:"total_servers"`
	TotalTunnels  int `json:"total_tunnels"`
}

type Engine interface {
	IsInitialized(ctx context.Context) (bool, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByID(ctx context.Context, id int64) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, u *User) (int64, error)
	UpdateUser(ctx context.Context, u *User, newPassHash string) error
	DeleteUser(ctx context.Context, id int64) error

	GetBranding(ctx context.Context) (Branding, error)
	SaveBranding(ctx context.Context, b Branding) error
	GetSystemStats(ctx context.Context) (SystemStats, error)

	// Per-Tenant isolated operations
	ListServers(ctx context.Context, tenantID int64) ([]Server, error)
	GetServerWithSecret(ctx context.Context, tenantID, serverID int64) (*Server, error)
	AddServer(ctx context.Context, tenantID int64, s *Server) (int64, error)
	UpdateServer(ctx context.Context, tenantID int64, s *Server) error
	UpdateServerRuntime(ctx context.Context, tenantID, serverID int64, status, cpu, ram, uptime, ipv4, ipv6, pingMs, lastErr string) error
	UpdateServerSpeed(ctx context.Context, tenantID, serverID int64, lastSpeed string) error
	DeleteServer(ctx context.Context, tenantID, serverID int64) error

	ListTunnels(ctx context.Context, tenantID int64) ([]Tunnel, error)
	GetTunnel(ctx context.Context, tenantID, tunnelID int64) (*Tunnel, error)
	AddTunnel(ctx context.Context, tenantID int64, t *Tunnel) (int64, error)
	UpdateTunnelRuntime(ctx context.Context, tenantID, tunnelID int64, status, lastLog, lastPing string) error
	DeleteTunnel(ctx context.Context, tenantID, tunnelID int64) error

	SaveLayout(ctx context.Context, tenantID int64, items []LayoutItem) error
	GetThemePrefs(ctx context.Context, tenantID int64) (ThemePrefs, error)
	SaveThemePrefs(ctx context.Context, tenantID int64, prefs ThemePrefs) error

	// Package 3: Per-Tenant Uptime, Telegram, DNS Failover & Cloud
	ListMonitors(ctx context.Context, tenantID int64) ([]Monitor, error)
	AddMonitor(ctx context.Context, tenantID int64, m *Monitor) (int64, error)
	DeleteMonitor(ctx context.Context, tenantID, monitorID int64) error
	RecordHeartbeat(ctx context.Context, tenantID, monitorID int64, up bool, latencyMs int, errMsg string) error
	GetTelegramConfig(ctx context.Context, tenantID int64) (TelegramConfig, error)
	SaveTelegramConfig(ctx context.Context, tenantID int64, cfg TelegramConfig) error

	ListDNSRules(ctx context.Context, tenantID int64) ([]DNSRule, error)
	GetDNSRuleWithToken(ctx context.Context, tenantID, ruleID int64) (*DNSRule, error)
	AddDNSRule(ctx context.Context, tenantID int64, r *DNSRule) (int64, error)
	UpdateDNSRuleActive(ctx context.Context, tenantID, ruleID int64, activeIP, status, lastSwitch string) error
	DeleteDNSRule(ctx context.Context, tenantID, ruleID int64) error

	ListCloudAccounts(ctx context.Context, tenantID int64) ([]CloudAccount, error)
	GetCloudAccountWithToken(ctx context.Context, tenantID, accID int64) (*CloudAccount, error)
	AddCloudAccount(ctx context.Context, tenantID int64, a *CloudAccount) (int64, error)
	DeleteCloudAccount(ctx context.Context, tenantID, accID int64) error

	MigrateLegacyV1(legacyDBPath string, targetTenantID int64) (bool, error)
	Close() error
}
