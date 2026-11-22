package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/sysinfo"
)

type AuditEntry struct {
	Time   int64  `json:"time"`
	User   string `json:"user"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

var auditMu sync.Mutex

func (h *Handler) getSettingDef(key, defVal string) string {
	if v := h.getSettingDef(key); v != "" {
		return v
	}
	return defVal
}

func (h *Handler) logAudit(user, method, path, detail string) {
	auditMu.Lock()
	defer auditMu.Unlock()
	raw := h.getSettingDef("audit_log_json", "[]")
	var list []AuditEntry
	_ = json.Unmarshal([]byte(raw), &list)
	list = append([]AuditEntry{{
		Time:   time.Now().Unix(),
		User:   user,
		Method: method,
		Path:   path,
		Detail: detail,
	}}, list...)
	if len(list) > 120 {
		list = list[:120]
	}
	if b, err := json.Marshal(list); err == nil {
		_ = h.store.SetSetting("audit_log_json", string(b))
	}
}

func (h *Handler) handleAuditList(w http.ResponseWriter, r *http.Request) {
	raw := h.getSettingDef("audit_log_json", "[]")
	var list []AuditEntry
	_ = json.Unmarshal([]byte(raw), &list)
	if list == nil {
		list = []AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": list})
}

type viewerConfig struct {
	Enabled  bool   `json:"enabled"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

func (h *Handler) loadViewerCfg() viewerConfig {
	enc := h.getSettingDef("viewer_auth_enc", "")
	if enc == "" {
		return viewerConfig{}
	}
	plain, err := h.store.Decrypt(enc)
	if err != nil {
		return viewerConfig{}
	}
	var c viewerConfig
	_ = json.Unmarshal([]byte(plain), &c)
	return c
}

func (h *Handler) verifyViewerLogin(user, pass string) bool {
	c := h.loadViewerCfg()
	if !c.Enabled || c.Username == "" || c.Password == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(user), strings.TrimSpace(c.Username)) && pass == c.Password
}

func (h *Handler) handleViewerGet(w http.ResponseWriter, r *http.Request) {
	c := h.loadViewerCfg()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"enabled":  c.Enabled,
		"username": c.Username,
		"has_pass": c.Password != "",
	})
}

func (h *Handler) handleViewerSave(w http.ResponseWriter, r *http.Request) {
	var req viewerConfig
	if !readJSON(w, r, &req) {
		return
	}
	cur := h.loadViewerCfg()
	cur.Enabled = req.Enabled
	if strings.TrimSpace(req.Username) != "" {
		cur.Username = strings.TrimSpace(req.Username)
	}
	if req.Password != "" {
		cur.Password = req.Password
	}
	if cur.Enabled && (cur.Username == "" || cur.Password == "") {
		fail(w, http.StatusBadRequest, "viewer username and password are required when enabled")
		return
	}
	b, _ := json.Marshal(cur)
	enc, err := h.store.Encrypt(string(b))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = h.store.SetSetting("viewer_auth_enc", enc)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Viewer account updated."})
}

