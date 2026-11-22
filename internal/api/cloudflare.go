package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/updater"
)

type cfConfig struct {
	Enabled  bool   `json:"enabled"`
	AuthMode string `json:"auth_mode"` // "token" or "global"
	APIToken string `json:"api_token"`
	Email    string `json:"email"`
	APIKey   string `json:"api_key"`
}

type cfZone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type cfDNSRecord struct {
	ID         string `json:"id"`
	ZoneID     string `json:"zone_id"`
	ZoneName   string `json:"zone_name"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	TTL        int    `json:"ttl"`
	Proxied    bool   `json:"proxied"`
	ServerID   int64  `json:"server_id,omitempty"`
	ServerName string `json:"server_name,omitempty"`
	ServerRole string `json:"server_role,omitempty"`
}

type cfCache struct {
	SyncedAt int64         `json:"synced_at"`
	Zones    []cfZone      `json:"zones"`
	Records  []cfDNSRecord `json:"records"`
}

func (h *Handler) loadCFConfig() cfConfig {
	raw := h.store.GetSetting("cloudflare_cfg_json")
	cfg := cfConfig{AuthMode: "token"}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "token"
	}
	return cfg
}

func (h *Handler) saveCFConfig(cfg cfConfig) {
	if b, err := json.Marshal(cfg); err == nil {
		_ = h.store.SetSetting("cloudflare_cfg_json", string(b))
	}
}

func (h *Handler) loadCFCache() cfCache {
	raw := h.store.GetSetting("cloudflare_cache_json")
	c := cfCache{Zones: []cfZone{}, Records: []cfDNSRecord{}}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &c)
	}
	if c.Zones == nil {
		c.Zones = []cfZone{}
	}
	if c.Records == nil {
		c.Records = []cfDNSRecord{}
	}
	return c
}

func (h *Handler) saveCFCache(c cfCache) {
	if b, err := json.Marshal(c); err == nil {
		_ = h.store.SetSetting("cloudflare_cache_json", string(b))
	}
}

func (h *Handler) cfHTTPDo(method, urlStr string, cfg cfConfig, payload any) ([]byte, error) {
	var bodyBytes []byte
	if payload != nil {
		bodyBytes, _ = json.Marshal(payload)
	}
	buildReq := func() (*http.Request, error) {
		var rdr io.Reader
		if bodyBytes != nil {
			rdr = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequest(method, urlStr, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.AuthMode == "global" && cfg.Email != "" && cfg.APIKey != "" {
			req.Header.Set("X-Auth-Email", cfg.Email)
			req.Header.Set("X-Auth-Key", cfg.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+cfg.APIToken)
		}
		return req, nil
	}

	// Route through Master Outbound if active, with automatic direct fallback
	if proxy := h.githubSocksProxy(); proxy != "" {
		if req, err := buildReq(); err == nil {
			client := updater.MakeHTTPClient(15*time.Second, proxy)
			if resp, err := client.Do(req); err == nil {
				raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
				resp.Body.Close()
				if rerr == nil {
					return raw, nil
				}
			}
		}
	}

	req, err := buildReq()
	if err != nil {
		return nil, err
	}
	client := updater.MakeHTTPClient(15*time.Second, "")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 10<<20))
}

func parseCFError(raw []byte) (bool, string) {
	var env struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return false, "invalid Cloudflare API response"
	}
	if env.Success {
		return true, ""
	}
	if len(env.Errors) > 0 {
		return false, env.Errors[0].Message
	}
	return false, "Cloudflare API request failed"
}

func (h *Handler) syncCloudflareAll(cfg cfConfig) (cfCache, error) {
	if cfg.AuthMode == "global" {
		if cfg.Email == "" || cfg.APIKey == "" {
			return cfCache{}, fmt.Errorf("Cloudflare Email and Global API Key are required")
		}
	} else if cfg.APIToken == "" {
		return cfCache{}, fmt.Errorf("Cloudflare API Token is required")
	}

	rawZ, err := h.cfHTTPDo("GET", "https://api.cloudflare.com/client/v4/zones?per_page=50", cfg, nil)
	if err != nil {
		return cfCache{}, err
	}
	if ok, msg := parseCFError(rawZ); !ok {
		return cfCache{}, fmt.Errorf("Cloudflare: %s", msg)
	}
	var zResp struct {
		Result []cfZone `json:"result"`
	}
	_ = json.Unmarshal(rawZ, &zResp)

	cache := cfCache{
		SyncedAt: time.Now().Unix(),
		Zones:    zResp.Result,
		Records:  []cfDNSRecord{},
	}
	if cache.Zones == nil {
		cache.Zones = []cfZone{}
	}

	for _, z := range cache.Zones {
		u := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?per_page=500", url.PathEscape(z.ID))
		rawR, err := h.cfHTTPDo("GET", u, cfg, nil)
		if err != nil {
			continue
		}
		if ok, _ := parseCFError(rawR); !ok {
			continue
		}
		var rResp struct {
			Result []struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Name    string `json:"name"`
				Content string `json:"content"`
				TTL     int    `json:"ttl"`
				Proxied bool   `json:"proxied"`
			} `json:"result"`
		}
		_ = json.Unmarshal(rawR, &rResp)
		for _, rec := range rResp.Result {
			cache.Records = append(cache.Records, cfDNSRecord{
				ID:       rec.ID,
				ZoneID:   z.ID,
				ZoneName: z.Name,
				Type:     rec.Type,
				Name:     rec.Name,
				Content:  rec.Content,
				TTL:      rec.TTL,
				Proxied:  rec.Proxied,
			})
		}
	}
	h.saveCFCache(cache)
	return cache, nil
}

func (h *Handler) matchRecordsWithServers(records []cfDNSRecord) ([]cfDNSRecord, map[int64][]string) {
	srvs, _ := h.store.ListServers()
	type srvInfo struct {
		id   int64
		name string
		role string
	}
	ipMap := map[string]srvInfo{}
	for _, s := range srvs {
		info := srvInfo{id: s.ID, name: s.Name, role: s.Role}
		for _, cand := range []string{s.Host, s.IPv4, s.IPv6} {
			c := strings.ToLower(strings.TrimSpace(cand))
			if c != "" && c != "n/a" {
				ipMap[c] = info
			}
		}
	}

	srvDomains := map[int64][]string{}
	seen := map[string]bool{}
	out := make([]cfDNSRecord, len(records))
	for i, r := range records {
		out[i] = r
		if r.Type == "A" || r.Type == "AAAA" {
			ip := strings.ToLower(strings.TrimSpace(r.Content))
			if info, ok := ipMap[ip]; ok {
				out[i].ServerID = info.id
				out[i].ServerName = info.name
				out[i].ServerRole = info.role
				k := fmt.Sprintf("%d|%s", info.id, r.Name)
				if !seen[k] {
					seen[k] = true
					srvDomains[info.id] = append(srvDomains[info.id], r.Name)
				}
			}
		}
	}
	return out, srvDomains
}

func (h *Handler) handleCFStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadCFConfig()
	cache := h.loadCFCache()
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"config":         cfg,
		"synced_at":      cache.SyncedAt,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
	})
}

func (h *Handler) handleCFSave(w http.ResponseWriter, r *http.Request) {
	var req cfConfig
	if !readJSON(w, r, &req) {
		return
	}
	req.APIToken = strings.TrimSpace(req.APIToken)
	req.Email = strings.TrimSpace(req.Email)
	req.APIKey = strings.TrimSpace(req.APIKey)
	if req.AuthMode != "global" {
		req.AuthMode = "token"
	}
	h.saveCFConfig(req)

	if !req.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Cloudflare settings saved (disabled)."})
		return
	}

	cache, err := h.syncCloudflareAll(req)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"synced_at":      cache.SyncedAt,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
		"message":        fmt.Sprintf("Connected! Synced %d zones & %d DNS records.", len(cache.Zones), len(matched)),
	})
}

func (h *Handler) handleCFSync(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadCFConfig()
	cache, err := h.syncCloudflareAll(cfg)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"synced_at":      cache.SyncedAt,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
		"message":        fmt.Sprintf("Synced %d zones & %d DNS records.", len(cache.Zones), len(matched)),
	})
}

func (h *Handler) handleCFCreateRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ZoneID  string `json:"zone_id"`
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl"`
		Proxied bool   `json:"proxied"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.ZoneID = strings.TrimSpace(req.ZoneID)
	req.Type = strings.ToUpper(strings.TrimSpace(req.Type))
	req.Name = strings.TrimSpace(req.Name)
	req.Content = strings.TrimSpace(req.Content)
	if req.ZoneID == "" || req.Type == "" || req.Name == "" || req.Content == "" {
		fail(w, http.StatusBadRequest, "zone, type, name, and content are required")
		return
	}
	if req.TTL <= 0 {
		req.TTL = 1 // Auto
	}
	cfg := h.loadCFConfig()
	u := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", url.PathEscape(req.ZoneID))
	raw, err := h.cfHTTPDo("POST", u, cfg, map[string]any{
		"type":    req.Type,
		"name":    req.Name,
		"content": req.Content,
		"ttl":     req.TTL,
		"proxied": req.Proxied,
	})
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if ok, msg := parseCFError(raw); !ok {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	cache, _ := h.syncCloudflareAll(cfg)
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
		"message":        "DNS record created!",
	})
}

func (h *Handler) handleCFUpdateRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ZoneID   string `json:"zone_id"`
		RecordID string `json:"record_id"`
		Type     string `json:"type"`
		Name     string `json:"name"`
		Content  string `json:"content"`
		TTL      int    `json:"ttl"`
		Proxied  bool   `json:"proxied"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.ZoneID == "" || req.RecordID == "" {
		fail(w, http.StatusBadRequest, "zone_id and record_id are required")
		return
	}
	if req.TTL <= 0 {
		req.TTL = 1
	}
	cfg := h.loadCFConfig()
	u := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", url.PathEscape(req.ZoneID), url.PathEscape(req.RecordID))
	raw, err := h.cfHTTPDo("PUT", u, cfg, map[string]any{
		"type":    strings.ToUpper(strings.TrimSpace(req.Type)),
		"name":    strings.TrimSpace(req.Name),
		"content": strings.TrimSpace(req.Content),
		"ttl":     req.TTL,
		"proxied": req.Proxied,
	})
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if ok, msg := parseCFError(raw); !ok {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	cache, _ := h.syncCloudflareAll(cfg)
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
		"message":        "DNS record updated!",
	})
}

func (h *Handler) handleCFDeleteRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ZoneID   string `json:"zone_id"`
		RecordID string `json:"record_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.ZoneID == "" || req.RecordID == "" {
		fail(w, http.StatusBadRequest, "zone_id and record_id are required")
		return
	}
	cfg := h.loadCFConfig()
	u := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", url.PathEscape(req.ZoneID), url.PathEscape(req.RecordID))
	raw, err := h.cfHTTPDo("DELETE", u, cfg, nil)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if ok, msg := parseCFError(raw); !ok {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	cache, _ := h.syncCloudflareAll(cfg)
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
		"message":        "DNS record deleted.",
	})
}
