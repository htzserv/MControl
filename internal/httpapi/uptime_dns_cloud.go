package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/storage"
)

// StartUptimeWorker runs isolated background health checks for every active tenant's monitors.
func (s *Server) StartUptimeWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(45 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runAllTenantMonitors(ctx)
			}
		}
	}()
}

func (s *Server) runAllTenantMonitors(ctx context.Context) {
	users, err := s.engine.ListUsers(ctx)
	if err != nil {
		return
	}
	for _, u := range users {
		if !u.Active {
			continue
		}
		mons, err := s.engine.ListMonitors(ctx, u.ID)
		if err != nil || len(mons) == 0 {
			continue
		}
		for _, m := range mons {
			up, lat, errMsg := checkMonitorTarget(m.Type, m.Target)
			prevStatus := m.Status
			_ = s.engine.RecordHeartbeat(ctx, u.ID, m.ID, up, lat, errMsg)
			newStatus := "down"
			if up {
				newStatus = "up"
			}
			if prevStatus != "unknown" && prevStatus != newStatus {
				if tg, err := s.engine.GetTelegramConfig(ctx, u.ID); err == nil && tg.Enabled && tg.BotToken != "" && tg.ChatID != "" {
					icon := "🔴 DOWN"
					if up {
						icon = "🟢 RECOVERED"
					}
					msg := fmt.Sprintf("%s\nMonitor: %s\nTarget: %s\nLatency: %d ms\n%s", icon, m.Name, m.Target, lat, errMsg)
					_ = sendTelegram(tg.BotToken, tg.ChatID, msg)
				}
			}
		}
	}
}

func checkMonitorTarget(kind, target string) (bool, int, string) {
	start := time.Now()
	if kind == "tcp" {
		conn, err := net.DialTimeout("tcp", target, 6*time.Second)
		lat := int(time.Since(start).Milliseconds())
		if err != nil {
			return false, lat, err.Error()
		}
		_ = conn.Close()
		return true, lat, ""
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	resp, err := client.Get(target)
	lat := int(time.Since(start).Milliseconds())
	if err != nil {
		return false, lat, err.Error()
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, lat, ""
	}
	return false, lat, fmt.Sprintf("HTTP %d", resp.StatusCode)
}

func sendTelegram(botToken, chatID, text string) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", url.PathEscape(botToken))
	payload, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": text})
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// ── Uptime & Public Status Handlers ──────────────────────────────────────────

func (s *Server) handleTenantListMonitors(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	list, err := s.engine.ListMonitors(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "monitors": list})
}

func (s *Server) handleTenantAddMonitor(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	u, err := s.engine.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusUnauthorized, "user not found")
		return
	}
	cur, _ := s.engine.ListMonitors(r.Context(), claims.UserID)
	if len(cur) >= u.MaxMonitors {
		fail(w, http.StatusForbidden, "monitor quota reached for your plan")
		return
	}
	var req storage.Monitor
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Target) == "" {
		fail(w, http.StatusBadRequest, "name and target are required")
		return
	}
	id, err := s.engine.AddMonitor(r.Context(), claims.UserID, &req)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	up, lat, errMsg := checkMonitorTarget(req.Type, req.Target)
	_ = s.engine.RecordHeartbeat(r.Context(), claims.UserID, id, up, lat, errMsg)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleTenantCheckMonitor(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	list, _ := s.engine.ListMonitors(r.Context(), claims.UserID)
	for _, m := range list {
		if m.ID == id {
			up, lat, errMsg := checkMonitorTarget(m.Type, m.Target)
			_ = s.engine.RecordHeartbeat(r.Context(), claims.UserID, id, up, lat, errMsg)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "up": up, "latency_ms": lat, "error": errMsg})
			return
		}
	}
	fail(w, http.StatusNotFound, "monitor not found")
}

func (s *Server) handleTenantDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.engine.DeleteMonitor(r.Context(), claims.UserID, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTenantGetTelegram(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	cfg, _ := s.engine.GetTelegramConfig(r.Context(), claims.UserID)
	if cfg.BotToken != "" {
		cfg.BotToken = "••••••••"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "telegram": cfg})
}

