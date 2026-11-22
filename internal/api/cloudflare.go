package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/updater"
)

type cfConfig struct {
	// Cloudflare independent config
	Enabled         bool   `json:"enabled"`
	AuthMode        string `json:"auth_mode"` // "token" or "global"
	APIToken        string `json:"api_token"`
	Email           string `json:"email"`
	APIKey          string `json:"api_key"`
	AutoSync        bool   `json:"auto_sync"`
	SyncIntervalMin int    `json:"sync_interval_min"` // 1..1440 minutes

	// ArvanCloud independent config
	ArvanEnabled         bool   `json:"arvan_enabled"`
	ArvanAPIKey          string `json:"arvan_api_key"`
	ArvanAutoSync        bool   `json:"arvan_auto_sync"`
	ArvanSyncIntervalMin int    `json:"arvan_sync_interval_min"` // 1..1440 minutes
}

type cfZone struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Provider string `json:"provider"` // "cloudflare" | "arvan"
}

type cfDNSRecord struct {
	ID         string `json:"id"`
	ZoneID     string `json:"zone_id"`
	ZoneName   string `json:"zone_name"`
	Provider   string `json:"provider"` // "cloudflare" | "arvan"
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
	SyncedAt      int64         `json:"synced_at"`
	CFSyncedAt    int64         `json:"cf_synced_at"`
	ArvanSyncedAt int64         `json:"arvan_synced_at"`
	Zones         []cfZone      `json:"zones"`
	Records       []cfDNSRecord `json:"records"`
}

var dnsSyncMu sync.Mutex

func clampSyncMin(v int) int {
	if v < 1 {
		return 60
	}
	if v > 1440 {
		return 1440
	}
	return v
}

