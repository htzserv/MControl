package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/updater"
)

type UptimeNotifyConfig struct {
	Enabled             bool   `json:"enabled"`
	BotToken            string `json:"bot_token"`
	ChatID              string `json:"chat_id"`
	TopicID             int64  `json:"topic_id"`
	NotifySSL           bool   `json:"notify_ssl"`
	SSLWarnDays         int    `json:"ssl_warn_days"`
	NotifyLatency       bool   `json:"notify_latency"`
	DailySummary        bool   `json:"daily_summary"`
	PublicStatusEnabled bool   `json:"public_status_enabled"`
	PublicStatusTitle   string `json:"public_status_title"`
	PublicStatusTheme   string `json:"public_status_theme"`
}

var (
	pingMsRe = regexp.MustCompile(`time[=<]\s*([0-9.]+)\s*ms`)
)

func (h *Handler) loadUptimeCfg() UptimeNotifyConfig {
	raw := h.store.GetSetting("uptime_notify_cfg_enc")
	cfg := UptimeNotifyConfig{
		NotifySSL:           true,
		SSLWarnDays:         7,
		NotifyLatency:       true,
		PublicStatusEnabled: false,
		PublicStatusTitle:   "MControl Service Status",
	}
	if raw == "" {
		return cfg
	}
	plain, err := h.store.Decrypt(raw)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal([]byte(plain), &cfg)
	if cfg.SSLWarnDays <= 0 {
		cfg.SSLWarnDays = 7
	}
	if strings.TrimSpace(cfg.PublicStatusTitle) == "" {
		cfg.PublicStatusTitle = "MControl Service Status"
	}
	if strings.TrimSpace(cfg.PublicStatusTheme) == "" {
		cfg.PublicStatusTheme = "mdesign-dark"
	}
	return cfg
}

func (h *Handler) saveUptimeCfg(cfg UptimeNotifyConfig) error {
	b, _ := json.Marshal(cfg)
	enc, err := h.store.Encrypt(string(b))
	if err != nil {
		return err
	}
	return h.store.SetSetting("uptime_notify_cfg_enc", enc)
}

func (h *Handler) sendUptimeTelegram(msg string) error {
	cfg := h.loadUptimeCfg()
	if !cfg.Enabled || strings.TrimSpace(cfg.BotToken) == "" || strings.TrimSpace(cfg.ChatID) == "" {
		return fmt.Errorf("dedicated uptime Telegram bot is not configured or disabled")
	}
	return h.sendUptimeTelegramWithCfg(cfg, msg)
}