func (s *Server) handleTenantSaveTelegram(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req storage.TelegramConfig
	if !readJSON(w, r, &req) {
		return
	}
	if req.BotToken == "••••••••" || strings.TrimSpace(req.BotToken) == "" {
		cur, _ := s.engine.GetTelegramConfig(r.Context(), claims.UserID)
		req.BotToken = cur.BotToken
	}
	if err := s.engine.SaveTelegramConfig(r.Context(), claims.UserID, req); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTenantTestTelegram(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	cfg, err := s.engine.GetTelegramConfig(r.Context(), claims.UserID)
	if err != nil || cfg.BotToken == "" || cfg.ChatID == "" {
		fail(w, http.StatusBadRequest, "save your Bot Token and Chat ID first")
		return
	}
	if err := sendTelegram(cfg.BotToken, cfg.ChatID, "✅ MControl v2 Isolated Uptime Alert Test — Connected!"); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Test alert sent to Telegram!"})
}

func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PathValue("username"))
	u, err := s.engine.GetUserByUsername(r.Context(), username)
	if err != nil || !u.Active {
		fail(w, http.StatusNotFound, "status page not found")
		return
	}
	all, _ := s.engine.ListMonitors(r.Context(), u.ID)
	pub := []storage.Monitor{}
	for _, m := range all {
		if m.Public {
			pub = append(pub, m)
		}
	}
	brand, _ := s.engine.GetBranding(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"username": u.Username,
		"branding": brand,
		"monitors": pub,
	})
}

// ── Per-Tenant DNS & Failover (Cloudflare / ArvanCloud) ──────────────────────

func (s *Server) handleTenantListDNS(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	list, err := s.engine.ListDNSRules(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rules": list})
}

func (s *Server) handleTenantAddDNS(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req storage.DNSRule
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Domain) == "" || strings.TrimSpace(req.RecordName) == "" || strings.TrimSpace(req.PrimaryIP) == "" {
		fail(w, http.StatusBadRequest, "domain, record_name and primary_ip are required")
		return
	}
	id, err := s.engine.AddDNSRule(r.Context(), claims.UserID, &req)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleTenantSwitchDNS(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Target string `json:"target"` // primary | backup
	}
	if !readJSON(w, r, &req) {
		return
	}
	rule, err := s.engine.GetDNSRuleWithToken(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "DNS rule not found")
		return
	}
	newIP := rule.PrimaryIP
	newStatus := "primary"
	if req.Target == "backup" && rule.BackupIP != "" {
		newIP = rule.BackupIP
		newStatus = "backup"
	}

	// Push update to Cloudflare or ArvanCloud if API credentials are configured
	apiMsg := "Updated locally (no API token/record_id configured)"
	if rule.APIToken != "" && rule.RecordID != "" {
		client := &http.Client{Timeout: 10 * time.Second}
		if rule.Provider == "arvancloud" {
			u := fmt.Sprintf("https://napi.arvancloud.ir/cdn/4.0/domains/%s/dns-records/%s", url.PathEscape(rule.Domain), url.PathEscape(rule.RecordID))
			body, _ := json.Marshal(map[string]any{"type": "a", "name": rule.RecordName, "value": []map[string]any{{"ip": newIP}}})
			httpReq, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(body))
			httpReq.Header.Set("Authorization", "Apikey "+rule.APIToken)
			httpReq.Header.Set("Content-Type", "application/json")
			if resp, err := client.Do(httpReq); err == nil {
				_ = resp.Body.Close()
				apiMsg = fmt.Sprintf("ArvanCloud synced (HTTP %d)", resp.StatusCode)
			} else {
				apiMsg = "ArvanCloud error: " + err.Error()
			}
		} else if rule.ZoneID != "" {
			u := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", url.PathEscape(rule.ZoneID), url.PathEscape(rule.RecordID))
			body, _ := json.Marshal(map[string]any{"type": "A", "name": rule.RecordName, "content": newIP, "ttl": 60})
			httpReq, _ := http.NewRequest(http.MethodPatch, u, bytes.NewReader(body))
			httpReq.Header.Set("Authorization", "Bearer "+rule.APIToken)
			httpReq.Header.Set("Content-Type", "application/json")
			if resp, err := client.Do(httpReq); err == nil {
				_ = resp.Body.Close()
				apiMsg = fmt.Sprintf("Cloudflare synced (HTTP %d)", resp.StatusCode)
			} else {
				apiMsg = "Cloudflare error: " + err.Error()
			}
		}
	}

	ts := time.Now().Format("2006-01-02 15:04")
	_ = s.engine.UpdateDNSRuleActive(r.Context(), claims.UserID, id, newIP, newStatus, ts)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "active_ip": newIP, "message": apiMsg})
}