func (h *Handler) loadCFConfig() cfConfig {
	raw := h.store.GetSetting("cloudflare_cfg_json")
	cfg := cfConfig{
		AuthMode:             "token",
		SyncIntervalMin:      60,
		ArvanSyncIntervalMin: 60,
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.AuthMode == "" {
		cfg.AuthMode = "token"
	}
	cfg.SyncIntervalMin = clampSyncMin(cfg.SyncIntervalMin)
	cfg.ArvanSyncIntervalMin = clampSyncMin(cfg.ArvanSyncIntervalMin)
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
	for i := range c.Zones {
		if c.Zones[i].Provider == "" {
			c.Zones[i].Provider = "cloudflare"
		}
	}
	for i := range c.Records {
		if c.Records[i].Provider == "" {
			c.Records[i].Provider = "cloudflare"
		}
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

func (h *Handler) arvanHTTPDo(method, urlStr, apiKey string, payload any) ([]byte, int, error) {
	var rdr io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, urlStr, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	key := strings.TrimSpace(apiKey)
	if !strings.HasPrefix(strings.ToLower(key), "apikey ") {
		key = "Apikey " + key
	}
	req.Header.Set("Authorization", key)
	client := updater.MakeHTTPClient(15*time.Second, "")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	return raw, resp.StatusCode, err
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

func extractArvanValue(recType string, val any) string {
	switch v := val.(type) {
	case string:
		return v
	case map[string]any:
		for _, k := range []string{"host", "ip", "text", "value"} {
			if s, ok := v[k].(string); ok && s != "" {
				return s
			}
		}
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				for _, k := range []string{"ip", "host", "text", "value"} {
					if s, ok := m[k].(string); ok && s != "" {
						parts = append(parts, s)
						break
					}
				}
			} else if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func buildArvanPayload(recType, name, content string, ttl int, proxied bool) map[string]any {
	rt := strings.ToLower(strings.TrimSpace(recType))
	if ttl < 120 {
		ttl = 120
	}
	var val any
	switch rt {
	case "a", "aaaa":
		val = []map[string]any{{"ip": content}}
	case "cname":
		val = map[string]any{"host": content, "host_header": "source"}
	case "txt":
		val = map[string]any{"text": content}
	default:
		val = map[string]any{"value": content}
	}
	return map[string]any{
		"type":  rt,
		"name":  name,
		"value": val,
		"ttl":   ttl,
		"cloud": proxied,
	}
}

func (h *Handler) fetchCloudflareData(cfg cfConfig) ([]cfZone, []cfDNSRecord, error) {
	if (cfg.AuthMode == "global" && (cfg.Email == "" || cfg.APIKey == "")) || (cfg.AuthMode != "global" && cfg.APIToken == "") {
		return nil, nil, fmt.Errorf("Cloudflare credentials are incomplete")
	}
	rawZ, err := h.cfHTTPDo("GET", "https://api.cloudflare.com/client/v4/zones?per_page=50", cfg, nil)
	if err != nil {
		return nil, nil, err
	}
	if ok, msg := parseCFError(rawZ); !ok {
		return nil, nil, fmt.Errorf("Cloudflare: %s", msg)
	}
	var zResp struct {
		Result []cfZone `json:"result"`
	}
	_ = json.Unmarshal(rawZ, &zResp)

	var zones []cfZone
	var recs []cfDNSRecord
	for _, z := range zResp.Result {
		z.Provider = "cloudflare"
		zones = append(zones, z)
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
			recs = append(recs, cfDNSRecord{
				ID:       rec.ID,
				ZoneID:   z.ID,
				ZoneName: z.Name,
				Provider: "cloudflare",
				Type:     rec.Type,
				Name:     rec.Name,
				Content:  rec.Content,
				TTL:      rec.TTL,
				Proxied:  rec.Proxied,
			})
		}
	}
	return zones, recs, nil
}

func (h *Handler) fetchArvanData(cfg cfConfig) ([]cfZone, []cfDNSRecord, error) {
	if strings.TrimSpace(cfg.ArvanAPIKey) == "" {
		return nil, nil, fmt.Errorf("ArvanCloud API Key is empty")
	}
	rawD, code, err := h.arvanHTTPDo("GET", "https://napi.arvancloud.ir/cdn/4.0/domains?per_page=100", cfg.ArvanAPIKey, nil)
	if err != nil {
		return nil, nil, err
	}
	if code < 200 || code >= 300 {
		return nil, nil, fmt.Errorf("ArvanCloud: HTTP %d", code)
	}
	var dResp struct {
		Data []struct {
			ID     string `json:"id"`
			Domain string `json:"domain"`
			Status string `json:"status"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rawD, &dResp)

	var zones []cfZone
	var recs []cfDNSRecord
	for _, d := range dResp.Data {
		if d.Domain == "" {
			continue
		}
		zID := "arvan:" + d.Domain
		zones = append(zones, cfZone{
			ID:       zID,
			Name:     d.Domain,
			Status:   d.Status,
			Provider: "arvan",
		})
		u := fmt.Sprintf("https://napi.arvancloud.ir/cdn/4.0/domains/%s/dns-records?per_page=500", url.PathEscape(d.Domain))
		rawR, rCode, rErr := h.arvanHTTPDo("GET", u, cfg.ArvanAPIKey, nil)
		if rErr != nil || rCode < 200 || rCode >= 300 {
			continue
		}
		var rResp struct {
			Data []struct {
				ID    string `json:"id"`
				Type  string `json:"type"`
				Name  string `json:"name"`
				Value any    `json:"value"`
				TTL   int    `json:"ttl"`
				Cloud bool   `json:"cloud"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rawR, &rResp)
		for _, rec := range rResp.Data {
			fullName := rec.Name
			if fullName == "@" || fullName == "" {
				fullName = d.Domain
			} else if !strings.HasSuffix(strings.ToLower(fullName), "."+strings.ToLower(d.Domain)) && !strings.EqualFold(fullName, d.Domain) {
				fullName = fullName + "." + d.Domain
			}
			recs = append(recs, cfDNSRecord{
				ID:       rec.ID,
				ZoneID:   zID,
				ZoneName: d.Domain,
				Provider: "arvan",
				Type:     strings.ToUpper(rec.Type),
				Name:     fullName,
				Content:  extractArvanValue(rec.Type, rec.Value),
				TTL:      rec.TTL,
				Proxied:  rec.Cloud,
			})
		}
	}
	return zones, recs, nil
}

// syncDNSProvider syncs "cloudflare", "arvan", or "all" (""), preserving the other provider's cached data.
func (h *Handler) syncDNSProvider(cfg cfConfig, provider string) (cfCache, error) {
	dnsSyncMu.Lock()
	defer dnsSyncMu.Unlock()

	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = "all"
	}

	cur := h.loadCFCache()
	now := time.Now().Unix()

	keepZones := func(excludeProv string) []cfZone {
		var out []cfZone
		for _, z := range cur.Zones {
			if z.Provider != excludeProv {
				out = append(out, z)
			}
		}
		return out
	}
	keepRecs := func(excludeProv string) []cfDNSRecord {
		var out []cfDNSRecord
		for _, r := range cur.Records {
			if r.Provider != excludeProv {
				out = append(out, r)
			}
		}
		return out
	}

	switch provider {
	case "cloudflare":
		if !cfg.Enabled {
			cur.Zones = keepZones("cloudflare")
			cur.Records = keepRecs("cloudflare")
			h.saveCFCache(cur)
			return cur, nil
		}
		z, r, err := h.fetchCloudflareData(cfg)
		if err != nil {
			return cur, err
		}
		cur.Zones = append(keepZones("cloudflare"), z...)
		cur.Records = append(keepRecs("cloudflare"), r...)
		cur.CFSyncedAt = now
		cur.SyncedAt = now
		h.saveCFCache(cur)
		return cur, nil

	case "arvan":
		if !cfg.ArvanEnabled {
			cur.Zones = keepZones("arvan")
			cur.Records = keepRecs("arvan")
			h.saveCFCache(cur)
			return cur, nil
		}
		z, r, err := h.fetchArvanData(cfg)
		if err != nil {
			return cur, err
		}
		cur.Zones = append(keepZones("arvan"), z...)
		cur.Records = append(keepRecs("arvan"), r...)
		cur.ArvanSyncedAt = now
		cur.SyncedAt = now
		h.saveCFCache(cur)
		return cur, nil

	default:
		var errs []string
		didAny := false
		if cfg.Enabled {
			didAny = true
			if z, r, err := h.fetchCloudflareData(cfg); err == nil {
				cur.Zones = append(keepZones("cloudflare"), z...)
				cur.Records = append(keepRecs("cloudflare"), r...)
				cur.CFSyncedAt = now
				cur.SyncedAt = now
			} else {
				errs = append(errs, err.Error())
			}
		} else {
			cur.Zones = keepZones("cloudflare")
			cur.Records = keepRecs("cloudflare")
		}
		if cfg.ArvanEnabled {
			didAny = true
			if z, r, err := h.fetchArvanData(cfg); err == nil {
				cur.Zones = append(keepZones("arvan"), z...)
				cur.Records = append(keepRecs("arvan"), r...)
				cur.ArvanSyncedAt = now
				cur.SyncedAt = now
			} else {
				errs = append(errs, err.Error())
			}
		} else {
			cur.Zones = keepZones("arvan")
			cur.Records = keepRecs("arvan")
		}
		if !didAny {
			return cur, fmt.Errorf("enable Cloudflare or ArvanCloud API first")
		}
		h.saveCFCache(cur)
		if len(cur.Zones) == 0 && len(errs) > 0 {
			return cur, fmt.Errorf("%s", strings.Join(errs, " | "))
		}
		return cur, nil
	}
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
			for _, part := range strings.Split(r.Content, ",") {
				ip := strings.ToLower(strings.TrimSpace(part))
				if info, ok := ipMap[ip]; ok {
					out[i].ServerID = info.id
					out[i].ServerName = info.name
					out[i].ServerRole = info.role
					k := fmt.Sprintf("%d|%s", info.id, r.Name)
					if !seen[k] {
						seen[k] = true
						srvDomains[info.id] = append(srvDomains[info.id], r.Name)
					}
					break
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
		"ok":              true,
		"config":          cfg,
		"synced_at":       cache.SyncedAt,
		"cf_synced_at":    cache.CFSyncedAt,
		"arvan_synced_at": cache.ArvanSyncedAt,
		"zones":           cache.Zones,
		"records":         matched,
		"server_domains":  srvDomains,
	})
}

func (h *Handler) handleCFSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider             string `json:"provider"` // "cloudflare" | "arvan" | "all"
		Enabled              bool   `json:"enabled"`
		AuthMode             string `json:"auth_mode"`
		APIToken             string `json:"api_token"`
		Email                string `json:"email"`
		APIKey               string `json:"api_key"`
		AutoSync             bool   `json:"auto_sync"`
		SyncIntervalMin      int    `json:"sync_interval_min"`
		ArvanEnabled         bool   `json:"arvan_enabled"`
		ArvanAPIKey          string `json:"arvan_api_key"`
		ArvanAutoSync        bool   `json:"arvan_auto_sync"`
		ArvanSyncIntervalMin int    `json:"arvan_sync_interval_min"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	prov := strings.ToLower(strings.TrimSpace(req.Provider))
	if prov == "" {
		prov = "all"
	}

	cfg := h.loadCFConfig()
	if prov == "cloudflare" || prov == "all" {
		cfg.Enabled = req.Enabled
		cfg.AuthMode = req.AuthMode
		if cfg.AuthMode != "global" {
			cfg.AuthMode = "token"
		}
		cfg.APIToken = strings.TrimSpace(req.APIToken)
		cfg.Email = strings.TrimSpace(req.Email)
		cfg.APIKey = strings.TrimSpace(req.APIKey)
		cfg.AutoSync = req.AutoSync
		cfg.SyncIntervalMin = clampSyncMin(req.SyncIntervalMin)
	}
	if prov == "arvan" || prov == "all" {
		cfg.ArvanEnabled = req.ArvanEnabled
		cfg.ArvanAPIKey = strings.TrimSpace(req.ArvanAPIKey)
		cfg.ArvanAutoSync = req.ArvanAutoSync
		cfg.ArvanSyncIntervalMin = clampSyncMin(req.ArvanSyncIntervalMin)
	}
	h.saveCFConfig(cfg)

	cache, err := h.syncDNSProvider(cfg, prov)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	msg := fmt.Sprintf("Saved & synced! (%d domains, %d records)", len(cache.Zones), len(matched))
	if prov == "cloudflare" {
		msg = "Cloudflare settings saved & synced!"
		if !cfg.Enabled {
			msg = "Cloudflare disabled."
		}
	} else if prov == "arvan" {
		msg = "ArvanCloud settings saved & synced!"
		if !cfg.ArvanEnabled {
			msg = "ArvanCloud disabled."
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"synced_at":       cache.SyncedAt,
		"cf_synced_at":    cache.CFSyncedAt,
		"arvan_synced_at": cache.ArvanSyncedAt,
		"zones":           cache.Zones,
		"records":         matched,
		"server_domains":  srvDomains,
		"message":         msg,
	})
}

func (h *Handler) handleCFSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	prov := strings.ToLower(strings.TrimSpace(req.Provider))
	if prov == "" {
		prov = "all"
	}
	cfg := h.loadCFConfig()
	cache, err := h.syncDNSProvider(cfg, prov)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	lbl := "All DNS providers"
	if prov == "cloudflare" {
		lbl = "Cloudflare"
	} else if prov == "arvan" {
		lbl = "ArvanCloud"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"synced_at":       cache.SyncedAt,
		"cf_synced_at":    cache.CFSyncedAt,
		"arvan_synced_at": cache.ArvanSyncedAt,
		"zones":           cache.Zones,
		"records":         matched,
		"server_domains":  srvDomains,
		"message":         fmt.Sprintf("%s synced! (%d domains, %d records)", lbl, len(cache.Zones), len(matched)),
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
		fail(w, http.StatusBadRequest, "domain, type, name, and content are required")
		return
	}
	cfg := h.loadCFConfig()
	prov := "cloudflare"

	if strings.HasPrefix(req.ZoneID, "arvan:") {
		prov = "arvan"
		domain := strings.TrimPrefix(req.ZoneID, "arvan:")
		sub := strings.TrimSuffix(strings.TrimSuffix(req.Name, domain), ".")
		if sub == "" {
			sub = "@"
		}
		u := fmt.Sprintf("https://napi.arvancloud.ir/cdn/4.0/domains/%s/dns-records", url.PathEscape(domain))
		raw, code, err := h.arvanHTTPDo("POST", u, cfg.ArvanAPIKey, buildArvanPayload(req.Type, sub, req.Content, req.TTL, req.Proxied))
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if code < 200 || code >= 300 {
			fail(w, http.StatusBadRequest, fmt.Sprintf("ArvanCloud HTTP %d: %s", code, string(raw)))
			return
		}
	} else {
		if req.TTL <= 0 {
			req.TTL = 1
		}
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
	}

	cache, _ := h.syncDNSProvider(cfg, prov)
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
	cfg := h.loadCFConfig()
	prov := "cloudflare"

	if strings.HasPrefix(req.ZoneID, "arvan:") {
		prov = "arvan"
		domain := strings.TrimPrefix(req.ZoneID, "arvan:")
		sub := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(req.Name), domain), ".")
		if sub == "" {
			sub = "@"
		}
		u := fmt.Sprintf("https://napi.arvancloud.ir/cdn/4.0/domains/%s/dns-records/%s", url.PathEscape(domain), url.PathEscape(req.RecordID))
		raw, code, err := h.arvanHTTPDo("PUT", u, cfg.ArvanAPIKey, buildArvanPayload(req.Type, sub, strings.TrimSpace(req.Content), req.TTL, req.Proxied))
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if code < 200 || code >= 300 {
			fail(w, http.StatusBadRequest, fmt.Sprintf("ArvanCloud HTTP %d: %s", code, string(raw)))
			return
		}
	} else {
		if req.TTL <= 0 {
			req.TTL = 1
		}
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
	}

	cache, _ := h.syncDNSProvider(cfg, prov)
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
	prov := "cloudflare"

	if strings.HasPrefix(req.ZoneID, "arvan:") {
		prov = "arvan"
		domain := strings.TrimPrefix(req.ZoneID, "arvan:")
		u := fmt.Sprintf("https://napi.arvancloud.ir/cdn/4.0/domains/%s/dns-records/%s", url.PathEscape(domain), url.PathEscape(req.RecordID))
		raw, code, err := h.arvanHTTPDo("DELETE", u, cfg.ArvanAPIKey, nil)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		if code < 200 || code >= 300 {
			fail(w, http.StatusBadRequest, fmt.Sprintf("ArvanCloud HTTP %d: %s", code, string(raw)))
			return
		}
	} else {
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
	}

	cache, _ := h.syncDNSProvider(cfg, prov)
	matched, srvDomains := h.matchRecordsWithServers(cache.Records)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"zones":          cache.Zones,
		"records":        matched,
		"server_domains": srvDomains,
		"message":        "DNS record deleted.",
	})
}

func (h *Handler) StartCloudflareAutoSync() {
	go func() {
		for {
			time.Sleep(30 * time.Second)
			cfg := h.loadCFConfig()
			cache := h.loadCFCache()

			if cfg.Enabled && cfg.AutoSync {
				iv := clampSyncMin(cfg.SyncIntervalMin)
				if cache.CFSyncedAt == 0 || time.Since(time.Unix(cache.CFSyncedAt, 0)) >= time.Duration(iv)*time.Minute {
					if _, err := h.syncDNSProvider(cfg, "cloudflare"); err != nil {
						log.Printf("[cloudflare] auto-sync warning: %v", err)
					}
				}
			}

			if cfg.ArvanEnabled && cfg.ArvanAutoSync {
				iv := clampSyncMin(cfg.ArvanSyncIntervalMin)
				if cache.ArvanSyncedAt == 0 || time.Since(time.Unix(cache.ArvanSyncedAt, 0)) >= time.Duration(iv)*time.Minute {
					if _, err := h.syncDNSProvider(cfg, "arvan"); err != nil {
						log.Printf("[arvancloud] auto-sync warning: %v", err)
					}
				}
			}
		}
	}()
}