func (h *Handler) sendUptimeTelegramWithCfg(cfg UptimeNotifyConfig, msg string) error {
	tok := strings.TrimSpace(cfg.BotToken)
	chatRaw := strings.TrimSpace(cfg.ChatID)
	if tok == "" || chatRaw == "" {
		return fmt.Errorf("bot token and chat ID are required")
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", tok)
	payload := map[string]any{
		"chat_id":                  chatRaw,
		"text":                     msg,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if cfg.TopicID > 0 {
		payload["message_thread_id"] = cfg.TopicID
	}
	bodyBytes, _ := json.Marshal(payload)

	trySend := func(proxy string) error {
		client := updater.MakeHTTPClient(12*time.Second, proxy)
		req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("Telegram HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
		}
		return nil
	}

	if isOutboundActive() {
		if err := trySend(fmt.Sprintf("127.0.0.1:%d", outboundSocksPort)); err == nil {
			return nil
		}
	}
	return trySend("")
}

func formatDurationHuman(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %dm", sec/3600, (sec%3600)/60)
}

type probeOutcome struct {
	Up        bool
	LatencyMs int
	SSLDays   int
	Msg       string
}

func (h *Handler) runMonitorProbe(m *db.Monitor) probeOutcome {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout < 2*time.Second {
		timeout = 10 * time.Second
	}
	start := time.Now()
	out := probeOutcome{SSLDays: -1}

	// Remote node probe (Iran / Foreign server)
	if strings.HasPrefix(m.ProbeFrom, "server:") {
		sid, _ := strconv.ParseInt(strings.TrimPrefix(m.ProbeFrom, "server:"), 10, 64)
		srv, err := h.store.GetServerWithSecret(sid)
		if err != nil {
			out.Msg = "probe node not found"
			return out
		}
		var cmd string
		switch m.Type {
		case "ping":
			flag := "-4"
			if strings.Contains(m.Target, ":") {
				flag = "-6"
			}
			cmd = fmt.Sprintf(`ping %s -c 1 -W %d %s 2>&1`, flag, m.TimeoutSec, sshclient.ShQuote(m.Target))
		case "tcp":
			host, port, err := net.SplitHostPort(m.Target)
			if err != nil {
				out.Msg = "invalid host:port"
				return out
			}
			cmd = fmt.Sprintf(`timeout %d bash -c 'exec 3<>/dev/tcp/%s/%s' 2>&1 && echo "TCP_OK"`, m.TimeoutSec, host, port)
		case "dns":
			cmd = fmt.Sprintf(`getent hosts %s 2>&1 | head -n 3`, sshclient.ShQuote(m.Target))
		default: // http
			cmd = fmt.Sprintf(`curl -skL -o /dev/null -w "HTTP_CODE=%%{http_code} TIME_MS=%%{time_total}" --max-time %d %s 2>&1`, m.TimeoutSec, sshclient.ShQuote(m.Target))
		}
		viaTag := srv.Name + " [SSH]"
		if srv.AuthMode == "peer" {
			viaTag = srv.Name + " [Agent]"
		}
		res := h.runOnServer(srv, cmd, timeout+6*time.Second)
		elapsed := int(time.Since(start).Milliseconds())
		out.LatencyMs = elapsed
		if res.Err != nil && strings.TrimSpace(res.Output) == "" {
			out.Msg = fmt.Sprintf("Probe node %s unreachable", viaTag)
			return out
		}
		switch m.Type {
		case "ping":
			if res.Err == nil {
				out.Up = true
				if mm := pingMsRe.FindStringSubmatch(res.Output); len(mm) == 2 {
					if v, err := strconv.ParseFloat(mm[1], 64); err == nil {
						out.LatencyMs = int(v + 0.5)
					}
				}
				out.Msg = fmt.Sprintf("OK via %s (%dms)", viaTag, out.LatencyMs)
			} else {
				out.Msg = fmt.Sprintf("Ping failed via %s", viaTag)
			}
		case "tcp":
			if strings.Contains(res.Output, "TCP_OK") {
				out.Up = true
				out.Msg = fmt.Sprintf("TCP open via %s", viaTag)
			} else {
				out.Msg = fmt.Sprintf("TCP unreachable via %s", viaTag)
			}
		case "dns":
			if res.Err == nil && strings.TrimSpace(res.Output) != "" {
				if m.Expected != "" && !strings.Contains(res.Output, m.Expected) {
					out.Msg = "DNS mismatch: " + strings.TrimSpace(res.Output)
				} else {
					out.Up = true
					out.Msg = "DNS resolved via " + srv.Name
				}
			} else {
				out.Msg = "DNS failed via " + srv.Name
			}
		default:
			if strings.Contains(res.Output, "HTTP_CODE=") {
				parts := strings.Fields(res.Output)
				code := 0
				for _, p := range parts {
					if strings.HasPrefix(p, "HTTP_CODE=") {
						code, _ = strconv.Atoi(strings.TrimPrefix(p, "HTTP_CODE="))
					} else if strings.HasPrefix(p, "TIME_MS=") {
						if sec, err := strconv.ParseFloat(strings.TrimPrefix(p, "TIME_MS="), 64); err == nil {
							out.LatencyMs = int(sec * 1000)
						}
					}
				}
				if code >= 200 && code < 400 {
					out.Up = true
					out.Msg = fmt.Sprintf("HTTP %d via %s", code, srv.Name)
				} else {
					out.Msg = fmt.Sprintf("HTTP %d via %s", code, srv.Name)
				}
			} else {
				out.Msg = fmt.Sprintf("HTTP error via %s", srv.Name)
			}
		}
		return out
	}

	// Local Master or Master Outbound probe
	proxy := ""
	if m.ProbeFrom == "outbound" && isOutboundActive() {
		proxy = fmt.Sprintf("127.0.0.1:%d", outboundSocksPort)
	}

	switch m.Type {
	case "tcp":
		conn, err := net.DialTimeout("tcp", m.Target, timeout)
		out.LatencyMs = int(time.Since(start).Milliseconds())
		if err != nil {
			out.Msg = err.Error()
			return out
		}
		_ = conn.Close()
		out.Up = true
		out.Msg = fmt.Sprintf("TCP OK (%dms)", out.LatencyMs)
		return out

	case "ping":
		flag := "-4"
		if strings.Contains(m.Target, ":") {
			flag = "-6"
		}
		cmd := exec.Command("ping", flag, "-c", "1", "-W", strconv.Itoa(m.TimeoutSec), m.Target)
		raw, err := cmd.CombinedOutput()
		out.LatencyMs = int(time.Since(start).Milliseconds())
		if err != nil {
			out.Msg = "Ping timeout / unreachable"
			return out
		}
		if mm := pingMsRe.FindStringSubmatch(string(raw)); len(mm) == 2 {
			if v, e := strconv.ParseFloat(mm[1], 64); e == nil {
				out.LatencyMs = int(v + 0.5)
			}
		}
		out.Up = true
		out.Msg = fmt.Sprintf("ICMP OK (%dms)", out.LatencyMs)
		return out

	case "dns":
		addrs, err := net.LookupHost(m.Target)
		out.LatencyMs = int(time.Since(start).Milliseconds())
		if err != nil || len(addrs) == 0 {
			out.Msg = "DNS lookup failed"
			return out
		}
		joined := strings.Join(addrs, ", ")
		if m.Expected != "" && !strings.Contains(joined, m.Expected) {
			out.Msg = "DNS mismatch: " + joined
			return out
		}
		out.Up = true
		out.Msg = "Resolved: " + addrs[0]
		return out

	default: // http / https
		targetURL := m.Target
		if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
			targetURL = "https://" + targetURL
		}
		client := updater.MakeHTTPClient(timeout, proxy)
		if tr, ok := client.Transport.(*http.Transport); ok && tr != nil {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		} else {
			client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		}
		req, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			out.Msg = err.Error()
			return out
		}
		req.Header.Set("User-Agent", "MControl-Monitor/1.3.0")
		resp, err := client.Do(req)
		out.LatencyMs = int(time.Since(start).Milliseconds())
		if err != nil {
			out.Msg = err.Error()
			return out
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32768))
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			days := int(time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24)
			out.SSLDays = days
		}
		okCode := resp.StatusCode >= 200 && resp.StatusCode < 400
		if m.Expected != "" {
			if codeExp, err := strconv.Atoi(strings.TrimSpace(m.Expected)); err == nil {
				okCode = resp.StatusCode == codeExp
			} else if !strings.Contains(string(body), m.Expected) {
				out.Msg = fmt.Sprintf("HTTP %d (keyword %q missing)", resp.StatusCode, m.Expected)
				return out
			}
		}
		if !okCode {
			out.Msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
			return out
		}
		out.Up = true
		out.Msg = fmt.Sprintf("HTTP %d (%dms)", resp.StatusCode, out.LatencyMs)
		return out
	}
}

