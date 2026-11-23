package db

import (
	"database/sql"
	"time"
)

type Monitor struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`       // http | tcp | ping | dns
	Target         string `json:"target"`     // URL, host:port, IP/host, domain
	Expected       string `json:"expected"`   // optional keyword / IP / status code
	IntervalSec    int    `json:"interval_sec"`
	TimeoutSec     int    `json:"timeout_sec"`
	Retries        int    `json:"retries"`
	ProbeFrom      string `json:"probe_from"` // master | outbound | server:<id>
	LinkedServerID int64  `json:"linked_server_id"`
	LinkedTunnelID int64  `json:"linked_tunnel_id"`
	AutoHeal       string `json:"auto_heal"` // none | restart_tunnel
	LatencyWarnMs  int    `json:"latency_warn_ms"`
	IsPublic       bool   `json:"is_public"`
	PublicName     string `json:"public_name"`
	Enabled        bool   `json:"enabled"`
	Status         string `json:"status"` // up | down | pending
	FailStreak     int    `json:"fail_streak"`
	LastLatencyMs  int    `json:"last_latency_ms"`
	LastError      string `json:"last_error"`
	SSLExpiryDays  int    `json:"ssl_expiry_days"` // -1 = N/A
	LastCheckAt    int64  `json:"last_check_at"`
	DownSince      int64  `json:"down_since"`
	CreatedAt      int64  `json:"created_at"`
}

type MonitorBeat struct {
	ID        int64  `json:"id"`
	MonitorID int64  `json:"monitor_id"`
	Status    string `json:"status"` // up | down | warn
	LatencyMs int    `json:"latency_ms"`
	Msg       string `json:"msg"`
	CreatedAt int64  `json:"created_at"`
}

type MonitorIncident struct {
	ID          int64  `json:"id"`
	MonitorID   int64  `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	Kind        string `json:"kind"` // down | up | heal | failover | quota | ssl | replace
	Message     string `json:"message"`
	DurationSec int64  `json:"duration_sec"`
	CreatedAt   int64  `json:"created_at"`
}

type MetricPoint struct {
	Target    string  `json:"target"`
	CPU       float64 `json:"cpu"`
	RAM       float64 `json:"ram"`
	PingMs    float64 `json:"ping_ms"`
	RxBytes   int64   `json:"rx_bytes"`
	TxBytes   int64   `json:"tx_bytes"`
	CreatedAt int64   `json:"created_at"`
}

func (s *Store) migrateMonitor() error {
	schema := `
	CREATE TABLE IF NOT EXISTS monitors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT 'http',
		target TEXT NOT NULL,
		expected TEXT NOT NULL DEFAULT '',
		interval_sec INTEGER NOT NULL DEFAULT 60,
		timeout_sec INTEGER NOT NULL DEFAULT 10,
		retries INTEGER NOT NULL DEFAULT 2,
		probe_from TEXT NOT NULL DEFAULT 'master',
		linked_server_id INTEGER NOT NULL DEFAULT 0,
		linked_tunnel_id INTEGER NOT NULL DEFAULT 0,
		auto_heal TEXT NOT NULL DEFAULT 'none',
		latency_warn_ms INTEGER NOT NULL DEFAULT 0,
		is_public INTEGER NOT NULL DEFAULT 0,
		public_name TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		status TEXT NOT NULL DEFAULT 'pending',
		fail_streak INTEGER NOT NULL DEFAULT 0,
		last_latency_ms INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT '',
		ssl_expiry_days INTEGER NOT NULL DEFAULT -1,
		last_check_at INTEGER NOT NULL DEFAULT 0,
		down_since INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS monitor_beats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id INTEGER NOT NULL,
		status TEXT NOT NULL,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		msg TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_monitor_beats_mid ON monitor_beats(monitor_id, created_at DESC);
	CREATE TABLE IF NOT EXISTS monitor_incidents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id INTEGER NOT NULL DEFAULT 0,
		monitor_name TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT 'down',
		message TEXT NOT NULL DEFAULT '',
		duration_sec INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_monitor_incidents_ts ON monitor_incidents(created_at DESC);
	CREATE TABLE IF NOT EXISTS metrics_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target TEXT NOT NULL,
		cpu REAL NOT NULL DEFAULT 0,
		ram REAL NOT NULL DEFAULT 0,
		ping_ms REAL NOT NULL DEFAULT 0,
		rx_bytes INTEGER NOT NULL DEFAULT 0,
		tx_bytes INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_metrics_history_tgt ON metrics_history(target, created_at DESC);
	`
	_, err := s.db.Exec(schema)
	return err
}

