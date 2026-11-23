package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sysinfo"
	"github.com/htzserv/MControl/internal/updater"
	"github.com/htzserv/MControl/internal/version"
)

type telegramConfig struct {
	Enabled            bool   `json:"enabled"`
	BotToken           string `json:"bot_token"`
	AdminIDs           string `json:"admin_ids"`
	BackupChatID       string `json:"backup_chat_id"`
	AutoBackupHours    int    `json:"auto_backup_hours"` // 0=off, 6, 12, 24, 48
	BackupBeforeUpdate bool   `json:"backup_before_update"`
	AlertServers       bool   `json:"alert_servers"`
	AlertTunnels       bool   `json:"alert_tunnels"`
	DailyDigest        bool   `json:"daily_digest"`
	DigestHour         int    `json:"digest_hour"` // 0..23 Iran time (UTC+3:30)
}

type tgWizard struct {
	Step    string // "ports"
	IranID  int64
	ForID   int64
	Engine  string
	Created time.Time
}

type tgRuntime struct {
	mu             sync.Mutex
	connected      bool
	botUsername    string
	viaOutbound    bool
	lastError      string
	lastBackupAt   int64
	lastDigestDate string
	reloadCh       chan struct{}
	wizards        map[int64]*tgWizard
	pendingRestore map[int64]string // chatID -> temp zip path
	srvPrevStatus  map[int64]string
	alertCooldown  map[string]time.Time
}

func newTgRuntime() *tgRuntime {
	return &tgRuntime{
		reloadCh:       make(chan struct{}, 2),
		wizards:        map[int64]*tgWizard{},
		pendingRestore: map[int64]string{},
		srvPrevStatus:  map[int64]string{},
		alertCooldown:  map[string]time.Time{},
	}
}

func (h *Handler) tg() *tgRuntime {
	if h.tgRT == nil {
		h.tgRT = newTgRuntime()
	}
	return h.tgRT
}

func (h *Handler) loadTelegramCfg() telegramConfig {
	raw := h.store.GetSetting("telegram_bot_json")
	cfg := telegramConfig{
		BackupBeforeUpdate: true,
		AlertServers:       true,
		AlertTunnels:       true,
		DigestHour:         0,
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg
}

func (h *Handler) saveTelegramCfg(cfg telegramConfig) {
	if b, err := json.Marshal(cfg); err == nil {
		_ = h.store.SetSetting("telegram_bot_json", string(b))
	}
}

func parseAdminIDs(raw string) map[int64]bool {
	out := map[int64]bool{}
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == ';' }) {
		if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil && id != 0 {
			out[id] = true
		}
	}
	return out
}

func firstTargetChat(cfg telegramConfig, preferBackup bool) int64 {
	if preferBackup && strings.TrimSpace(cfg.BackupChatID) != "" {
		if id, err := strconv.ParseInt(strings.TrimSpace(cfg.BackupChatID), 10, 64); err == nil && id != 0 {
			return id
		}
	}
	for _, p := range strings.FieldsFunc(cfg.AdminIDs, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == ';' }) {
		if id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64); err == nil && id != 0 {
			return id
		}
	}
	return 0
}

func (h *Handler) tgHTTPDo(reqBuilder func() (*http.Request, error), timeout time.Duration) ([]byte, bool, error) {
	proxy := h.telegramSocksProxy()
	if proxy != "" {
		req, err := reqBuilder()
		if err != nil {
			return nil, false, err
		}
		client := updater.MakeHTTPClient(timeout, proxy)
		resp, err := client.Do(req)
		if err == nil {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
			resp.Body.Close()
			if rerr == nil {
				return body, true, nil
			}
		}
		// Auto-fallback to direct if outbound failed
	}
	req, err := reqBuilder()
	if err != nil {
		return nil, false, err
	}
	client := updater.MakeHTTPClient(timeout, "")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 25<<20))
	return body, false, err
}

func (h *Handler) tgAPIJSON(token, method string, payload any, timeout time.Duration) (map[string]any, bool, error) {
	var bodyBytes []byte
	if payload != nil {
		bodyBytes, _ = json.Marshal(payload)
	}
	urlStr := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	raw, viaOB, err := h.tgHTTPDo(func() (*http.Request, error) {
		if payload != nil {
			req, err := http.NewRequest("POST", urlStr, bytes.NewReader(bodyBytes))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
			}
			return req, err
		}
		return http.NewRequest("GET", urlStr, nil)
	}, timeout)
	if err != nil {
		return nil, viaOB, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, viaOB, fmt.Errorf("invalid telegram response")
	}
	if okVal, _ := out["ok"].(bool); !okVal {
		desc, _ := out["description"].(string)
		if desc == "" {
			desc = "telegram API error"
		}
		return out, viaOB, fmt.Errorf("%s", desc)
	}
	return out, viaOB, nil
}

func (h *Handler) tgSendMessage(token string, chatID int64, text string, kb [][]map[string]string) error {
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if len(kb) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": kb}
	}
	_, _, err := h.tgAPIJSON(token, "sendMessage", payload, 15*time.Second)
	return err
}

func (h *Handler) tgAnswerCallback(token, cbID, text string) {
	if cbID == "" {
		return
	}
	_, _, _ = h.tgAPIJSON(token, "answerCallbackQuery", map[string]any{
		"callback_query_id": cbID,
		"text":              text,
	}, 8*time.Second)
}

func (h *Handler) tgSendDocument(token string, chatID int64, filename string, data []byte, caption string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = mw.WriteField("caption", caption)
		_ = mw.WriteField("parse_mode", "HTML")
	}
	fw, err := mw.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	_ = mw.Close()

	ct := mw.FormDataContentType()
	rawPayload := buf.Bytes()
	urlStr := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", token)
	raw, _, err := h.tgHTTPDo(func() (*http.Request, error) {
		req, err := http.NewRequest("POST", urlStr, bytes.NewReader(rawPayload))
		if err == nil {
			req.Header.Set("Content-Type", ct)
		}
		return req, err
	}, 60*time.Second)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("invalid telegram response")
	}
	if okVal, _ := out["ok"].(bool); !okVal {
		desc, _ := out["description"].(string)
		return fmt.Errorf("telegram sendDocument failed: %s", desc)
	}
	return nil
}