func (h *Handler) executeMonitorCheck(m *db.Monitor) {
	res := h.runMonitorProbe(m)
	now := time.Now().Unix()
	prevStatus := m.Status
	failStreak := m.FailStreak
	downSince := m.DownSince
	newStatus := prevStatus
	beatStatus := "up"

	if res.Up {
		failStreak = 0
		newStatus = "up"
		if m.LatencyWarnMs > 0 && res.LatencyMs >= m.LatencyWarnMs {
			beatStatus = "warn"
		}
	} else {
		failStreak++
		beatStatus = "down"
		if failStreak >= m.Retries {
			newStatus = "down"
			if downSince == 0 {
				downSince = now
			}
		} else if prevStatus == "pending" {
			newStatus = "pending"
		}
	}

	lastErr := ""
	if !res.Up {
		lastErr = res.Msg
	}

	_ = h.store.AddMonitorBeat(m.ID, beatStatus, res.LatencyMs, res.Msg)
	_ = h.store.UpdateMonitorState(m.ID, newStatus, failStreak, res.LatencyMs, res.SSLDays, lastErr, now, func() int64 {
		if newStatus == "up" {
			return 0
		}
		return downSince
	}())

	cfg := h.loadUptimeCfg()
	probeLbl := m.ProbeFrom
	if probeLbl == "" {
		probeLbl = "master"
	}

	// Transition: UP/pending -> DOWN
	if newStatus == "down" && prevStatus != "down" {
		_ = h.store.AddIncident(m.ID, m.Name, "down", res.Msg, 0)
		msg := fmt.Sprintf("🔴 <b>MONITOR DOWN — %s</b>\n• Type: <code>%s</code> (%s)\n• Target: <code>%s</code>\n• Probe: <code>%s</code>\n• Reason: <code>%s</code>\n• Failed Checks: %d",
			m.Name, strings.ToUpper(m.Type), m.PublicName, m.Target, probeLbl, res.Msg, failStreak)
		_ = h.sendUptimeTelegram(msg)

		// Optional Auto-Heal: restart linked tunnel
		if m.AutoHeal == "restart_tunnel" && m.LinkedTunnelID > 0 {
			go h.autoHealLinkedTunnel(m.ID, m.Name, m.LinkedTunnelID)
		}
	}

	// Transition: DOWN -> UP (Recovered!)
	if newStatus == "up" && prevStatus == "down" {
		dur := int64(0)
		if downSince > 0 && now > downSince {
			dur = now - downSince
		}
		_ = h.store.AddIncident(m.ID, m.Name, "up", fmt.Sprintf("Recovered after %s (%dms)", formatDurationHuman(dur), res.LatencyMs), dur)
		msg := fmt.Sprintf("🟢 <b>MONITOR RECOVERED — %s</b>\n• Target: <code>%s</code>\n• Latency: <b>%d ms</b>\n• Downtime Duration: <b>%s</b>",
			m.Name, m.Target, res.LatencyMs, formatDurationHuman(dur))
		_ = h.sendUptimeTelegram(msg)
	}

	// SSL Expiry Warning (once per 24h)
	if res.Up && cfg.NotifySSL && res.SSLDays >= 0 && res.SSLDays <= cfg.SSLWarnDays {
		h.monMu.Lock()
		if h.monSSLAlerted == nil { h.monSSLAlerted = map[int64]time.Time{} }
		last := h.monSSLAlerted[m.ID]
		shouldSend := time.Since(last) > 24*time.Hour
		if shouldSend {
			h.monSSLAlerted[m.ID] = time.Now()
		}
		h.monMu.Unlock()
		if shouldSend {
			_ = h.store.AddIncident(m.ID, m.Name, "ssl", fmt.Sprintf("SSL certificate expires in %d day(s)", res.SSLDays), 0)
			_ = h.sendUptimeTelegram(fmt.Sprintf("⚠️ <b>SSL EXPIRY WARNING — %s</b>\n• Target: <code>%s</code>\n• Certificate expires in <b>%d day(s)</b>.", m.Name, m.Target, res.SSLDays))
		}
	}

	// High Latency Spike Alert (once per 30m)
	if res.Up && cfg.NotifyLatency && m.LatencyWarnMs > 0 && res.LatencyMs >= m.LatencyWarnMs {
		h.monMu.Lock()
		if h.monLatAlerted == nil { h.monLatAlerted = map[int64]time.Time{} }
		last := h.monLatAlerted[m.ID]
		shouldSend := time.Since(last) > 30*time.Minute
		if shouldSend {
			h.monLatAlerted[m.ID] = time.Now()
		}
		h.monMu.Unlock()
		if shouldSend {
			_ = h.sendUptimeTelegram(fmt.Sprintf("⚠️ <b>HIGH LATENCY — %s</b>\n• Target: <code>%s</code>\n• Current: <b>%d ms</b> (threshold: %d ms)", m.Name, m.Target, res.LatencyMs, m.LatencyWarnMs))
		}
	}
}