func scanMonitor(row interface{ Scan(...any) error }) (*Monitor, error) {
	var m Monitor
	var isPub, en int
	err := row.Scan(
		&m.ID, &m.Name, &m.Type, &m.Target, &m.Expected,
		&m.IntervalSec, &m.TimeoutSec, &m.Retries, &m.ProbeFrom,
		&m.LinkedServerID, &m.LinkedTunnelID, &m.AutoHeal, &m.LatencyWarnMs,
		&isPub, &m.PublicName, &en, &m.Status, &m.FailStreak,
		&m.LastLatencyMs, &m.LastError, &m.SSLExpiryDays,
		&m.LastCheckAt, &m.DownSince, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	m.IsPublic = isPub == 1
	m.Enabled = en == 1
	return &m, nil
}

const monitorCols = `id, name, type, target, expected, interval_sec, timeout_sec, retries, probe_from,
	linked_server_id, linked_tunnel_id, auto_heal, latency_warn_ms, is_public, public_name, enabled,
	status, fail_streak, last_latency_ms, last_error, ssl_expiry_days, last_check_at, down_since, created_at`

func (s *Store) ListMonitors() ([]Monitor, error) {
	rows, err := s.db.Query("SELECT " + monitorCols + " FROM monitors ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, nil
}

func (s *Store) GetMonitor(id int64) (*Monitor, error) {
	row := s.db.QueryRow("SELECT "+monitorCols+" FROM monitors WHERE id = ?", id)
	return scanMonitor(row)
}

func (s *Store) AddMonitor(m *Monitor) (int64, error) {
	now := time.Now().Unix()
	if m.IntervalSec < 15 {
		m.IntervalSec = 60
	}
	if m.TimeoutSec < 2 {
		m.TimeoutSec = 10
	}
	if m.Retries < 1 {
		m.Retries = 2
	}
	if m.ProbeFrom == "" {
		m.ProbeFrom = "master"
	}
	if m.AutoHeal == "" {
		m.AutoHeal = "none"
	}
	if m.Status == "" {
		m.Status = "pending"
	}
	isPub := 0
	if m.IsPublic {
		isPub = 1
	}
	en := 0
	if m.Enabled {
		en = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO monitors (name, type, target, expected, interval_sec, timeout_sec, retries, probe_from,
			linked_server_id, linked_tunnel_id, auto_heal, latency_warn_ms, is_public, public_name, enabled,
			status, ssl_expiry_days, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, -1, ?)`,
		m.Name, m.Type, m.Target, m.Expected, m.IntervalSec, m.TimeoutSec, m.Retries, m.ProbeFrom,
		m.LinkedServerID, m.LinkedTunnelID, m.AutoHeal, m.LatencyWarnMs, isPub, m.PublicName, en,
		m.Status, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateMonitor(m *Monitor) error {
	if m.IntervalSec < 15 {
		m.IntervalSec = 60
	}
	if m.TimeoutSec < 2 {
		m.TimeoutSec = 10
	}
	if m.Retries < 1 {
		m.Retries = 2
	}
	isPub := 0
	if m.IsPublic {
		isPub = 1
	}
	en := 0
	if m.Enabled {
		en = 1
	}
	_, err := s.db.Exec(`
		UPDATE monitors
		SET name=?, type=?, target=?, expected=?, interval_sec=?, timeout_sec=?, retries=?, probe_from=?,
		    linked_server_id=?, linked_tunnel_id=?, auto_heal=?, latency_warn_ms=?, is_public=?, public_name=?, enabled=?
		WHERE id=?`,
		m.Name, m.Type, m.Target, m.Expected, m.IntervalSec, m.TimeoutSec, m.Retries, m.ProbeFrom,
		m.LinkedServerID, m.LinkedTunnelID, m.AutoHeal, m.LatencyWarnMs, isPub, m.PublicName, en, m.ID,
	)
	return err
}

func (s *Store) UpdateMonitorState(id int64, status string, failStreak, latencyMs, sslDays int, lastErr string, lastCheckAt, downSince int64) error {
	_, err := s.db.Exec(`
		UPDATE monitors
		SET status=?, fail_streak=?, last_latency_ms=?, ssl_expiry_days=?, last_error=?, last_check_at=?, down_since=?
		WHERE id=?`,
		status, failStreak, latencyMs, sslDays, lastErr, lastCheckAt, downSince, id,
	)
	return err
}

func (s *Store) DeleteMonitor(id int64) error {
	_, _ = s.db.Exec("DELETE FROM monitor_beats WHERE monitor_id = ?", id)
	_, err := s.db.Exec("DELETE FROM monitors WHERE id = ?", id)
	return err
}

func (s *Store) AddMonitorBeat(monitorID int64, status string, latencyMs int, msg string) error {
	_, err := s.db.Exec(`INSERT INTO monitor_beats (monitor_id, status, latency_ms, msg, created_at) VALUES (?, ?, ?, ?, ?)`,
		monitorID, status, latencyMs, msg, time.Now().Unix())
	return err
}

func (s *Store) ListMonitorBeats(monitorID int64, limit int) ([]MonitorBeat, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.Query(`SELECT id, monitor_id, status, latency_ms, msg, created_at FROM monitor_beats WHERE monitor_id = ? ORDER BY created_at DESC LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rev := []MonitorBeat{}
	for rows.Next() {
		var b MonitorBeat
		if err := rows.Scan(&b.ID, &b.MonitorID, &b.Status, &b.LatencyMs, &b.Msg, &b.CreatedAt); err != nil {
			return nil, err
		}
		rev = append(rev, b)
	}
	// Reverse to chronological order (oldest -> newest) for heartbeat bars
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

func (s *Store) GetMonitorUptimeStats(monitorID int64) (float64, float64, float64) {
	now := time.Now().Unix()
	calc := func(since int64) float64 {
		var total, up int
		err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status IN ('up','warn') THEN 1 ELSE 0 END), 0) FROM monitor_beats WHERE monitor_id = ? AND created_at >= ?`, monitorID, since).Scan(&total, &up)
		if err != nil || total == 0 {
			return 100.0
		}
		pct := float64(up) * 100.0 / float64(total)
		return float64(int(pct*100+0.5)) / 100.0
	}
	return calc(now - 86400), calc(now - 7*86400), calc(now - 30*86400)
}

func (s *Store) PruneMonitorHistory(days int) {
	if days < 1 {
		days = 30
	}
	cutoff := time.Now().Unix() - int64(days)*86400
	_, _ = s.db.Exec("DELETE FROM monitor_beats WHERE created_at < ?", cutoff)
	_, _ = s.db.Exec("DELETE FROM monitor_incidents WHERE created_at < ?", cutoff)
}

func (s *Store) AddIncident(monitorID int64, name, kind, message string, durationSec int64) error {
	_, err := s.db.Exec(`INSERT INTO monitor_incidents (monitor_id, monitor_name, kind, message, duration_sec, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		monitorID, name, kind, message, durationSec, time.Now().Unix())
	return err
}

func (s *Store) ListIncidents(limit int) ([]MonitorIncident, error) {
	if limit <= 0 {
		limit = 40
	}
	rows, err := s.db.Query(`SELECT id, monitor_id, monitor_name, kind, message, duration_sec, created_at FROM monitor_incidents ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonitorIncident{}
	for rows.Next() {
		var inc MonitorIncident
		if err := rows.Scan(&inc.ID, &inc.MonitorID, &inc.MonitorName, &inc.Kind, &inc.Message, &inc.DurationSec, &inc.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, nil
}

func (s *Store) AddMetricPoint(target string, cpu, ram, pingMs float64, rxBytes, txBytes int64) error {
	_, err := s.db.Exec(`INSERT INTO metrics_history (target, cpu, ram, ping_ms, rx_bytes, tx_bytes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		target, cpu, ram, pingMs, rxBytes, txBytes, time.Now().Unix())
	return err
}

func (s *Store) ListMetricPoints(target string, since int64) ([]MetricPoint, error) {
	rows, err := s.db.Query(`SELECT target, cpu, ram, ping_ms, rx_bytes, tx_bytes, created_at FROM metrics_history WHERE target = ? AND created_at >= ? ORDER BY created_at ASC LIMIT 400`, target, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.Target, &p.CPU, &p.RAM, &p.PingMs, &p.RxBytes, &p.TxBytes, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) PruneMetricPoints(days int) {
	if days < 1 {
		days = 7
	}
	cutoff := time.Now().Unix() - int64(days)*86400
	_, _ = s.db.Exec("DELETE FROM metrics_history WHERE created_at < ?", cutoff)
}

func (s *Store) ReplaceTunnelServerID(oldID, newID int64) (int64, error) {
	var total int64
	r1, err := s.db.Exec("UPDATE tunnels SET iran_server_id = ? WHERE iran_server_id = ?", newID, oldID)
	if err != nil {
		return 0, err
	}
	n1, _ := r1.RowsAffected()
	total += n1
	r2, err := s.db.Exec("UPDATE tunnels SET foreign_server_id = ? WHERE foreign_server_id = ?", newID, oldID)
	if err != nil {
		return total, err
	}
	n2, _ := r2.RowsAffected()
	total += n2
	return total, nil
}

// Unused sql import guard
var _ = sql.ErrNoRows