func parseOpsPorts(csv string) []int {
	seen := map[int]bool{}
	var out []int
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n >= 1 && n <= 65535 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func (h *Handler) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	var req struct {
		Name       string `json:"name"`
		Transport  string `json:"transport"`
		TunnelPort int    `json:"tunnel_port"`
		Token      string `json:"token"`
		Ports      string `json:"ports"`
		LBMode     string `json:"lb_mode"`
		VIPTargets string `json:"vip_targets"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		if !safeLegacyName.MatchString(strings.TrimSpace(req.Name)) {
			fail(w, http.StatusBadRequest, "invalid tunnel name")
			return
		}
		tun.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Token) != "" {
		if !safeLegacyName.MatchString(strings.TrimSpace(req.Token)) {
			fail(w, http.StatusBadRequest, "invalid tunnel token")
			return
		}
		tun.Token = strings.TrimSpace(req.Token)
	}
	if req.TunnelPort > 0 && req.TunnelPort <= 65535 {
		tun.TunnelPort = req.TunnelPort
	}
	if strings.TrimSpace(req.Transport) != "" {
		tun.Transport = strings.TrimSpace(req.Transport)
	}
	if strings.TrimSpace(req.Ports) != "" {
		tun.Ports = strings.TrimSpace(req.Ports)
	}
	if req.LBMode != "" {
		tun.LBMode = req.LBMode
	}
	tun.VIPTargets = strings.TrimSpace(req.VIPTargets)
	tun.Status = "deploying"

	ports := parseOpsPorts(tun.Ports)
	if len(ports) == 0 {
		ports = []int{443}
	}
	iranSrv, err := h.store.GetServerWithSecret(tun.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "entry server not found")
		return
	}
	forSrv, err := h.store.GetServerWithSecret(tun.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "exit server not found")
		return
	}

	if err := h.store.UpdateTunnelFull(tun); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	iranCmd, forCmd := buildTunnelDeployScripts(tun, iranSrv, forSrv, ports)
	var wg sync.WaitGroup
	var rIran, rFor sshclient.Result
	wg.Add(2)
	go func() { defer wg.Done(); rIran = h.runOnServer(iranSrv, iranCmd, 45*time.Second) }()
	go func() { defer wg.Done(); rFor = h.runOnServer(forSrv, forCmd, 45*time.Second) }()
	wg.Wait()

	if rIran.Err != nil || rFor.Err != nil {
		errMsg := ""
		if rIran.Err != nil {
			errMsg += "Iran: " + rIran.Err.Error() + " "
		}
		if rFor.Err != nil {
			errMsg += "Foreign: " + rFor.Err.Error()
		}
		_ = h.store.UpdateTunnelStatus(id, "error", strings.TrimSpace(errMsg))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": strings.TrimSpace(errMsg), "output": rIran.Output + "\n" + rFor.Output})
		return
	}
	_ = h.store.UpdateTunnelStatus(id, "active", "")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "Tunnel updated & redeployed on both servers!",
		"output":  "=== IRAN ===\n" + rIran.Output + "\n=== FOREIGN ===\n" + rFor.Output,
	})
}

func (h *Handler) handleTunnelMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iranSrv, _ := h.store.GetServerWithSecret(tun.IranServerID)
	forSrv, _ := h.store.GetServerWithSecret(tun.ForeignServerID)
	unit := fmt.Sprintf("mcontrol-tun-%d.service", id)
	targetHost := ""
	if forSrv != nil {
		targetHost = forSrv.Host
		if (tun.Engine == "gre" || tun.Engine == "ip6gre" || tun.Engine == "vxlan") && tun.Subnet != "" {
			targetHost = tun.Subnet + ".2"
		}
	}
	iranCmd := fmt.Sprintf(`ST=$(systemctl is-active %s 2>/dev/null || echo inactive)
PING=$(ping -c 2 -W 1 %s 2>/dev/null | awk -F'/' 'END{print ($5==""?"N/A":$5" ms")}')
CONNS=$(ss -Htan 2>/dev/null | grep -c ":%d " || echo 0)
echo "STATUS=$ST"
echo "PING=$PING"
echo "CONNS=$CONNS"`, unit, sshclient.ShQuote(targetHost), tun.TunnelPort)

	forCmd := fmt.Sprintf(`ST=$(systemctl is-active %s 2>/dev/null || echo inactive)
echo "STATUS=$ST"`, unit)

	var wg sync.WaitGroup
	var rI, rF sshclient.Result
	if iranSrv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); rI = h.runOnServer(iranSrv, iranCmd, 15*time.Second) }()
	}
	if forSrv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); rF = h.runOnServer(forSrv, forCmd, 15*time.Second) }()
	}
	wg.Wait()

	kvI := parseKV(rI.Output)
	kvF := parseKV(rF.Output)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"iran_status":    kvI["STATUS"],
		"foreign_status": kvF["STATUS"],
		"ping":           kvI["PING"],
		"conns":          kvI["CONNS"],
	})
}

func (h *Handler) handleServerExec(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" {
		fail(w, http.StatusBadRequest, "command is required")
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	res := h.runOnServer(srv, cmd, 35*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     res.Err == nil,
		"output": res.Output,
		"error":  func() string { if res.Err != nil { return res.Err.Error() }; return "" }(),
	})
}

func (h *Handler) handleFleetExec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerIDs []int64 `json:"server_ids"`
		Command   string  `json:"command"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" || len(req.ServerIDs) == 0 {
		fail(w, http.StatusBadRequest, "select servers and enter a command")
		return
	}
	type itemRes struct {
		ServerID   int64  `json:"server_id"`
		ServerName string `json:"server_name"`
		OK         bool   `json:"ok"`
		Output     string `json:"output"`
		Error      string `json:"error,omitempty"`
	}
	results := make([]itemRes, len(req.ServerIDs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for idx, sid := range req.ServerIDs {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			srv, err := h.store.GetServerWithSecret(id)
			if err != nil {
				results[i] = itemRes{ServerID: id, OK: false, Error: "server not found"}
				return
			}
			res := h.runOnServer(srv, cmd, 45*time.Second)
			results[i] = itemRes{
				ServerID:   id,
				ServerName: srv.Name,
				OK:         res.Err == nil,
				Output:     res.Output,
				Error:      func() string { if res.Err != nil { return res.Err.Error() }; return "" }(),
			}
		}(idx, sid)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": results})
}

func (h *Handler) handleServerPorts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	cmd := `echo "=== LISTENING PORTS ==="
ss -tulpn 2>/dev/null | head -n 45 || netstat -tulpn 2>/dev/null | head -n 45
echo ""
echo "=== FIREWALL STATUS ==="
if command -v ufw >/dev/null 2>&1; then ufw status numbered 2>/dev/null | head -n 30; else echo "UFW not installed (iptables active)"; fi`
	res := h.runOnServer(srv, cmd, 20*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": res.Output})
}

func (h *Handler) handleServerFirewall(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Port   int    `json:"port"`
		Proto  string `json:"proto"`
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		fail(w, http.StatusBadRequest, "invalid port (1-65535)")
		return
	}
	if req.Proto != "tcp" && req.Proto != "udp" {
		req.Proto = "tcp"
	}
	if req.Action != "allow" && req.Action != "deny" {
		fail(w, http.StatusBadRequest, "action must be allow or deny")
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	var cmd string
	if req.Action == "allow" {
		cmd = fmt.Sprintf(`if command -v ufw >/dev/null 2>&1 && ufw status | grep -qi active; then ufw allow %d/%s; else iptables -C INPUT -p %s --dport %d -j ACCEPT 2>/dev/null || iptables -I INPUT -p %s --dport %d -j ACCEPT; fi; echo "Port %d/%s allowed."`, req.Port, req.Proto, req.Proto, req.Port, req.Proto, req.Port, req.Port, req.Proto)
	} else {
		cmd = fmt.Sprintf(`if command -v ufw >/dev/null 2>&1 && ufw status | grep -qi active; then ufw deny %d/%s; else iptables -C INPUT -p %s --dport %d -j DROP 2>/dev/null || iptables -I INPUT -p %s --dport %d -j DROP; fi; echo "Port %d/%s blocked."`, req.Port, req.Proto, req.Proto, req.Port, req.Proto, req.Port, req.Port, req.Proto)
	}
	res := h.runOnServer(srv, cmd, 20*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": strings.TrimSpace(res.Output)})
}

var safeUnitRe = regexp.MustCompile(`^[A-Za-z0-9@._-]{1,64}$`)

func (h *Handler) handleServerServices(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	key := fmt.Sprintf("srv_services_%d", id)
	if r.Method == http.MethodPost {
		var req struct {
			Services []string `json:"services"`
			Action   string   `json:"action"`
			Unit     string   `json:"unit"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if req.Action == "restart" || req.Action == "start" || req.Action == "stop" {
			if !safeUnitRe.MatchString(req.Unit) {
				fail(w, http.StatusBadRequest, "invalid service name")
				return
			}
			res := h.runOnServer(srv, fmt.Sprintf("systemctl %s %s 2>&1; systemctl is-active %s 2>&1", req.Action, req.Unit, req.Unit), 20*time.Second)
			writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": strings.TrimSpace(res.Output)})
			return
		}
		var clean []string
		for _, s := range req.Services {
			s = strings.TrimSpace(s)
			if safeUnitRe.MatchString(s) {
				clean = append(clean, s)
			}
		}
		b, _ := json.Marshal(clean)
		_ = h.store.SetSetting(key, string(b))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": clean})
		return
	}

	raw := h.getSettingDef(key, `["x-ui","xray","nginx"]`)
	var units []string
	_ = json.Unmarshal([]byte(raw), &units)
	if len(units) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "statuses": []any{}})
		return
	}
	var sb strings.Builder
	for _, u := range units {
		if safeUnitRe.MatchString(u) {
			sb.WriteString(fmt.Sprintf(`echo "%s=$(systemctl is-active %s 2>/dev/null || echo inactive)"`+"\n", u, u))
		}
	}
	res := h.runOnServer(srv, sb.String(), 15*time.Second)
	kv := parseKV(res.Output)
	type svcSt struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	out := make([]svcSt, 0, len(units))
	for _, u := range units {
		st := kv[u]
		if st == "" {
			st = "unknown"
		}
		out = append(out, svcSt{Name: u, Status: st})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "statuses": out})
}

type SchedJob struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TargetID    int64  `json:"target_id"`
	Task        string `json:"task"`
	Command     string `json:"command,omitempty"`
	IntervalMin int    `json:"interval_min"`
	Enabled     bool   `json:"enabled"`
	LastRun     int64  `json:"last_run"`
}

type FailoverRule struct {
	ID              int64  `json:"id"`
	PrimaryServerID int64  `json:"primary_server_id"`
	BackupServerID  int64  `json:"backup_server_id"`
	ZoneID          string `json:"zone_id"`
	RecordID        string `json:"record_id"`
	RecordName      string `json:"record_name"`
	RecordType      string `json:"record_type"`
	Enabled         bool   `json:"enabled"`
	ActiveOnBackup  bool   `json:"active_on_backup"`
}

type OpsAutomationCfg struct {
	AlertEnabled  bool           `json:"alert_enabled"`
	CPUThreshold  int            `json:"cpu_threshold"`
	RAMThreshold  int            `json:"ram_threshold"`
	FailoverRules []FailoverRule `json:"failover_rules"`
}

var opsMu sync.Mutex

func (h *Handler) loadSchedJobs() []SchedJob {
	raw := h.getSettingDef("sched_jobs_json", "[]")
	var list []SchedJob
	_ = json.Unmarshal([]byte(raw), &list)
	if list == nil {
		list = []SchedJob{}
	}
	return list
}

func (h *Handler) saveSchedJobs(list []SchedJob) {
	b, _ := json.Marshal(list)
	_ = h.store.SetSetting("sched_jobs_json", string(b))
}

func (h *Handler) handleScheduler(w http.ResponseWriter, r *http.Request) {
	opsMu.Lock()
	defer opsMu.Unlock()
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": h.loadSchedJobs()})
		return
	}
	if r.Method == http.MethodDelete {
		idStr := r.URL.Query().Get("id")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		list := h.loadSchedJobs()
		var kept []SchedJob
		for _, j := range list {
			if j.ID != id {
				kept = append(kept, j)
			}
		}
		h.saveSchedJobs(kept)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": kept})
		return
	}
	var req SchedJob
	if !readJSON(w, r, &req) {
		return
	}
	if req.IntervalMin < 5 {
		req.IntervalMin = 60
	}
	if req.Name == "" {
		req.Name = req.Task
	}
	req.ID = time.Now().UnixNano() / 1e6
	req.Enabled = true
	list := append(h.loadSchedJobs(), req)
	h.saveSchedJobs(list)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": list})
}

func (h *Handler) loadOpsAuto() OpsAutomationCfg {
	raw := h.getSettingDef("ops_auto_json", "")
	cfg := OpsAutomationCfg{CPUThreshold: 90, RAMThreshold: 90, FailoverRules: []FailoverRule{}}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.CPUThreshold <= 0 {
		cfg.CPUThreshold = 90
	}
	if cfg.RAMThreshold <= 0 {
		cfg.RAMThreshold = 90
	}
	if cfg.FailoverRules == nil {
		cfg.FailoverRules = []FailoverRule{}
	}
	return cfg
}

func (h *Handler) saveOpsAuto(cfg OpsAutomationCfg) {
	b, _ := json.Marshal(cfg)
	_ = h.store.SetSetting("ops_auto_json", string(b))
}

func (h *Handler) handleOpsAutomation(w http.ResponseWriter, r *http.Request) {
	opsMu.Lock()
	defer opsMu.Unlock()
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": h.loadOpsAuto()})
		return
	}
	var req OpsAutomationCfg
	if !readJSON(w, r, &req) {
		return
	}
	h.saveOpsAuto(req)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": req, "message": "Automation settings saved."})
}

func (h *Handler) sendOpsAlert(msg string) {
	tg := h.loadTelegramCfg()
	if !tg.Enabled || strings.TrimSpace(tg.BotToken) == "" || strings.TrimSpace(tg.AdminIDs) == "" {
		return
	}
	for _, rawID := range strings.Split(tg.AdminIDs, ",") {
		cid, err := strconv.ParseInt(strings.TrimSpace(rawID), 10, 64)
		if err == nil && cid != 0 {
			_ = h.tgSendMessage(tg.BotToken, cid, msg, nil)
		}
	}
}

func (h *Handler) switchDNSRecordIP(zoneID, recID, recType, recName, newIP string) error {
	cfg := h.loadCFConfig()
	if strings.HasPrefix(zoneID, "arvan:") {
		domain := strings.TrimPrefix(zoneID, "arvan:")
		sub := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(recName), domain), ".")
		if sub == "" {
			sub = "@"
		}
		u := fmt.Sprintf("https://napi.arvancloud.ir/cdn/4.0/domains/%s/dns-records/%s", url.PathEscape(domain), url.PathEscape(recID))
		_, code, err := h.arvanHTTPDo("PUT", u, cfg.ArvanAPIKey, buildArvanPayload(recType, sub, newIP, 120, false))
		if err != nil {
			return err
		}
		if code < 200 || code >= 300 {
			return fmt.Errorf("ArvanCloud HTTP %d", code)
		}
		return nil
	}
	u := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", url.PathEscape(zoneID), url.PathEscape(recID))
	raw, err := h.cfHTTPDo("PUT", u, cfg, map[string]any{
		"type": recType, "name": recName, "content": newIP, "ttl": 1, "proxied": false,
	})
	if err != nil {
		return err
	}
	if ok, msg := parseCFError(raw); !ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (h *Handler) StartOpsDaemon() {
	go func() {
		lastAlert := map[string]time.Time{}
		for {
			time.Sleep(60 * time.Second)
			now := time.Now()

			opsMu.Lock()
			jobs := h.loadSchedJobs()
			changed := false
			for i := range jobs {
				j := &jobs[i]
				if !j.Enabled || j.IntervalMin < 5 {
					continue
				}
				if now.Unix()-j.LastRun < int64(j.IntervalMin*60) {
					continue
				}
				j.LastRun = now.Unix()
				changed = true
				var cmd string
				switch j.Task {
				case "clean_ram":
					cmd = "sync; echo 3 > /proc/sys/vm/drop_caches 2>/dev/null || true"
				case "reboot":
					cmd = "nohup sh -c 'sleep 2 && reboot' >/dev/null 2>&1 &"
				case "restart_tunnels":
					cmd = "systemctl restart 'mcontrol-tun-*.service' 2>/dev/null || true"
				default:
					cmd = j.Command
				}
				if cmd == "" {
					continue
				}
				if j.TargetID == 0 {
					go func(c string) { _ = exec.Command("sh", "-c", c).Run() }(cmd)
				} else if srv, err := h.store.GetServerWithSecret(j.TargetID); err == nil {
					go func(s *db.Server, c string) { _ = h.runOnServer(s, c, 30*time.Second) }(srv, cmd)
				}
			}
			if changed {
				h.saveSchedJobs(jobs)
			}

			auto := h.loadOpsAuto()
			opsMu.Unlock()

			if auto.AlertEnabled {
				st := sysinfo.Collect()
				if int(st.CPUPercent) >= auto.CPUThreshold && time.Since(lastAlert["master_cpu"]) > 30*time.Minute {
					lastAlert["master_cpu"] = now
					h.sendOpsAlert(fmt.Sprintf("⚠️ <b>High CPU Alert (Master)</b>\nCPU usage reached <b>%.1f%%</b> (threshold: %d%%).", st.CPUPercent, auto.CPUThreshold))
				}
				if int(st.RAMPercent) >= auto.RAMThreshold && time.Since(lastAlert["master_ram"]) > 30*time.Minute {
					lastAlert["master_ram"] = now
					h.sendOpsAlert(fmt.Sprintf("⚠️ <b>High RAM Alert (Master)</b>\nRAM usage reached <b>%.1f%%</b> (threshold: %d%%).", st.RAMPercent, auto.RAMThreshold))
				}
			}

			if len(auto.FailoverRules) > 0 {
				srvs, _ := h.store.ListServers()
				srvMap := map[int64]db.Server{}
				for _, s := range srvs {
					srvMap[s.ID] = s
				}
				foChanged := false
				for i := range auto.FailoverRules {
					rule := &auto.FailoverRules[i]
					if !rule.Enabled {
						continue
					}
					pri, ok1 := srvMap[rule.PrimaryServerID]
					bak, ok2 := srvMap[rule.BackupServerID]
					if !ok1 || !ok2 {
						continue
					}
					if pri.Status == "offline" && !rule.ActiveOnBackup && bak.Status == "online" {
						newIP := bak.Host
						if rule.RecordType == "AAAA" && bak.IPv6 != "" && bak.IPv6 != "N/A" {
							newIP = bak.IPv6
						}
						if err := h.switchDNSRecordIP(rule.ZoneID, rule.RecordID, rule.RecordType, rule.RecordName, newIP); err == nil {
							rule.ActiveOnBackup = true
							foChanged = true
							h.sendOpsAlert(fmt.Sprintf("🔄 <b>DNS Auto-Failover Triggered</b>\nPrimary <b>%s</b> went offline.\nSwitched <code>%s</code> (%s) ➔ <code>%s</code> (%s).", pri.Name, rule.RecordName, rule.RecordType, newIP, bak.Name))
						}
					} else if pri.Status == "online" && rule.ActiveOnBackup {
						newIP := pri.Host
						if rule.RecordType == "AAAA" && pri.IPv6 != "" && pri.IPv6 != "N/A" {
							newIP = pri.IPv6
						}
						if err := h.switchDNSRecordIP(rule.ZoneID, rule.RecordID, rule.RecordType, rule.RecordName, newIP); err == nil {
							rule.ActiveOnBackup = false
							foChanged = true
							h.sendOpsAlert(fmt.Sprintf("✅ <b>DNS Failback Restored</b>\nPrimary <b>%s</b> is back online.\nRestored <code>%s</code> (%s) ➔ <code>%s</code>.", pri.Name, rule.RecordName, rule.RecordType, newIP))
						}
					}
				}
				if foChanged {
					opsMu.Lock()
					h.saveOpsAuto(auto)
					opsMu.Unlock()
				}
			}
		}
	}()
}