func (h *Handler) autoHealLinkedTunnel(monID int64, monName string, tunnelID int64) {
	tun, err := h.store.GetTunnel(tunnelID)
	if err != nil {
		return
	}
	unit := fmt.Sprintf("mcontrol-tun-%d.service", tunnelID)
	cmd := fmt.Sprintf("systemctl restart %s 2>&1 || true", unit)
	if s1, err := h.store.GetServerWithSecret(tun.IranServerID); err == nil {
		_ = h.runOnServer(s1, cmd, 20*time.Second)
	}
	if s2, err := h.store.GetServerWithSecret(tun.ForeignServerID); err == nil {
		_ = h.runOnServer(s2, cmd, 20*time.Second)
	}
	_ = h.store.AddIncident(monID, monName, "heal", "Auto-Healed linked tunnel: "+tun.Name, 0)
	_ = h.sendUptimeTelegram(fmt.Sprintf("🛠 <b>AUTO-HEAL TRIGGERED</b>\nMonitor <b>%s</b> restarted linked tunnel <b>%s</b> on both nodes.", monName, tun.Name))
}

func (h *Handler) StartMonitorDaemon() {
	go func() {
		lastPrune := time.Now()
		sem := make(chan struct{}, 10)
		for {
			time.Sleep(5 * time.Second)
			if h.isStopped() {
				return
			}
			now := time.Now().Unix()
			list, err := h.store.ListMonitors()
			if err == nil {
				for i := range list {
					m := list[i]
					if !m.Enabled {
						continue
					}
					if now-m.LastCheckAt < int64(m.IntervalSec) {
						continue
					}
					monCopy := m
					go func(mc db.Monitor) {
						sem <- struct{}{}
						defer func() {
							if rec := recover(); rec != nil {
								log.Printf("[monitor] panic on %s: %v", mc.Name, rec)
							}
							<-sem
						}()
						h.executeMonitorCheck(&mc)
					}(monCopy)
				}
			}
			if time.Since(lastPrune) > 6*time.Hour {
				lastPrune = time.Now()
				h.store.PruneMonitorHistory(30)
			}
		}
	}()
}

// ── Monitor HTTP API Handlers ────────────────────────────────────────────────

type monitorView struct {
	db.Monitor
	Beats    []db.MonitorBeat `json:"beats"`
	Uptime24 float64          `json:"uptime_24h"`
	Uptime7d float64          `json:"uptime_7d"`
	Uptime30 float64          `json:"uptime_30d"`
}

