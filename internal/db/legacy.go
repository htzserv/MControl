package db

import (
	"time"
)

// LegacySnapshot is an exact, encrypted copy of everything MControl read from an
// old MTunnel installation before it was removed (all files, verbatim).
type LegacySnapshot struct {
	ID        int64  `json:"id"`
	ServerID  int64  `json:"server_id"`
	CreatedAt int64  `json:"created_at"`
	Data      string `json:"-"` // decrypted JSON (only filled by GetLatestLegacySnapshot)
}

// LegacyTunnel is one tunnel side (Iran or Foreign) found on a server that ran
// MTunnel. Two sides that belong together are paired into one MControl tunnel.
type LegacyTunnel struct {
	ID              int64  `json:"id"`
	ServerID        int64  `json:"server_id"`
	Side            string `json:"side"`   // "iran" (server role) | "foreign" (client role)
	Engine          string `json:"engine"` // backhaul | rathole | paqet | gre | vxlan
	Name            string `json:"name"`
	Transport       string `json:"transport"`
	TunnelPort      int    `json:"tunnel_port"`
	Token           string `json:"token,omitempty"`
	Ports           string `json:"ports"`
	PeerHost        string `json:"peer_host"` // REMOTE_IP as stored by MTunnel
	PairKey         string `json:"pair_key"`  // shared secret used to match both sides (kernel tunnels)
	Config          string `json:"-"`         // verbatim main config (toml / yaml / conf), decrypted
	Status          string `json:"status"`    // pending | migrated | snapshot_only | error
	Note            string `json:"note"`
	MatchedTunnelID int64  `json:"matched_tunnel_id"`
	CreatedAt       int64  `json:"created_at"`
}

func (s *Store) migrateLegacy() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS legacy_snapshots (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id INTEGER NOT NULL,
		data_enc TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS legacy_tunnels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id INTEGER NOT NULL,
		side TEXT NOT NULL,
		engine TEXT NOT NULL,
		name TEXT NOT NULL,
		transport TEXT NOT NULL DEFAULT '',
		tunnel_port INTEGER NOT NULL DEFAULT 0,
		token_enc TEXT NOT NULL DEFAULT '',
		ports TEXT NOT NULL DEFAULT '',
		peer_host TEXT NOT NULL DEFAULT '',
		pair_key TEXT NOT NULL DEFAULT '',
		config_enc TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		note TEXT NOT NULL DEFAULT '',
		matched_tunnel_id INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);`)
	return err
}

func (s *Store) AddLegacySnapshot(serverID int64, jsonData string) (int64, error) {
	enc, err := s.Encrypt(jsonData)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec("INSERT INTO legacy_snapshots (server_id, data_enc, created_at) VALUES (?, ?, ?)",
		serverID, enc, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetLatestLegacySnapshot(serverID int64) (*LegacySnapshot, error) {
	var snap LegacySnapshot
	var enc string
	err := s.db.QueryRow("SELECT id, server_id, data_enc, created_at FROM legacy_snapshots WHERE server_id = ? ORDER BY id DESC LIMIT 1",
		serverID).Scan(&snap.ID, &snap.ServerID, &enc, &snap.CreatedAt)
	if err != nil {
		return nil, err
	}
	plain, err := s.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	snap.Data = plain
	return &snap, nil
}

// AddLegacyTunnel stores one tunnel side. A server is migrated at most once per
// (engine, name) so re-running a migration never creates duplicates.
func (s *Store) AddLegacyTunnel(t *LegacyTunnel) (int64, error) {
	var existing int64
	err := s.db.QueryRow("SELECT id FROM legacy_tunnels WHERE server_id = ? AND engine = ? AND name = ?",
		t.ServerID, t.Engine, t.Name).Scan(&existing)
	if err == nil && existing > 0 {
		return existing, nil
	}
	tokenEnc, err := s.Encrypt(t.Token)
	if err != nil {
		return 0, err
	}
	cfgEnc, err := s.Encrypt(t.Config)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`
		INSERT INTO legacy_tunnels (server_id, side, engine, name, transport, tunnel_port, token_enc, ports, peer_host, pair_key, config_enc, status, note, matched_tunnel_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		t.ServerID, t.Side, t.Engine, t.Name, t.Transport, t.TunnelPort, tokenEnc, t.Ports, t.PeerHost, t.PairKey, cfgEnc, t.Status, t.Note, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListLegacyTunnels returns staged tunnel sides. serverID 0 = all servers; status "" = any.
func (s *Store) ListLegacyTunnels(serverID int64, status string) ([]LegacyTunnel, error) {
	q := "SELECT id, server_id, side, engine, name, transport, tunnel_port, token_enc, ports, peer_host, pair_key, config_enc, status, note, matched_tunnel_id, created_at FROM legacy_tunnels WHERE 1=1"
	var args []any
	if serverID > 0 {
		q += " AND server_id = ?"
		args = append(args, serverID)
	}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY id"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []LegacyTunnel{}
	for rows.Next() {
		var t LegacyTunnel
		var tokEnc, cfgEnc string
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Side, &t.Engine, &t.Name, &t.Transport, &t.TunnelPort, &tokEnc, &t.Ports, &t.PeerHost, &t.PairKey, &cfgEnc, &t.Status, &t.Note, &t.MatchedTunnelID, &t.CreatedAt); err != nil {
			return nil, err
		}
		if tokEnc != "" {
			t.Token, _ = s.Decrypt(tokEnc)
		}
		if cfgEnc != "" {
			t.Config, _ = s.Decrypt(cfgEnc)
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (s *Store) SetLegacyTunnelState(id int64, status, note string, matchedTunnelID int64) error {
	_, err := s.db.Exec("UPDATE legacy_tunnels SET status = ?, note = ?, matched_tunnel_id = ? WHERE id = ?",
		status, note, matchedTunnelID, id)
	return err
}

func (s *Store) GetLegacyTunnel(id int64) (*LegacyTunnel, error) {
	var t LegacyTunnel
	var tokEnc, cfgEnc string
	err := s.db.QueryRow("SELECT id, server_id, side, engine, name, transport, tunnel_port, token_enc, ports, peer_host, pair_key, config_enc, status, note, matched_tunnel_id, created_at FROM legacy_tunnels WHERE id = ?", id).Scan(
		&t.ID, &t.ServerID, &t.Side, &t.Engine, &t.Name, &t.Transport, &t.TunnelPort, &tokEnc, &t.Ports, &t.PeerHost, &t.PairKey, &cfgEnc, &t.Status, &t.Note, &t.MatchedTunnelID, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if tokEnc != "" {
		t.Token, _ = s.Decrypt(tokEnc)
	}
	if cfgEnc != "" {
		t.Config, _ = s.Decrypt(cfgEnc)
	}
	return &t, nil
}

func (s *Store) DeleteLegacyTunnel(id int64) error {
	_, err := s.db.Exec("DELETE FROM legacy_tunnels WHERE id = ?", id)
	return err
}