// buildBackupZipBytes creates the exact same full encrypted backup ZIP as /api/backup/db.
func (h *Handler) buildBackupZipBytes() ([]byte, string, error) {
	tmpDir, err := os.MkdirTemp("", "mcontrol-tgbak-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpDir)

	snap := filepath.Join(tmpDir, "mcontrol.db")
	if err := h.store.Snapshot(snap); err != nil {
		return nil, "", err
	}
	dbBytes, err := os.ReadFile(snap)
	if err != nil {
		return nil, "", err
	}
	keyBytes, err := os.ReadFile(h.store.KeyPath())
	if err != nil {
		return nil, "", err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := []struct {
		name string
		data []byte
	}{{"mcontrol.db", dbBytes}, {".secret.key", keyBytes}}
	if b, err := os.ReadFile(filepath.Join(h.store.DataDir(), "tls.crt")); err == nil {
		entries = append(entries, struct{ name string; data []byte }{"tls.crt", b})
	}
	if b, err := os.ReadFile(filepath.Join(h.store.DataDir(), "tls.key")); err == nil {
		entries = append(entries, struct{ name string; data []byte }{"tls.key", b})
	}
	for _, f := range entries {
		fw, err := zw.Create(f.name)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(f.data); err != nil {
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	fname := fmt.Sprintf("mcontrol-backup-%s.zip", time.Now().Format("20060102-150405"))
	return buf.Bytes(), fname, nil
}

func (h *Handler) sendTelegramBackup(chatID int64, reason string) error {
	cfg := h.loadTelegramCfg()
	if strings.TrimSpace(cfg.BotToken) == "" {
		return fmt.Errorf("bot token is not configured")
	}
	if chatID == 0 {
		chatID = firstTargetChat(cfg, true)
	}
	if chatID == 0 {
		return fmt.Errorf("no admin or backup chat ID configured")
	}
	data, fname, err := h.buildBackupZipBytes()
	if err != nil {
		return err
	}
	srvs, _ := h.store.ListServers()
	tuns, _ := h.store.ListTunnels()
	st := h.store.GetSettings()
	panelName := st.PanelName
	if panelName == "" {
		panelName = "MControl"
	}
	caption := fmt.Sprintf(
		"📦 <b>%s Backup</b> (%s)\n• Version: <code>v%s</code>\n• Servers: <b>%d</b> | Tunnels: <b>%d</b>\n• Size: <code>%.1f KB</code>\n• Time: <code>%s</code>",
		tgEsc(panelName), tgEsc(reason), version.Current, len(srvs), len(tuns), float64(len(data))/1024.0, time.Now().Format("2006-01-02 15:04:05"),
	)
	if err := h.tgSendDocument(cfg.BotToken, chatID, fname, data, caption); err != nil {
		return err
	}
	h.tg().mu.Lock()
	h.tg().lastBackupAt = time.Now().Unix()
	h.tg().mu.Unlock()
	_ = h.store.SetSetting("telegram_last_backup_at", strconv.FormatInt(time.Now().Unix(), 10))
	return nil
}

func (h *Handler) maybeSendPreUpdateBackup(source string) {
	cfg := h.loadTelegramCfg()
	if !cfg.Enabled || !cfg.BackupBeforeUpdate || strings.TrimSpace(cfg.BotToken) == "" {
		return
	}
	if err := h.sendTelegramBackup(0, "Pre-Update: "+source); err != nil {
		log.Printf("[telegram] pre-update backup warning: %v", err)
	}
}

func tgEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func mainMenuKB() [][]map[string]string {
	return [][]map[string]string{
		{
			{"text": "📊 Status & Stats", "callback_data": "mc:status"},
			{"text": "🖥 Servers", "callback_data": "mc:servers"},
		},
		{
			{"text": "🚇 Tunnels", "callback_data": "mc:tunnels"},
			{"text": "➕ New Tunnel", "callback_data": "mc:wiz:start"},
		},
		{
			{"text": "📦 Backup Now", "callback_data": "mc:backup"},
			{"text": "🔄 Updates", "callback_data": "mc:updates"},
		},
		{
			{"text": "🆘 Emergency Tools", "callback_data": "mc:emergency"},
		},
	}
}

func (h *Handler) buildStatusText() string {
	st := sysinfo.Collect()
	srvs, _ := h.store.ListServers()
	tuns, _ := h.store.ListTunnels()
	onS := 0
	for _, s := range srvs {
		if s.Status == "online" {
			onS++
		}
	}
	actT := 0
	for _, t := range tuns {
		if t.Status == "active" {
			actT++
		}
	}
	obState := "STOPPED"
	if isOutboundActive() {
		obState = "ACTIVE (127.0.0.1:10808)"
	}
	return fmt.Sprintf(
		"⚡ <b>MControl v%s — Live Status</b>\n\n"+
			"🖥 <b>Master:</b> <code>%s</code> (%s)\n"+
			"• CPU: <b>%.1f%%</b> | RAM: <b>%.1f%%</b> (%d/%d MB)\n"+
			"• Disk: <b>%.1f%%</b> | Uptime: %s\n\n"+
			"🌐 <b>Servers:</b> <b>%d/%d</b> online\n"+
			"🚇 <b>Tunnels:</b> <b>%d/%d</b> active\n"+
			"🛡 <b>Master Outbound:</b> <code>%s</code>",
		version.Current, tgEsc(st.Hostname), tgEsc(st.IPv4),
		st.CPUPercent, st.RAMPercent, st.RAMUsedMB, st.RAMTotalMB,
		st.DiskPercent, tgEsc(st.Uptime),
		onS, len(srvs), actT, len(tuns), obState,
	)
}

func (h *Handler) handleTGUpdate(token string, upd map[string]any, admins map[int64]bool) {
	// 1. Callback Query (Inline Buttons)
	if cb, ok := upd["callback_query"].(map[string]any); ok {
		cbID, _ := cb["id"].(string)
		data, _ := cb["data"].(string)
		msg, _ := cb["message"].(map[string]any)
		chat, _ := msg["chat"].(map[string]any)
		chatID := int64(0)
		if f, ok := chat["id"].(float64); ok {
			chatID = int64(f)
		}
		if !admins[chatID] {
			h.tgAnswerCallback(token, cbID, "Unauthorized")
			return
		}
		h.tgAnswerCallback(token, cbID, "")
		h.handleTGCallback(token, chatID, data)
		return
	}

	// 2. Normal Message or Document
	msg, ok := upd["message"].(map[string]any)
	if !ok {
		return
	}
	chat, _ := msg["chat"].(map[string]any)
	chatID := int64(0)
	if f, ok := chat["id"].(float64); ok {
		chatID = int64(f)
	}
	if !admins[chatID] {
		return
	}

	// 2a. Document upload (Send-to-Restore .zip)
	if doc, ok := msg["document"].(map[string]any); ok {
		fname, _ := doc["file_name"].(string)
		fileID, _ := doc["file_id"].(string)
		if strings.HasSuffix(strings.ToLower(fname), ".zip") && fileID != "" {
			h.handleTGBackupUpload(token, chatID, fileID, fname)
			return
		}
	}

	text, _ := msg["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Check active wizard step (waiting for ports)
	h.tg().mu.Lock()
	wiz := h.tg().wizards[chatID]
	if wiz != nil && time.Since(wiz.Created) > 10*time.Minute {
		delete(h.tg().wizards, chatID)
		wiz = nil
	}
	h.tg().mu.Unlock()

	if wiz != nil && wiz.Step == "ports" && !strings.HasPrefix(text, "/") {
		h.tg().mu.Lock()
		delete(h.tg().wizards, chatID)
		h.tg().mu.Unlock()
		h.deployTunnelFromTG(token, chatID, wiz, text)
		return
	}

	switch strings.ToLower(strings.Fields(text)[0]) {
	case "/start", "/menu":
		_ = h.tgSendMessage(token, chatID, "🎛 <b>MControl Control Center</b>\nSelect an action below:", mainMenuKB())
	case "/status":
		_ = h.tgSendMessage(token, chatID, h.buildStatusText(), mainMenuKB())
	case "/backup":
		_ = h.tgSendMessage(token, chatID, "⏳ Creating encrypted backup archive...", nil)
		if err := h.sendTelegramBackup(chatID, "Manual"); err != nil {
			_ = h.tgSendMessage(token, chatID, "❌ Backup failed: <code>"+tgEsc(err.Error())+"</code>", mainMenuKB())
		}
	default:
		_ = h.tgSendMessage(token, chatID, "Use /menu to open the MControl dashboard.", mainMenuKB())
	}
}

func (h *Handler) handleTGCallback(token string, chatID int64, data string) {
	switch {
	case data == "mc:menu":
		_ = h.tgSendMessage(token, chatID, "🎛 <b>MControl Control Center</b>", mainMenuKB())

	case data == "mc:status":
		_ = h.tgSendMessage(token, chatID, h.buildStatusText(), mainMenuKB())

	case data == "mc:servers":
		srvs, _ := h.store.ListServers()
		if len(srvs) == 0 {
			_ = h.tgSendMessage(token, chatID, "No servers configured yet.", mainMenuKB())
			return
		}
		var sb strings.Builder
		sb.WriteString("🖥 <b>Managed Servers</b>\n\n")
		for _, s := range srvs {
			ico := "🟢"
			if s.Status != "online" {
				ico = "🔴"
			}
			roleTag := "EX"
			if s.Role == "iran" {
				roleTag = "IR"
			}
			sb.WriteString(fmt.Sprintf("%s <b>[%s] %s</b> — <code>%s</code>\n    CPU: %s | RAM: %s | Ping: %s\n",
				ico, roleTag, tgEsc(s.Name), tgEsc(s.Host), tgEsc(s.CPU), tgEsc(s.RAM), tgEsc(s.PingMs)))
		}
		_ = h.tgSendMessage(token, chatID, sb.String(), mainMenuKB())

	case data == "mc:tunnels":
		tuns, _ := h.store.ListTunnels()
		if len(tuns) == 0 {
			_ = h.tgSendMessage(token, chatID, "🚇 No tunnels configured yet.", [][]map[string]string{
				{{"text": "➕ Create New Tunnel", "callback_data": "mc:wiz:start"}},
				{{"text": "⬅️ Back to Menu", "callback_data": "mc:menu"}},
			})
			return
		}
		var kb [][]map[string]string
		for _, t := range tuns {
			ico := "🟢"
			if t.Status != "active" {
				ico = "🟠"
			}
			kb = append(kb, []map[string]string{{
				"text":          fmt.Sprintf("%s %s (%s) • %s ⇄ %s", ico, t.Name, strings.ToUpper(t.Engine), t.IranName, t.ForeignName),
				"callback_data": fmt.Sprintf("mc:tun:view:%d", t.ID),
			}})
		}
		kb = append(kb, []map[string]string{
			{"text": "➕ New Tunnel", "callback_data": "mc:wiz:start"},
			{"text": "⬅️ Menu", "callback_data": "mc:menu"},
		})
		_ = h.tgSendMessage(token, chatID, "🚇 <b>Active Tunnels</b>\nTap a tunnel to manage it:", kb)

	case strings.HasPrefix(data, "mc:tun:view:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "mc:tun:view:"), 10, 64)
		t, err := h.store.GetTunnel(id)
		if err != nil {
			_ = h.tgSendMessage(token, chatID, "❌ Tunnel not found.", mainMenuKB())
			return
		}
		txt := fmt.Sprintf("🚇 <b>%s</b> (#%d)\n• Engine: <code>%s</code> (%s)\n• Route: <b>%s</b> ⇄ <b>%s</b>\n• Control Port: <code>%d</code>\n• Ports: <code>%s</code>\n• Status: <b>%s</b>",
			tgEsc(t.Name), t.ID, strings.ToUpper(t.Engine), tgEsc(t.Transport),
			tgEsc(t.IranName), tgEsc(t.ForeignName), t.TunnelPort, tgEsc(t.Ports), strings.ToUpper(t.Status))
		kb := [][]map[string]string{
			{
				{"text": "🔄 Restart", "callback_data": fmt.Sprintf("mc:tun:act:%d:restart", id)},
				{"text": "⏹ Stop", "callback_data": fmt.Sprintf("mc:tun:act:%d:stop", id)},
			},
			{
				{"text": "📋 Status", "callback_data": fmt.Sprintf("mc:tun:act:%d:status", id)},
				{"text": "📜 Logs", "callback_data": fmt.Sprintf("mc:tun:act:%d:logs", id)},
			},
			{
				{"text": "🗑 Delete Tunnel", "callback_data": fmt.Sprintf("mc:tun:del:%d", id)},
				{"text": "⬅️ Tunnels", "callback_data": "mc:tunnels"},
			},
		}
		_ = h.tgSendMessage(token, chatID, txt, kb)

	case strings.HasPrefix(data, "mc:tun:act:"):
		parts := strings.Split(strings.TrimPrefix(data, "mc:tun:act:"), ":")
		if len(parts) != 2 {
			return
		}
		id, _ := strconv.ParseInt(parts[0], 10, 64)
		action := parts[1]
		out := h.runTunnelActionInternal(id, action)
		if len(out) > 3200 {
			out = out[len(out)-3200:]
		}
		_ = h.tgSendMessage(token, chatID, fmt.Sprintf("⚡ <b>Tunnel #%d — %s</b>\n<pre>%s</pre>", id, strings.ToUpper(action), tgEsc(out)), [][]map[string]string{
			{{"text": "⬅️ Back to Tunnel", "callback_data": fmt.Sprintf("mc:tun:view:%d", id)}},
		})

	case strings.HasPrefix(data, "mc:tun:del:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(data, "mc:tun:del:"), 10, 64)
		msg := h.deleteTunnelInternal(id)
		_ = h.tgSendMessage(token, chatID, "🗑 "+tgEsc(msg), [][]map[string]string{
			{{"text": "⬅️ Tunnels", "callback_data": "mc:tunnels"}},
		})

	case data == "mc:wiz:start":
		srvs, _ := h.store.ListServers()
		if len(srvs) < 2 {
			_ = h.tgSendMessage(token, chatID, "❌ You need at least 2 servers to build a tunnel.", mainMenuKB())
			return
		}
		var kb [][]map[string]string
		for _, s := range srvs {
			tag := "EX"
			if s.Role == "iran" {
				tag = "IR"
			}
			kb = append(kb, []map[string]string{{
				"text":          fmt.Sprintf("[%s] %s (%s)", tag, s.Name, s.Host),
				"callback_data": fmt.Sprintf("mc:wiz:ir:%d", s.ID),
			}})
		}
		kb = append(kb, []map[string]string{{"text": "✖ Cancel", "callback_data": "mc:menu"}})
		_ = h.tgSendMessage(token, chatID, "🛠 <b>New Tunnel Wizard (1/4)</b>\nSelect the <b>Entry (Iran) Server</b>:", kb)

	case strings.HasPrefix(data, "mc:wiz:ir:"):
		irID, _ := strconv.ParseInt(strings.TrimPrefix(data, "mc:wiz:ir:"), 10, 64)
		srvs, _ := h.store.ListServers()
		var kb [][]map[string]string
		for _, s := range srvs {
			if s.ID == irID {
				continue
			}
			tag := "EX"
			if s.Role == "iran" {
				tag = "IR"
			}
			kb = append(kb, []map[string]string{{
				"text":          fmt.Sprintf("[%s] %s (%s)", tag, s.Name, s.Host),
				"callback_data": fmt.Sprintf("mc:wiz:fr:%d:%d", irID, s.ID),
			}})
		}
		kb = append(kb, []map[string]string{{"text": "✖ Cancel", "callback_data": "mc:menu"}})
		_ = h.tgSendMessage(token, chatID, "🛠 <b>New Tunnel Wizard (2/4)</b>\nSelect the <b>Exit (Foreign) Server</b>:", kb)

	case strings.HasPrefix(data, "mc:wiz:fr:"):
		parts := strings.Split(strings.TrimPrefix(data, "mc:wiz:fr:"), ":")
		if len(parts) != 2 {
			return
		}
		kb := [][]map[string]string{
			{
				{"text": "Backhaul (TCP)", "callback_data": fmt.Sprintf("mc:wiz:eng:%s:%s:backhaul", parts[0], parts[1])},
				{"text": "Rathole", "callback_data": fmt.Sprintf("mc:wiz:eng:%s:%s:rathole", parts[0], parts[1])},
			},
			{
				{"text": "GRE (4-to-4)", "callback_data": fmt.Sprintf("mc:wiz:eng:%s:%s:gre", parts[0], parts[1])},
				{"text": "VXLAN", "callback_data": fmt.Sprintf("mc:wiz:eng:%s:%s:vxlan", parts[0], parts[1])},
			},
			{
				{"text": "Paqet", "callback_data": fmt.Sprintf("mc:wiz:eng:%s:%s:paqet", parts[0], parts[1])},
				{"text": "✖ Cancel", "callback_data": "mc:menu"},
			},
		}
		_ = h.tgSendMessage(token, chatID, "🛠 <b>New Tunnel Wizard (3/4)</b>\nChoose the <b>Tunnel Engine</b>:", kb)

	case strings.HasPrefix(data, "mc:wiz:eng:"):
		parts := strings.Split(strings.TrimPrefix(data, "mc:wiz:eng:"), ":")
		if len(parts) != 3 {
			return
		}
		irID, _ := strconv.ParseInt(parts[0], 10, 64)
		frID, _ := strconv.ParseInt(parts[1], 10, 64)
		h.tg().mu.Lock()
		h.tg().wizards[chatID] = &tgWizard{
			Step:    "ports",
			IranID:  irID,
			ForID:   frID,
			Engine:  parts[2],
			Created: time.Now(),
		}
		h.tg().mu.Unlock()
		_ = h.tgSendMessage(token, chatID, "🛠 <b>New Tunnel Wizard (4/4)</b>\nReply with the <b>Forward Ports</b> (comma-separated, e.g. <code>443, 8443, 2053</code>):", [][]map[string]string{
			{{"text": "✖ Cancel", "callback_data": "mc:menu"}},
		})

	case data == "mc:backup":
		_ = h.tgSendMessage(token, chatID, "⏳ Creating encrypted backup archive...", nil)
		if err := h.sendTelegramBackup(chatID, "Manual"); err != nil {
			_ = h.tgSendMessage(token, chatID, "❌ Backup failed: <code>"+tgEsc(err.Error())+"</code>", mainMenuKB())
		}

	case data == "mc:updates":
		st := h.store.GetSettings()
		chk := updater.CheckAll(st.PanelGitHubURL, st.PanelMirrorURL, h.githubSocksProxy(), st.BetaBuilds, st.InstalledBetaBuild)
		txt := fmt.Sprintf("🔄 <b>Panel Updates</b>\n• Installed: <code>v%s</code>\n• GitHub: <code>%s</code>\n• Mirror: <code>%s</code>",
			chk.CurrentVersion,
			tgFmtSrc(chk.GitHub),
			tgFmtSrc(chk.Mirror),
		)
		if chk.Beta.Enabled {
			txt += fmt.Sprintf("\n• Rolling Beta: <code>%s</code>", tgFmtBeta(chk.Beta))
		}
		kb := [][]map[string]string{
			{
				{"text": "⬇️ Update from GitHub", "callback_data": "mc:upd:github"},
				{"text": "⬇️ Update from Mirror", "callback_data": "mc:upd:mirror"},
			},
		}
		if chk.Beta.Enabled {
			kb = append(kb, []map[string]string{{"text": "🧪 Install Rolling Beta", "callback_data": "mc:upd:beta"}})
		}
		kb = append(kb, []map[string]string{{"text": "⬅️ Menu", "callback_data": "mc:menu"}})
		_ = h.tgSendMessage(token, chatID, txt, kb)

	case strings.HasPrefix(data, "mc:upd:"):
		src := strings.TrimPrefix(data, "mc:upd:")
		st := h.store.GetSettings()
		h.maybeSendPreUpdateBackup(src)
		err := updater.StartRemoteUpdate(src, st.PanelGitHubURL, st.PanelMirrorURL, h.githubSocksProxy(), st.AllowUnverified, func(build int) {
			_ = h.store.SetInstalledBetaBuild(build)
		}, func() {
			time.Sleep(1500 * time.Millisecond)
			os.Exit(0)
		})
		if err != nil {
			_ = h.tgSendMessage(token, chatID, "❌ Update failed to start: <code>"+tgEsc(err.Error())+"</code>", mainMenuKB())
		} else {
			_ = h.tgSendMessage(token, chatID, "🚀 Update started from <b>"+tgEsc(src)+"</b>. Panel will restart automatically in a few seconds.", nil)
		}

	case data == "mc:emergency":
		_, wpath, port := h.store.GetPanelConfig(h.currentPort)
		st := sysinfo.Collect()
		urlStr := fmt.Sprintf("http://%s:%s/%s/", st.IPv4, port, wpath)
		txt := fmt.Sprintf("🆘 <b>Emergency Panel Tools</b>\n• Panel URL: <code>%s</code>\n• Port: <code>%s</code> | Path: <code>/%s/</code>",
			tgEsc(urlStr), tgEsc(port), tgEsc(wpath))
		kb := [][]map[string]string{
			{
				{"text": "🔄 Restart Panel", "callback_data": "mc:em:restart"},
				{"text": "🔓 Reset 2FA", "callback_data": "mc:em:reset2fa"},
			},
			{{"text": "⬅️ Menu", "callback_data": "mc:menu"}},
		}
		_ = h.tgSendMessage(token, chatID, txt, kb)

	case data == "mc:em:restart":
		_ = h.tgSendMessage(token, chatID, "🔄 Restarting MControl service...", nil)
		go func() {
			time.Sleep(800 * time.Millisecond)
			os.Exit(0)
		}()

	case data == "mc:em:reset2fa":
		_ = h.store.SetSetting("twofa_enabled", "0")
		_ = h.tgSendMessage(token, chatID, "🔓 <b>2FA has been disabled.</b> You can now log in with your password.", mainMenuKB())

	case data == "mc:restore:confirm":
		h.tg().mu.Lock()
		path := h.tg().pendingRestore[chatID]
		delete(h.tg().pendingRestore, chatID)
		h.tg().mu.Unlock()
		if path == "" {
			_ = h.tgSendMessage(token, chatID, "❌ No pending backup file to restore.", mainMenuKB())
			return
		}
		if err := h.applyRestoreZipFromPath(path); err != nil {
			_ = os.Remove(path)
			_ = h.tgSendMessage(token, chatID, "❌ Restore failed: <code>"+tgEsc(err.Error())+"</code>", mainMenuKB())
			return
		}
		_ = os.Remove(path)
		_ = h.tgSendMessage(token, chatID, "✅ <b>Backup restored successfully!</b> Restarting MControl now...", nil)
		go func() {
			time.Sleep(900 * time.Millisecond)
			os.Exit(0)
		}()
	}
}

func tgFmtSrc(s updater.SourceStatus) string {
	if s.Error != "" {
		return s.Error
	}
	if s.Available {
		return "v" + s.Version + " (NEW)"
	}
	return "v" + s.Version + " (up to date)"
}

func tgFmtBeta(b updater.BetaStatus) string {
	if b.Error != "" {
		return b.Error
	}
	if b.Available {
		return "v" + b.Tag + " (NEW)"
	}
	return "v" + b.Tag + " (up to date)"
}

func (h *Handler) runTunnelActionInternal(id int64, action string) string {
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		return "tunnel not found"
	}
	iranSrv, _ := h.store.GetServerWithSecret(tun.IranServerID)
	forSrv, _ := h.store.GetServerWithSecret(tun.ForeignServerID)
	unit := fmt.Sprintf("mcontrol-tun-%d.service", id)
	var cmd string
	switch action {
	case "start", "stop", "restart":
		cmd = fmt.Sprintf("systemctl %s %s 2>&1; systemctl is-active %s 2>&1", action, unit, unit)
	case "status":
		cmd = fmt.Sprintf("systemctl status %s --no-pager -l 2>&1 | head -n 16", unit)
	case "logs":
		cmd = fmt.Sprintf("journalctl -u %s -n 25 --no-pager 2>&1", unit)
	default:
		return "invalid action"
	}
	var out strings.Builder
	if iranSrv != nil {
		r := h.runOnServer(iranSrv, cmd, 20*time.Second)
		out.WriteString("[IRAN] " + strings.TrimSpace(r.Output) + "\n")
	}
	if forSrv != nil {
		r := h.runOnServer(forSrv, cmd, 20*time.Second)
		out.WriteString("[FOREIGN] " + strings.TrimSpace(r.Output) + "\n")
	}
	if action == "stop" {
		_ = h.store.UpdateTunnelStatus(id, "stopped", "")
	} else if action == "start" || action == "restart" {
		_ = h.store.UpdateTunnelStatus(id, "active", "")
	}
	return strings.TrimSpace(out.String())
}

func (h *Handler) deleteTunnelInternal(id int64) string {
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		return "Tunnel not found."
	}
	iranSrv, _ := h.store.GetServerWithSecret(tun.IranServerID)
	forSrv, _ := h.store.GetServerWithSecret(tun.ForeignServerID)
	unit := fmt.Sprintf("mcontrol-tun-%d", id)
	ifName := fmt.Sprintf("mctun%d", id)
	tag := fmt.Sprintf("mc_tun_%d", id)
	cleanCmd := fmt.Sprintf(`systemctl disable --now %s.service 2>/dev/null || true
rm -f /etc/systemd/system/%s.service /etc/mcontrol/tunnels/%s.toml /etc/mcontrol/tunnels/%s.sh /etc/mcontrol/tunnels/%s.yaml
ip link del %s 2>/dev/null || true
ip tunnel del %s 2>/dev/null || true
for tbl in nat raw mangle; do iptables-save -t "$tbl" | grep -v "%s" | iptables-restore -T "$tbl" 2>/dev/null || true; done
systemctl daemon-reload`, unit, unit, unit, unit, unit, ifName, ifName, tag)
	if iranSrv != nil {
		_ = h.runOnServer(iranSrv, cleanCmd, 15*time.Second)
	}
	if forSrv != nil {
		_ = h.runOnServer(forSrv, cleanCmd, 15*time.Second)
	}
	_ = h.store.DeleteTunnel(id)
	return fmt.Sprintf("Tunnel %s (#%d) deleted from both servers.", tun.Name, id)
}

func (h *Handler) deployTunnelFromTG(token string, chatID int64, wiz *tgWizard, rawPorts string) {
	normPorts, portList, err := normalizePorts(rawPorts)
	if err != nil || len(portList) == 0 {
		_ = h.tgSendMessage(token, chatID, "❌ Invalid ports list. Try /menu -> New Tunnel again.", mainMenuKB())
		return
	}
	iranSrv, err1 := h.store.GetServerWithSecret(wiz.IranID)
	forSrv, err2 := h.store.GetServerWithSecret(wiz.ForID)
	if err1 != nil || err2 != nil {
		_ = h.tgSendMessage(token, chatID, "❌ Selected servers no longer exist.", mainMenuKB())
		return
	}
	_ = h.tgSendMessage(token, chatID, fmt.Sprintf("⏳ Deploying <b>%s</b> tunnel (%s ⇄ %s)...", strings.ToUpper(wiz.Engine), tgEsc(iranSrv.Name), tgEsc(forSrv.Name)), nil)

	tun := &db.Tunnel{
		Name:            fmt.Sprintf("TG-%s-%d", strings.ToUpper(wiz.Engine), time.Now().Unix()%1000),
		Engine:          wiz.Engine,
		Transport:       "tcp",
		IranServerID:    wiz.IranID,
		ForeignServerID: wiz.ForID,
		TunnelPort:      3080 + int(time.Now().Unix()%400),
		Token:           randTunnelToken(),
		Ports:           normPorts,
		Forwarder:       "iptables",
		LBMode:          "none",
		Status:          "deploying",
	}
	id, err := h.store.AddTunnel(tun)
	if err != nil {
		_ = h.tgSendMessage(token, chatID, "❌ DB error: <code>"+tgEsc(err.Error())+"</code>", mainMenuKB())
		return
	}
	tun.ID = id
	tun.Subnet = fmt.Sprintf("10.10.%d", (id%240)+10)
	iranScript, forScript := buildTunnelDeployScripts(tun, iranSrv, forSrv, portList)

	rIran := h.runOnServer(iranSrv, iranScript, 35*time.Second)
	rFor := h.runOnServer(forSrv, forScript, 35*time.Second)
	if rIran.Err == nil && rFor.Err == nil {
		_ = h.store.UpdateTunnelStatus(id, "active", "")
		_ = h.tgSendMessage(token, chatID, fmt.Sprintf("✅ <b>Tunnel %s (#%d) Deployed!</b>\n• Engine: <code>%s</code>\n• Ports: <code>%s</code>",
			tgEsc(tun.Name), id, strings.ToUpper(tun.Engine), tgEsc(normPorts)), mainMenuKB())
	} else {
		errTxt := strings.TrimSpace(rIran.Output + "\n" + rFor.Output)
		_ = h.store.UpdateTunnelStatus(id, "error", errTxt)
		if len(errTxt) > 2000 {
			errTxt = errTxt[len(errTxt)-2000:]
		}
		_ = h.tgSendMessage(token, chatID, fmt.Sprintf("⚠️ <b>Tunnel #%d deployed with errors:</b>\n<pre>%s</pre>", id, tgEsc(errTxt)), mainMenuKB())
	}
}

func (h *Handler) handleTGBackupUpload(token string, chatID int64, fileID, fname string) {
	info, _, err := h.tgAPIJSON(token, "getFile", map[string]any{"file_id": fileID}, 15*time.Second)
	if err != nil {
		_ = h.tgSendMessage(token, chatID, "❌ Could not fetch file info: "+tgEsc(err.Error()), nil)
		return
	}
	resMap, _ := info["result"].(map[string]any)
	filePath, _ := resMap["file_path"].(string)
	if filePath == "" {
		return
	}
	dlURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", token, filePath)
	raw, _, err := h.tgHTTPDo(func() (*http.Request, error) {
		return http.NewRequest("GET", dlURL, nil)
	}, 45*time.Second)
	if err != nil || !bytes.HasPrefix(raw, []byte("PK")) {
		_ = h.tgSendMessage(token, chatID, "❌ Invalid ZIP backup file.", nil)
		return
	}
	// Quick validate archive contents
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		_ = h.tgSendMessage(token, chatID, "❌ Corrupt ZIP archive.", nil)
		return
	}
	hasDB, hasKey := false, false
	for _, f := range zr.File {
		if f.Name == "mcontrol.db" {
			hasDB = true
		}
		if f.Name == ".secret.key" {
			hasKey = true
		}
	}
	if !hasDB || !hasKey {
		_ = h.tgSendMessage(token, chatID, "❌ This ZIP does not contain both <code>mcontrol.db</code> and <code>.secret.key</code>.", nil)
		return
	}
	tmp, err := os.CreateTemp("", "mc-tg-restore-*.zip")
	if err != nil {
		return
	}
	_, _ = tmp.Write(raw)
	_ = tmp.Close()

	h.tg().mu.Lock()
	if old := h.tg().pendingRestore[chatID]; old != "" {
		_ = os.Remove(old)
	}
	h.tg().pendingRestore[chatID] = tmp.Name()
	h.tg().mu.Unlock()

	_ = h.tgSendMessage(token, chatID, fmt.Sprintf("📦 <b>Valid MControl Backup Detected</b> (<code>%s</code>, %.1f KB)\n\nConfirm to restore this database & encryption key and restart the panel:", tgEsc(fname), float64(len(raw))/1024.0), [][]map[string]string{
		{
			{"text": "⚠️ Confirm Restore & Restart", "callback_data": "mc:restore:confirm"},
			{"text": "✖ Cancel", "callback_data": "mc:menu"},
		},
	})
}

func (h *Handler) applyRestoreZipFromPath(zipPath string) error {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var dbData, keyData, certData, tlsKeyData []byte
	for _, f := range zr.File {
		switch f.Name {
		case "mcontrol.db":
			dbData, err = readZipEntry(f, 64<<20)
		case ".secret.key":
			keyData, err = readZipEntry(f, 4096)
		case "tls.crt":
			certData, err = readZipEntry(f, 65536)
		case "tls.key":
			tlsKeyData, err = readZipEntry(f, 65536)
		}
		if err != nil {
			return err
		}
	}
	if len(dbData) < 16 || !bytes.HasPrefix(dbData, []byte("SQLite format 3\x00")) {
		return fmt.Errorf("invalid SQLite DB in backup")
	}
	if len(keyData) < 32 {
		return fmt.Errorf("invalid .secret.key in backup")
	}
	dbPath := h.store.DBPath()
	tmpDB := dbPath + ".restore.tmp"
	if err := os.WriteFile(tmpDB, dbData, 0600); err != nil {
		return err
	}
	if len(certData) > 0 && len(tlsKeyData) > 0 {
		_ = os.WriteFile(filepath.Join(h.store.DataDir(), "tls.crt"), certData, 0600)
		_ = os.WriteFile(filepath.Join(h.store.DataDir(), "tls.key"), tlsKeyData, 0600)
	}
	if err := os.WriteFile(h.store.KeyPath(), keyData, 0600); err != nil {
		return err
	}
	return os.Rename(tmpDB, dbPath)
}

func (h *Handler) canSendAlert(key string) bool {
	h.tg().mu.Lock()
	defer h.tg().mu.Unlock()
	if last, ok := h.tg().alertCooldown[key]; ok && time.Since(last) < 15*time.Minute {
		return false
	}
	h.tg().alertCooldown[key] = time.Now()
	return true
}

func (h *Handler) StartTelegramBot() {
	if raw := h.store.GetSetting("telegram_last_backup_at"); raw != "" {
		if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
			h.tg().lastBackupAt = ts
		}
	}

	// 1. Long-polling worker
	go func() {
		var offset int64
		for {
			if h.isStopped() {
				return
			}
			cfg := h.loadTelegramCfg()
			if !cfg.Enabled || strings.TrimSpace(cfg.BotToken) == "" {
				h.tg().mu.Lock()
				h.tg().connected = false
				h.tg().botUsername = ""
				h.tg().mu.Unlock()
				select {
				case <-h.tg().reloadCh:
				case <-time.After(5 * time.Second):
				}
				continue
			}

			// Verify bot token via getMe if not connected yet
			h.tg().mu.Lock()
			isConn := h.tg().connected
			h.tg().mu.Unlock()
			if !isConn {
				res, viaOB, err := h.tgAPIJSON(cfg.BotToken, "getMe", nil, 10*time.Second)
				h.tg().mu.Lock()
				if err != nil {
					h.tg().connected = false
					h.tg().lastError = err.Error()
					h.tg().viaOutbound = viaOB
				} else {
					h.tg().connected = true
					h.tg().lastError = ""
					h.tg().viaOutbound = viaOB
					if r, ok := res["result"].(map[string]any); ok {
						h.tg().botUsername, _ = r["username"].(string)
					}
				}
				h.tg().mu.Unlock()
				if err != nil {
					select {
					case <-h.tg().reloadCh:
					case <-time.After(10 * time.Second):
					}
					continue
				}
			}

			// Poll updates
			res, viaOB, err := h.tgAPIJSON(cfg.BotToken, "getUpdates", map[string]any{
				"offset":  offset,
				"timeout": 25,
			}, 35*time.Second)
			if err != nil {
				h.tg().mu.Lock()
				h.tg().connected = false
				h.tg().lastError = err.Error()
				h.tg().viaOutbound = viaOB
				h.tg().mu.Unlock()
				select {
				case <-h.tg().reloadCh:
				case <-time.After(6 * time.Second):
				}
				continue
			}
			h.tg().mu.Lock()
			h.tg().connected = true
			h.tg().lastError = ""
			h.tg().viaOutbound = viaOB
			h.tg().mu.Unlock()

			admins := parseAdminIDs(cfg.AdminIDs)
			if list, ok := res["result"].([]any); ok {
				for _, item := range list {
					if upd, ok := item.(map[string]any); ok {
						if uid, ok := upd["update_id"].(float64); ok {
							if int64(uid) >= offset {
								offset = int64(uid) + 1
							}
						}
						go h.handleTGUpdate(cfg.BotToken, upd, admins)
					}
				}
			}
		}
	}()

	// 2. Scheduler: Auto-Backuper + Smart Alerts + Daily Digest
	go func() {
		iranTZ := time.FixedZone("IRST", 3*3600+30*60)
		for {
			time.Sleep(60 * time.Second)
			if h.isStopped() {
				return
			}
			cfg := h.loadTelegramCfg()
			if !cfg.Enabled || strings.TrimSpace(cfg.BotToken) == "" {
				continue
			}
			adminChat := firstTargetChat(cfg, false)
			if adminChat == 0 {
				continue
			}

			// 2a. Auto-Backuper
			if cfg.AutoBackupHours > 0 {
				h.tg().mu.Lock()
				lastB := h.tg().lastBackupAt
				h.tg().mu.Unlock()
				if time.Since(time.Unix(lastB, 0)) >= time.Duration(cfg.AutoBackupHours)*time.Hour {
					if err := h.sendTelegramBackup(0, fmt.Sprintf("Auto %dh", cfg.AutoBackupHours)); err != nil {
						log.Printf("[telegram] auto-backup failed: %v", err)
					}
				}
			}

			// 2b. Server Offline / Recovered + High Resource Alerts
			if cfg.AlertServers {
				srvs, _ := h.store.ListServers()
				for _, s := range srvs {
					h.tg().mu.Lock()
					prev := h.tg().srvPrevStatus[s.ID]
					h.tg().srvPrevStatus[s.ID] = s.Status
					h.tg().mu.Unlock()

					if prev == "online" && s.Status == "offline" && h.canSendAlert(fmt.Sprintf("srv-down-%d", s.ID)) {
						_ = h.tgSendMessage(cfg.BotToken, adminChat,
							fmt.Sprintf("🔴 <b>Server OFFLINE:</b> %s (<code>%s</code>)\nError: <code>%s</code>", tgEsc(s.Name), tgEsc(s.Host), tgEsc(s.LastError)),
							mainMenuKB())
					} else if prev == "offline" && s.Status == "online" && h.canSendAlert(fmt.Sprintf("srv-up-%d", s.ID)) {
						_ = h.tgSendMessage(cfg.BotToken, adminChat,
							fmt.Sprintf("🟢 <b>Server RECOVERED:</b> %s (<code>%s</code>) is back online!", tgEsc(s.Name), tgEsc(s.Host)),
							nil)
					}
				}
				// Master high resource alert (>90%)
				st := sysinfo.Collect()
				if (st.CPUPercent >= 90 || st.RAMPercent >= 90 || st.DiskPercent >= 90) && h.canSendAlert("master-res") {
					_ = h.tgSendMessage(cfg.BotToken, adminChat,
						fmt.Sprintf("⚠️ <b>Master High Resource Alert</b>\n• CPU: <b>%.1f%%</b> | RAM: <b>%.1f%%</b> | Disk: <b>%.1f%%</b>",
							st.CPUPercent, st.RAMPercent, st.DiskPercent), nil)
				}
			}

			// 2c. Tunnel Down Alerts with Actionable Restart & Logs buttons
			if cfg.AlertTunnels {
				tuns, _ := h.store.ListTunnels()
				for _, t := range tuns {
					if t.Status == "error" && h.canSendAlert(fmt.Sprintf("tun-err-%d", t.ID)) {
						kb := [][]map[string]string{{
							{"text": "🔄 Restart Tunnel", "callback_data": fmt.Sprintf("mc:tun:act:%d:restart", t.ID)},
							{"text": "📜 Logs", "callback_data": fmt.Sprintf("mc:tun:act:%d:logs", t.ID)},
						}}
						_ = h.tgSendMessage(cfg.BotToken, adminChat,
							fmt.Sprintf("🚨 <b>Tunnel Down:</b> %s (#%d)\n• Route: %s ⇄ %s\n• Engine: <code>%s</code>",
								tgEsc(t.Name), t.ID, tgEsc(t.IranName), tgEsc(t.ForeignName), strings.ToUpper(t.Engine)),
							kb)
					}
				}
			}

			// 2d. Daily Digest (Iran Standard Time UTC+3:30)
			if cfg.DailyDigest {
				nowIR := time.Now().In(iranTZ)
				today := nowIR.Format("2006-01-02")
				h.tg().mu.Lock()
				already := h.tg().lastDigestDate == today
				h.tg().mu.Unlock()
				if !already && nowIR.Hour() == cfg.DigestHour {
					h.tg().mu.Lock()
					h.tg().lastDigestDate = today
					h.tg().mu.Unlock()
					_ = h.tgSendMessage(cfg.BotToken, adminChat, "🌙 <b>Daily Summary Digest (IRST)</b>\n\n"+h.buildStatusText(), mainMenuKB())
				}
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// HTTP API Endpoints for the Telegram Panel Tab
// ---------------------------------------------------------------------------

func (h *Handler) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadTelegramCfg()
	h.tg().mu.Lock()
	resp := map[string]any{
		"ok":             true,
		"config":         cfg,
		"connected":      h.tg().connected,
		"bot_username":   h.tg().botUsername,
		"via_outbound":   h.tg().viaOutbound,
		"last_error":     h.tg().lastError,
		"last_backup_at": h.tg().lastBackupAt,
	}
	h.tg().mu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleTelegramSave(w http.ResponseWriter, r *http.Request) {
	var req telegramConfig
	if !readJSON(w, r, &req) {
		return
	}
	req.BotToken = strings.TrimSpace(req.BotToken)
	req.AdminIDs = strings.TrimSpace(req.AdminIDs)
	req.BackupChatID = strings.TrimSpace(req.BackupChatID)
	if req.DigestHour < 0 || req.DigestHour > 23 {
		req.DigestHour = 0
	}
	h.saveTelegramCfg(req)

	h.tg().mu.Lock()
	h.tg().connected = false
	h.tg().lastError = ""
	h.tg().mu.Unlock()
	select {
	case h.tg().reloadCh <- struct{}{}:
	default:
	}

	// Quick live check if enabled
	botUser := ""
	viaOB := false
	if req.Enabled && req.BotToken != "" {
		res, ob, err := h.tgAPIJSON(req.BotToken, "getMe", nil, 10*time.Second)
		viaOB = ob
		if err != nil {
			h.tg().mu.Lock()
			h.tg().lastError = err.Error()
			h.tg().mu.Unlock()
			fail(w, http.StatusBadGateway, "Saved, but Telegram connection failed: "+err.Error())
			return
		}
		if rm, ok := res["result"].(map[string]any); ok {
			botUser, _ = rm["username"].(string)
		}
		h.tg().mu.Lock()
		h.tg().connected = true
		h.tg().botUsername = botUser
		h.tg().viaOutbound = viaOB
		h.tg().mu.Unlock()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"connected":    req.Enabled && botUser != "",
		"bot_username": botUser,
		"via_outbound": viaOB,
		"message":      "Telegram Bot settings saved.",
	})
}

func (h *Handler) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadTelegramCfg()
	if strings.TrimSpace(cfg.BotToken) == "" {
		fail(w, http.StatusBadRequest, "Bot Token is empty. Save settings first.")
		return
	}
	chatID := firstTargetChat(cfg, false)
	if chatID == 0 {
		fail(w, http.StatusBadRequest, "Admin Chat ID is empty.")
		return
	}
	if err := h.tgSendMessage(cfg.BotToken, chatID, "✅ <b>MControl Telegram Bot Connected!</b>\nSend /menu anytime to manage servers, tunnels, and backups.", mainMenuKB()); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Test message sent to Telegram!"})
}

func (h *Handler) handleTelegramBackupNow(w http.ResponseWriter, r *http.Request) {
	if err := h.sendTelegramBackup(0, "Panel Manual"); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Encrypted backup ZIP sent to Telegram!"})
}