func (h *Handler) handleMonitorsList(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListMonitors()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]monitorView, 0, len(list))
	upCnt, downCnt := 0, 0
	var avgSum float64
	for _, m := range list {
		beats, _ := h.store.ListMonitorBeats(m.ID, 30)
		u24, u7, u30 := h.store.GetMonitorUptimeStats(m.ID)
		if m.Status == "up" {
			upCnt++
		} else if m.Status == "down" {
			downCnt++
		}
		avgSum += u24
		views = append(views, monitorView{
			Monitor:  m,
			Beats:    beats,
			Uptime24: u24,
			Uptime7d: u7,
			Uptime30: u30,
		})
	}
	avgUptime := 100.0
	if len(views) > 0 {
		avgUptime = float64(int((avgSum/float64(len(views)))*100+0.5)) / 100.0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"monitors":   views,
		"total":      len(views),
		"up":         upCnt,
		"down":       downCnt,
		"avg_uptime": avgUptime,
	})
}

func (h *Handler) handleMonitorCreate(w http.ResponseWriter, r *http.Request) {
	var m db.Monitor
	if !readJSON(w, r, &m) {
		return
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Target = strings.TrimSpace(m.Target)
	if m.Name == "" || m.Target == "" {
		fail(w, http.StatusBadRequest, "monitor name and target are required")
		return
	}
	if m.Type == "" {
		m.Type = "http"
	}
	m.Enabled = true
	id, err := h.store.AddMonitor(&m)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	m.ID = id
	go h.executeMonitorCheck(&m)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": "Monitor created & probing started."})
}

func (h *Handler) handleMonitorUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := h.store.GetMonitor(id)
	if err != nil {
		fail(w, http.StatusNotFound, "monitor not found")
		return
	}
	var req db.Monitor
	if !readJSON(w, r, &req) {
		return
	}
	req.ID = id
	if strings.TrimSpace(req.Name) == "" {
		req.Name = cur.Name
	}
	if strings.TrimSpace(req.Target) == "" {
		req.Target = cur.Target
	}
	if req.Type == "" {
		req.Type = cur.Type
	}
	if req.ProbeFrom == "" {
		req.ProbeFrom = cur.ProbeFrom
	}
	if err := h.store.UpdateMonitor(&req); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Monitor updated."})
}

func (h *Handler) handleMonitorDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteMonitor(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleMonitorCheckNow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := h.store.GetMonitor(id)
	if err != nil {
		fail(w, http.StatusNotFound, "monitor not found")
		return
	}
	h.executeMonitorCheck(m)
	updated, _ := h.store.GetMonitor(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "monitor": updated})
}

func (h *Handler) handleMonitorImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind      string `json:"kind"`       // servers | tunnels
		ProbeFrom string `json:"probe_from"` // master | outbound | server:<id>
	}
	if !readJSON(w, r, &req) {
		return
	}
	existing, _ := h.store.ListMonitors()
	hasTarget := map[string]bool{}
	for _, m := range existing {
		hasTarget[m.Type+"|"+m.Target] = true
	}
	added := 0
	if req.Kind == "tunnels" {
		tuns, _ := h.store.ListTunnels()
		srvs, _ := h.store.ListServers()
		srvMap := map[int64]db.Server{}
		for _, s := range srvs {
			srvMap[s.ID] = s
		}
		for _, t := range tuns {
			if s, ok := srvMap[t.ForeignServerID]; ok {
				tgt := fmt.Sprintf("%s:%d", s.Host, t.TunnelPort)
				if hasTarget["tcp|"+tgt] {
					continue
				}
				m := &db.Monitor{
					Name:           "Tunnel: " + t.Name,
					Type:           "tcp",
					Target:         tgt,
					IntervalSec:    60,
					TimeoutSec:     8,
					Retries:        2,
					ProbeFrom:      fmt.Sprintf("server:%d", t.IranServerID),
					LinkedTunnelID: t.ID,
					AutoHeal:       "restart_tunnel",
					IsPublic:       true,
					PublicName:     t.Name,
					Enabled:        true,
				}
				if id, err := h.store.AddMonitor(m); err == nil {
					m.ID = id
					added++
					go h.executeMonitorCheck(m)
				}
			}
		}
	} else {
		srvs, _ := h.store.ListServers()
		for _, s := range srvs {
			if hasTarget["ping|"+s.Host] {
				continue
			}
			pf := strings.TrimSpace(req.ProbeFrom)
			if pf == "" || pf == fmt.Sprintf("server:%d", s.ID) {
				pf = "master"
			}
			m := &db.Monitor{
				Name:           "Server: " + s.Name,
				Type:           "ping",
				Target:         s.Host,
				IntervalSec:    60,
				TimeoutSec:     8,
				Retries:        2,
				ProbeFrom:      pf,
				LinkedServerID: s.ID,
				IsPublic:       true,
				PublicName:     s.Name,
				Enabled:        true,
			}
			if id, err := h.store.AddMonitor(m); err == nil {
				m.ID = id
				added++
				go h.executeMonitorCheck(m)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": added, "message": fmt.Sprintf("Imported %d monitor(s).", added)})
}

func (h *Handler) handleMonitorIncidents(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListIncidents(40)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "incidents": list})
}