func (s *Server) handleTenantDeleteDNS(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.engine.DeleteDNSRule(r.Context(), claims.UserID, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── Per-Tenant Cloud / Datacenters (Hetzner, DigitalOcean, Vultr) ────────────

func (s *Server) handleTenantListCloud(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	list, err := s.engine.ListCloudAccounts(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "accounts": list})
}

func (s *Server) handleTenantAddCloud(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req storage.CloudAccount
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Label) == "" || strings.TrimSpace(req.APIToken) == "" {
		fail(w, http.StatusBadRequest, "label and api_token are required")
		return
	}
	id, err := s.engine.AddCloudAccount(r.Context(), claims.UserID, &req)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleTenantCloudInstances(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	acc, err := s.engine.GetCloudAccountWithToken(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "cloud account not found")
		return
	}
	type inst struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		IPv4     string `json:"ipv4"`
		Region   string `json:"region"`
		Status   string `json:"status"`
		Provider string `json:"provider"`
	}
	out := []inst{}
	client := &http.Client{Timeout: 12 * time.Second}

	switch acc.Provider {
	case "hetzner":
		req, _ := http.NewRequest(http.MethodGet, "https://api.hetzner.cloud/v1/servers", nil)
		req.Header.Set("Authorization", "Bearer "+acc.APIToken)
		resp, err := client.Do(req)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		var data struct {
			Servers []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				Status     string `json:"status"`
				PublicNet  struct{ IPv4 struct{ IP string `json:"ip"` } `json:"ipv4"` } `json:"public_net"`
				Datacenter struct{ Name string `json:"name"` } `json:"datacenter"`
			} `json:"servers"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&data)
		for _, sv := range data.Servers {
			out = append(out, inst{ID: fmt.Sprintf("%d", sv.ID), Name: sv.Name, IPv4: sv.PublicNet.IPv4.IP, Region: sv.Datacenter.Name, Status: sv.Status, Provider: "hetzner"})
		}
	case "digitalocean":
		req, _ := http.NewRequest(http.MethodGet, "https://api.digitalocean.com/v2/droplets", nil)
		req.Header.Set("Authorization", "Bearer "+acc.APIToken)
		resp, err := client.Do(req)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		var data struct {
			Droplets []struct {
				ID       int    `json:"id"`
				Name     string `json:"name"`
				Status   string `json:"status"`
				Region   struct{ Slug string `json:"slug"` } `json:"region"`
				Networks struct {
					V4 []struct {
						IP   string `json:"ip_address"`
						Type string `json:"type"`
					} `json:"v4"`
				} `json:"networks"`
			} `json:"droplets"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&data)
		for _, d := range data.Droplets {
			ip := ""
			for _, n := range d.Networks.V4 {
				if n.Type == "public" {
					ip = n.IP
					break
				}
			}
			out = append(out, inst{ID: fmt.Sprintf("%d", d.ID), Name: d.Name, IPv4: ip, Region: d.Region.Slug, Status: d.Status, Provider: "digitalocean"})
		}
	case "vultr":
		req, _ := http.NewRequest(http.MethodGet, "https://api.vultr.com/v2/instances", nil)
		req.Header.Set("Authorization", "Bearer "+acc.APIToken)
		resp, err := client.Do(req)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		var data struct {
			Instances []struct {
				ID     string `json:"id"`
				Label  string `json:"label"`
				MainIP string `json:"main_ip"`
				Region string `json:"region"`
				Status string `json:"status"`
			} `json:"instances"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&data)
		for _, v := range data.Instances {
			out = append(out, inst{ID: v.ID, Name: v.Label, IPv4: v.MainIP, Region: v.Region, Status: v.Status, Provider: "vultr"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "instances": out})
}

func (s *Server) handleTenantDeleteCloud(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.engine.DeleteCloudAccount(r.Context(), claims.UserID, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