func (h *Handler) handleMonitorConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := h.loadUptimeCfg()
		masked := cfg
		hasTok := cfg.BotToken != ""
		if len(masked.BotToken) > 12 {
			masked.BotToken = masked.BotToken[:6] + "••••••" + masked.BotToken[len(masked.BotToken)-4:]
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": masked, "has_token": hasTok})
		return
	}
	var req UptimeNotifyConfig
	if !readJSON(w, r, &req) {
		return
	}
	cur := h.loadUptimeCfg()
	if strings.Contains(req.BotToken, "••••") || strings.TrimSpace(req.BotToken) == "" {
		req.BotToken = cur.BotToken
	}
	if err := h.saveUptimeCfg(req); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Dedicated Uptime Bot & Status Page settings saved!"})
}

func (h *Handler) handleMonitorTestNotify(w http.ResponseWriter, r *http.Request) {
	var req UptimeNotifyConfig
	_ = readJSON(w, r, &req)
	cfg := h.loadUptimeCfg()
	if strings.TrimSpace(req.BotToken) != "" && !strings.Contains(req.BotToken, "••••") {
		cfg.BotToken = strings.TrimSpace(req.BotToken)
	}
	if strings.TrimSpace(req.ChatID) != "" {
		cfg.ChatID = strings.TrimSpace(req.ChatID)
	}
	if req.TopicID > 0 {
		cfg.TopicID = req.TopicID
	}
	err := h.sendUptimeTelegramWithCfg(cfg, "✅ <b>MControl Uptime Monitor — Test Notification</b>\nYour dedicated notification bot is connected and ready!")
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Test notification sent to Telegram!"})
}

// ── Public Status Page (/status & /api/public/status) ────────────────────────

func (h *Handler) ServePublicStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadUptimeCfg()
	if !cfg.PublicStatusEnabled {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/api/public/status" || strings.HasPrefix(r.URL.Path, "/api/public/status/") {
		list, _ := h.store.ListMonitors()
		type pubBeat struct {
			Status    string `json:"status"`
			LatencyMs int    `json:"latency_ms"`
			CreatedAt int64  `json:"created_at"`
		}
		type pubItem struct {
			Name          string    `json:"name"`
			Status        string    `json:"status"`
			LastLatencyMs int       `json:"last_latency_ms"`
			Uptime24      float64   `json:"uptime_24h"`
			Uptime30      float64   `json:"uptime_30d"`
			Beats         []pubBeat `json:"beats"`
		}
		items := []pubItem{}
		allUp := true
		for _, m := range list {
			if !m.IsPublic || !m.Enabled {
				continue
			}
			if m.Status == "down" {
				allUp = false
			}
			dn := strings.TrimSpace(m.PublicName)
			if dn == "" {
				dn = m.Name
			}
			beats, _ := h.store.ListMonitorBeats(m.ID, 30)
			pb := make([]pubBeat, 0, len(beats))
			for _, b := range beats {
				pb = append(pb, pubBeat{Status: b.Status, LatencyMs: b.LatencyMs, CreatedAt: b.CreatedAt})
			}
			u24, _, u30 := h.store.GetMonitorUptimeStats(m.ID)
			items = append(items, pubItem{
				Name:          dn,
				Status:        m.Status,
				LastLatencyMs: m.LastLatencyMs,
				Uptime24:      u24,
				Uptime30:      u30,
				Beats:         pb,
			})
		}
		var customTheme map[string]any
		if rawCT := strings.TrimSpace(h.store.GetSetting("ui_public_theme_json")); rawCT != "" {
			_ = json.Unmarshal([]byte(rawCT), &customTheme)
		}
		var brandCfg struct {
			BrandName string `json:"brand_name"`
			BrandLogo string `json:"brand_logo"`
		}
		if rawBr := strings.TrimSpace(h.store.GetSetting("ui_brand_json")); rawBr != "" {
			_ = json.Unmarshal([]byte(rawBr), &brandCfg)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			"title":        cfg.PublicStatusTitle,
			"theme":        cfg.PublicStatusTheme,
			"custom_theme": customTheme,
			"brand_name":   brandCfg.BrandName,
			"brand_logo":   brandCfg.BrandLogo,
			"all_up":       allUp,
			"updated_at":   time.Now().Unix(),
			"monitors":     items,
		})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(publicStatusHTML))
}

const publicStatusHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Service Status — MControl</title>
<style>
  :root { --bg1:#162040; --bg2:#090f1d; --card:rgba(19,27,46,.86); --bdr:rgba(148,163,184,.18); --txt:#e2e8f0; --mut:#94a3b8; --g:#22c55e; --y:#eab308; --r:#ef4444; --acc:#6366f1; --bar-r:6px; --glow:none; }
  body[data-pt="kuma-emerald"] { --bg1:#0c2b22; --bg2:#071410; --card:rgba(13,34,27,.88); --bdr:rgba(52,211,153,.22); --txt:#ecfdf5; --mut:#86efac; --g:#10b981; --acc:#10b981; }
  body[data-pt="prism-light"] { --bg1:#eef2ff; --bg2:#f8fafc; --card:rgba(255,255,255,.92); --bdr:rgba(99,102,241,.18); --txt:#0f172a; --mut:#64748b; --g:#059669; --y:#d97706; --r:#dc2626; --acc:#6366f1; }
  body[data-pt="neon-cyber"] { --bg1:#1e0b36; --bg2:#07040f; --card:rgba(20,10,38,.86); --bdr:rgba(192,132,252,.32); --txt:#f5f3ff; --mut:#c4b5fd; --g:#22d3ee; --y:#facc15; --r:#f43f5e; --acc:#e879f9; --glow:0 0 12px currentColor; }
  body[data-pt="obsidian-gold"] { --bg1:#241c0d; --bg2:#0c0a07; --card:rgba(24,20,14,.92); --bdr:rgba(245,158,11,.28); --txt:#fef3c7; --mut:#d6b87c; --g:#34d399; --y:#f59e0b; --r:#f87171; --acc:#f59e0b; }
  body[data-pt="minimal-slate"] { --bg1:#1e293b; --bg2:#0f172a; --card:rgba(30,41,59,.78); --bdr:rgba(148,163,184,.16); --txt:#f1f5f9; --mut:#94a3b8; --g:#4ade80; --acc:#38bdf8; --bar-r:3px; }
  * { box-sizing:border-box; margin:0; padding:0; }
  body { font-family:system-ui,-apple-system,sans-serif; background:radial-gradient(circle at top,var(--bg1),var(--bg2)); color:var(--txt); min-height:100vh; padding:28px 16px; transition:background .25s,color .25s; }
  .wrap { max-width:860px; margin:0 auto; }
  .hdr { display:flex; align-items:center; justify-content:space-between; gap:12px; flex-wrap:wrap; margin-bottom:20px; }
  .hdr-r { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }
  .th-sel { background:var(--card); color:var(--txt); border:1px solid var(--bdr); border-radius:999px; padding:6px 12px; font-size:.76rem; font-weight:700; cursor:pointer; outline:none; }
  .banner { padding:16px 20px; border-radius:20px; background:var(--card); border:1.5px solid var(--bdr); display:flex; align-items:center; gap:12px; margin-bottom:18px; backdrop-filter:blur(14px); }
  .banner.ok { border-color:var(--g); }
  .banner.deg { border-color:var(--r); }
  .dot { width:12px; height:12px; border-radius:50%; flex-shrink:0; }
  .dot.up { background:var(--g); box-shadow:0 0 12px var(--g); }
  .dot.down { background:var(--r); box-shadow:0 0 12px var(--r); }
  .card { background:var(--card); border:1px solid var(--bdr); border-radius:20px; padding:16px 18px; margin-bottom:12px; backdrop-filter:blur(14px); }
  .top { display:flex; justify-content:space-between; align-items:center; gap:10px; margin-bottom:10px; flex-wrap:wrap; }
  .pill { font-size:.74rem; font-weight:800; padding:3px 10px; border-radius:999px; }
  .pill.up { background:rgba(34,197,94,.16); color:var(--g); }
  .pill.down { background:rgba(239,68,68,.18); color:var(--r); }
  .bars { display:flex; gap:4px; align-items:center; height:26px; }
  .bar { flex:1; height:100%; border-radius:var(--bar-r); background:rgba(148,163,184,.16); transition:transform .15s; }
  .bar:hover { transform:scaleY(1.16); }
  .bar.up { background:var(--g); color:var(--g); box-shadow:var(--glow); }
  .bar.warn { background:var(--y); color:var(--y); box-shadow:var(--glow); }
  .bar.down { background:var(--r); color:var(--r); box-shadow:var(--glow); }
  .foot { display:flex; justify-content:space-between; font-size:.74rem; color:var(--mut); margin-top:8px; }
</style>
</head>
<body data-pt="mdesign-dark">
<div class="wrap">
  <div class="hdr">
    <div style="display:flex;align-items:center;gap:10px;"><img id="st-logo" alt="" style="display:none;height:30px;width:30px;object-fit:contain;border-radius:8px;"><h2 id="st-title">Service Status</h2></div>
    <div class="hdr-r">
      <select class="th-sel" id="pt-sel" onchange="setPT(this.value)" title="Switch Theme">
        <option value="mdesign-dark">🎨 MDesign Dark</option>
        <option value="kuma-emerald">💚 Kuma Emerald</option>
        <option value="prism-light">☀️ Prism Light</option>
        <option value="neon-cyber">⚡ Neon Cyber</option>
        <option value="obsidian-gold">✨ Obsidian Gold</option>
        <option value="minimal-slate">🧊 Minimal Slate</option>
      </select>
      <span id="st-upd" style="font-size:.76rem;color:var(--mut);">Loading...</span>
    </div>
  </div>
  <div class="banner" id="st-banner"><span class="dot up" id="st-bdot"></span><strong id="st-btxt">Checking services...</strong></div>
  <div id="st-list"></div>
</div>
<script>
let userPickedPT = localStorage.getItem('mc_pub_theme') || '';
function setPT(t){ userPickedPT = t; localStorage.setItem('mc_pub_theme', t); document.body.dataset.pt = t; document.getElementById('pt-sel').value = t; }
async function load(){
  const _sp = location.pathname.replace(/\/+$/, ''); const _api = (_sp.startsWith('/status/') && _sp.length > 8) ? ('/api/public' + _sp) : '/api/public/status'; const r = await fetch(_api).then(x=>x.json()).catch(()=>null);
  if(!r||!r.ok) return;
  const th = userPickedPT || r.theme || 'mdesign-dark';
  document.body.dataset.pt = th;
  document.getElementById('pt-sel').value = th;
  if (r.custom_theme && !userPickedPT) {
    const ct = r.custom_theme, rs = document.documentElement.style;
    if (ct.bg) { rs.setProperty('--bg1', ct.surface || ct.bg); rs.setProperty('--bg2', ct.bg); }
    if (ct.surface) rs.setProperty('--card', ct.surface);
    if (ct.border) rs.setProperty('--bdr', ct.border);
    if (ct.text) rs.setProperty('--txt', ct.text);
    if (ct.muted) rs.setProperty('--mut', ct.muted);
    if (ct.accent) rs.setProperty('--acc', ct.accent);
    if (ct.green) rs.setProperty('--g', ct.green);
    if (ct.yellow) rs.setProperty('--y', ct.yellow);
    if (ct.red) rs.setProperty('--r', ct.red);
  }
  const finalTitle = (r.title && r.title !== 'MControl Service Status') ? r.title : (r.brand_name ? (r.brand_name + ' — Status') : (r.title || 'Service Status'));
  document.getElementById('st-title').textContent = finalTitle;
  document.title = finalTitle;
  const lg = document.getElementById('st-logo');
  if (lg) { if (r.brand_logo) { lg.src = r.brand_logo; lg.style.display = 'block'; } else { lg.style.display = 'none'; } }
  document.getElementById('st-upd').textContent = 'Updated: ' + new Date(r.updated_at*1000).toLocaleTimeString();
  const b = document.getElementById('st-banner'), d = document.getElementById('st-bdot'), t = document.getElementById('st-btxt');
  b.className = 'banner ' + (r.all_up ? 'ok' : 'deg');
  d.className = 'dot ' + (r.all_up ? 'up' : 'down');
  t.textContent = r.all_up ? 'All Systems Operational' : 'Partial Service Disruption Detected';
  document.getElementById('st-list').innerHTML = (r.monitors||[]).map(m => {
    const beats = m.beats || [];
    const pad = Math.max(0, 30 - beats.length);
    const bars = '<div class="bar"></div>'.repeat(pad) + beats.map(x => '<div class="bar '+x.status+'" title="'+new Date(x.created_at*1000).toLocaleString()+' · '+x.latency_ms+'ms"></div>').join('');
    return '<div class="card"><div class="top"><div style="display:flex;align-items:center;gap:8px;"><span class="dot '+(m.status==='down'?'down':'up')+'"></span><strong>'+m.name+'</strong></div><div style="display:flex;gap:8px;align-items:center;"><span style="font-size:.78rem;color:var(--mut);">'+m.last_latency_ms+' ms</span><span class="pill '+(m.status==='down'?'down':'up')+'">'+m.uptime_24h+'% (24h)</span></div></div><div class="bars">'+bars+'</div><div class="foot"><span>30-check heartbeat</span><span>30d Uptime: '+m.uptime_30d+'%</span></div></div>';
  }).join('') || '<div class="card" style="color:var(--mut);">No public monitors published yet.</div>';
}
load(); setInterval(load, 30000);
</script>
</body>
</html>`
