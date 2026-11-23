package httpapi

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/geo"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/storage"
)

func toSSHTarget(s *storage.Server) sshclient.Target {
	at := s.AuthType
	if at == "" {
		at = "password"
	}
	return sshclient.Target{
		Host:     s.Host,
		Port:     s.Port,
		User:     s.Username,
		Secret:   s.Secret,
		AuthType: at,
	}
}

func (s *Server) probeOneServer(ctx context.Context, tenantID, serverID int64) (*storage.Server, error) {
	srv, err := s.engine.GetServerWithSecret(ctx, tenantID, serverID)
	if err != nil {
		return nil, err
	}
	pr := sshclient.Probe(toSSHTarget(srv))
	ping := sshclient.MeasurePingFromMaster(srv.Host, srv.Port)
	status := "offline"
	if pr.Online {
		status = "online"
	}
	ipv4 := pr.IPv4
	if ipv4 == "" {
		ipv4 = srv.Host
	}
	ipv6 := pr.IPv6
	if ipv6 == "" {
		ipv6 = "N/A"
	}
	_ = s.engine.UpdateServerRuntime(ctx, tenantID, serverID, status, pr.CPU, pr.RAM, pr.Uptime, ipv4, ipv6, ping, pr.Error)
	srv.Status = status
	srv.CPU = pr.CPU
	srv.RAM = pr.RAM
	srv.Uptime = pr.Uptime
	srv.IPv4 = ipv4
	srv.IPv6 = ipv6
	srv.PingMs = ping
	srv.LastError = pr.Error
	srv.Secret = ""
	return srv, nil
}

func (s *Server) handleTenantUpdateServer(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req storage.Server
	if !readJSON(w, r, &req) {
		return
	}
	req.ID = id
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Host) == "" {
		fail(w, http.StatusBadRequest, "name and host are required")
		return
	}
	if req.Role != "iran" && req.Role != "foreign" {
		req.Role = "foreign"
	}
	if strings.TrimSpace(req.Country) == "" {
		gi := geo.Lookup(req.Host)
		req.Country = gi.Country
		req.Datacenter = gi.Datacenter
	}
	if err := s.engine.UpdateServer(r.Context(), claims.UserID, &req); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTenantProbeServer(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := s.probeOneServer(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server": srv})
}

func (s *Server) handleTenantProbeAll(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	list, err := s.engine.ListServers(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, item := range list {
		wg.Add(1)
		go func(sid int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, _ = s.probeOneServer(r.Context(), claims.UserID, sid)
		}(item.ID)
	}
	wg.Wait()
	updated, _ := s.engine.ListServers(r.Context(), claims.UserID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": updated})
}

func (s *Server) handleTenantServerSpeedtest(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	sr := sshclient.RunSpeedtest(toSSHTarget(srv))
	if sr.Error != "" {
		fail(w, http.StatusBadGateway, sr.Error)
		return
	}
	summary := fmt.Sprintf("↓ %s Mbps · ↑ %s Mbps · %s ms", sr.DownloadMB, sr.UploadMB, sr.PingMs)
	_ = s.engine.UpdateServerSpeed(r.Context(), claims.UserID, id, summary)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "speed": summary, "result": sr})
}

func (s *Server) handleTenantServerTweak(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}

	var cmd string
	switch req.Action {
	case "status":
		cmd = `V6=$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null || echo 1)
CC=$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo cubic)
FW="0"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi "Status: active"; then FW="1"; fi
if systemctl is-active --quiet firewalld 2>/dev/null; then FW="1"; fi
echo "IPV6=$([ "$V6" = "0" ] && echo 1 || echo 0)"
echo "BBR=$([ "$CC" = "bbr" ] && echo 1 || echo 0)"
echo "FW=$FW"`
	case "ipv6_on":
		cmd = `sysctl -w net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0 net.ipv6.conf.lo.disable_ipv6=0 >/dev/null && echo "IPv6 enabled."`
	case "ipv6_off":
		cmd = `sysctl -w net.ipv6.conf.all.disable_ipv6=1 net.ipv6.conf.default.disable_ipv6=1 net.ipv6.conf.lo.disable_ipv6=1 >/dev/null && echo "IPv6 disabled."`
	case "bbr_on":
		cmd = `sysctl -w net.core.default_qdisc=fq net.ipv4.tcp_congestion_control=bbr >/dev/null && (grep -q "tcp_congestion_control=bbr" /etc/sysctl.conf || printf "\nnet.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n" >> /etc/sysctl.conf) && echo "BBR enabled."`
	case "bbr_off":
		cmd = `sysctl -w net.ipv4.tcp_congestion_control=cubic >/dev/null && echo "BBR disabled (cubic)."`
	case "fw_on":
		cmd = `if command -v ufw >/dev/null 2>&1; then ufw allow 22/tcp >/dev/null 2>&1 || true; ufw --force enable; else echo "UFW not installed."; fi`
	case "fw_off":
		cmd = `ufw disable 2>/dev/null || true; systemctl stop firewalld 2>/dev/null || true; iptables -P INPUT ACCEPT 2>/dev/null || true; iptables -F 2>/dev/null || true; echo "Firewall disabled & flushed."`
	case "net_opt":
		cmd = `cat <<'EOF' > /etc/sysctl.d/99-mcontrol-opt.conf
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.tcp_fastopen=3
net.core.somaxconn=65535
net.ipv4.tcp_max_syn_backlog=65535
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.ip_forward=1
EOF
sysctl --system >/dev/null 2>&1 || sysctl -p /etc/sysctl.d/99-mcontrol-opt.conf; echo "Kernel sysctl optimized."`
	case "clear_cache":
		cmd = `sync; echo 3 > /proc/sys/vm/drop_caches 2>/dev/null || true; journalctl --vacuum-time=2d 2>/dev/null || true; echo "RAM cache & logs cleaned."`
	case "reboot":
		cmd = `(sleep 1 && reboot) >/dev/null 2>&1 & echo "Reboot signal sent."`
	default:
		fail(w, http.StatusBadRequest, "unknown tweak action")
		return
	}

	res := sshclient.Run(toSSHTarget(srv), cmd, 10*time.Second, 20*time.Second)
	if res.Err != nil && !sshclient.IsExitError(res.Err) {
		fail(w, http.StatusBadGateway, res.Err.Error())
		return
	}
	if req.Action == "status" {
		state := map[string]bool{"ipv6": false, "bbr": false, "fw": false}
		for _, ln := range strings.Split(res.Output, "\n") {
			kv := strings.SplitN(strings.TrimSpace(ln), "=", 2)
			if len(kv) == 2 {
				switch kv[0] {
				case "IPV6":
					state["ipv6"] = kv[1] == "1"
				case "BBR":
					state["bbr"] = kv[1] == "1"
				case "FW":
					state["fw"] = kv[1] == "1"
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": state})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(res.Output)})
}

// ── Multi-Line Web Terminal Exec ─────────────────────────────────────────────

func (s *Server) handleTenantServerExec(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cmd := strings.TrimSpace(strings.ReplaceAll(req.Command, "\r\n", "\n"))
	if cmd == "" || len(cmd) > 32000 {
		fail(w, http.StatusBadRequest, "command is empty or exceeds 32KB limit")
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	cwd := strings.TrimSpace(req.Cwd)
	if cwd == "" {
		cwd = "/root"
	}
	const marker = "__MCONTROL_CWD__="
	wrapped := fmt.Sprintf("cd %s 2>/dev/null || cd /root
%s
__mc_rc=$?
printf '\n%s%%s\n' "$PWD"
exit $__mc_rc",
		sshclient.ShQuote(cwd), cmd, marker)

	res := sshclient.Run(toSSHTarget(srv), wrapped, 10*time.Second, 60*time.Second)
	exitStr := ""
	if res.Err != nil {
		if !sshclient.IsExitError(res.Err) {
			fail(w, http.StatusBadGateway, res.Err.Error())
			return
		}
		exitStr = res.Err.Error()
	}
	out := res.Output
	newCwd := cwd
	if idx := strings.LastIndex(out, marker); idx >= 0 {
		tail := strings.TrimSpace(out[idx+len(marker):])
		if nl := strings.IndexByte(tail, '
'); nl >= 0 {
			tail = strings.TrimSpace(tail[:nl])
		}
		if tail != "" {
			newCwd = tail
		}
		out = strings.TrimRight(out[:idx], "
")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"output": out,
		"cwd":    newCwd,
		"exit":   exitStr,
	})
}

// ── SFTP Web File Manager (List / Read / Write / Upload / Download / Delete) ─

func (s *Server) handleTenantServerFiles(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action  string `json:"action"` // list | read | write | upload | download | delete | mkdir
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	p := strings.TrimSpace(req.Path)
	if p == "" {
		p = "/root"
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	tgt := toSSHTarget(srv)

	switch req.Action {
	case "read":
		cmd := fmt.Sprintf(`head -c 250000 %s 2>&1`, sshclient.ShQuote(p))
		res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "content": res.Output, "path": p})
	case "write":
		b64 := base64.StdEncoding.EncodeToString([]byte(req.Content))
		cmd := fmt.Sprintf(`printf '%%s' %s | base64 -d > %s && echo "Saved %s"`, sshclient.ShQuote(b64), sshclient.ShQuote(p), p)
		res := sshclient.Run(tgt, cmd, 10*time.Second, 20*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "upload":
		if len(req.Content) > 12*1024*1024 {
			fail(w, http.StatusRequestEntityTooLarge, "file exceeds ~8MB limit")
			return
		}
		cmd := fmt.Sprintf(`printf '%%s' %s | base64 -d > %s && echo "Uploaded %s"`, sshclient.ShQuote(req.Content), sshclient.ShQuote(p), filepath.Base(p))
		res := sshclient.Run(tgt, cmd, 10*time.Second, 45*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "download":
		cmd := fmt.Sprintf(`base64 -w0 %s 2>/dev/null || base64 %s | tr -d '\n'`, sshclient.ShQuote(p), sshclient.ShQuote(p))
		res := sshclient.Run(tgt, cmd, 10*time.Second, 30*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "b64": strings.TrimSpace(res.Output), "filename": filepath.Base(p)})
	case "mkdir":
		cmd := fmt.Sprintf(`mkdir -p %s && echo "Folder created"`, sshclient.ShQuote(p))
		res := sshclient.Run(tgt, cmd, 10*time.Second, 10*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "delete":
		if p == "/" || p == "/etc" || p == "/root" || p == "/usr" || p == "/var" || p == "/bin" {
			fail(w, http.StatusBadRequest, "refusing to delete protected system path")
			return
		}
		cmd := fmt.Sprintf(`rm -rf %s 2>&1 && echo "Deleted"`, sshclient.ShQuote(p))
		res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	default: // list
		cmd := fmt.Sprintf(`ls -la --group-directories-first %s 2>&1 | awk 'NR>1{t="file"; if(substr($1,1,1)=="d") t="dir"; name=$9; for(i=10;i<=NF;i++) name=name" "$i; if(name!="." && name!="..") print t"\t"$5"\t"name}' | head -n 150`, sshclient.ShQuote(p))
		res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
		type entry struct {
			Type string `json:"type"`
			Size string `json:"size"`
			Name string `json:"name"`
		}
		items := []entry{}
		for _, ln := range strings.Split(res.Output, "\n") {
			parts := strings.SplitN(strings.TrimSpace(ln), "\t", 3)
			if len(parts) == 3 {
				items = append(items, entry{Type: parts[0], Size: parts[1], Name: parts[2]})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": p, "items": items})
	}
}

// ── Firewall Ports, Systemd Services & Processes / SSH Security ──────────────

func (s *Server) handleTenantServerFirewall(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	tgt := toSSHTarget(srv)
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"` // allow | deny
			Port   int    `json:"port"`
			Proto  string `json:"proto"` // tcp | udp
		}
		if !readJSON(w, r, &req) {
			return
		}
		if req.Port <= 0 || req.Port > 65535 {
			fail(w, http.StatusBadRequest, "invalid port")
			return
		}
		if req.Proto != "udp" {
			req.Proto = "tcp"
		}
		var cmd string
		if req.Action == "deny" {
			cmd = fmt.Sprintf(`ufw delete allow %d/%s 2>/dev/null || true; iptables -D INPUT -p %s --dport %d -j ACCEPT 2>/dev/null || true; echo "Closed %d/%s"`, req.Port, req.Proto, req.Proto, req.Port, req.Port, req.Proto)
		} else {
			cmd = fmt.Sprintf(`if command -v ufw >/dev/null 2>&1; then ufw allow %d/%s; else iptables -I INPUT -p %s --dport %d -j ACCEPT; fi && echo "Opened %d/%s"`, req.Port, req.Proto, req.Proto, req.Port, req.Port, req.Proto)
		}
		res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": strings.TrimSpace(res.Output)})
		return
	}
	cmd := `ss -tulpn 2>/dev/null | head -n 35`
	res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(res.Output)})
}

func (s *Server) handleTenantServerServices(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	tgt := toSSHTarget(srv)
	if r.Method == http.MethodPost {
		var req struct {
			Unit   string `json:"unit"`
			Action string `json:"action"` // start | stop | restart
		}
		if !readJSON(w, r, &req) {
			return
		}
		if req.Action != "start" && req.Action != "stop" && req.Action != "restart" {
			fail(w, http.StatusBadRequest, "invalid service action")
			return
		}
		res := sshclient.Run(tgt, fmt.Sprintf(`systemctl %s %s 2>&1 && echo "OK"`, req.Action, sshclient.ShQuote(req.Unit)), 10*time.Second, 20*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": strings.TrimSpace(res.Output)})
		return
	}
	cmd := `systemctl list-units --type=service --state=running --no-pager --no-legend 2>/dev/null | awk '{print $1"\t"$3"\t"$4}' | head -n 40`
	res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
	type svc struct {
		Unit   string `json:"unit"`
		Active string `json:"active"`
		Sub    string `json:"sub"`
	}
	list := []svc{}
	for _, ln := range strings.Split(res.Output, "\n") {
		f := strings.Split(strings.TrimSpace(ln), "\t")
		if len(f) >= 3 {
			list = append(list, svc{Unit: f[0], Active: f[1], Sub: f[2]})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "services": list})
}

func (s *Server) handleTenantServerProcesses(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	tgt := toSSHTarget(srv)
	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"` // kill | ban
			PID    int    `json:"pid"`
			IP     string `json:"ip"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if req.Action == "kill" && req.PID > 1 {
			res := sshclient.Run(tgt, fmt.Sprintf("kill -9 %d 2>&1 && echo 'Killed PID %d'", req.PID, req.PID), 10*time.Second, 10*time.Second)
			writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
			return
		}
		if req.Action == "ban" && strings.TrimSpace(req.IP) != "" {
			ip := strings.TrimSpace(req.IP)
			res := sshclient.Run(tgt, fmt.Sprintf("iptables -I INPUT -s %s -j DROP 2>&1 && echo 'Blocked %s'", sshclient.ShQuote(ip), ip), 10*time.Second, 10*time.Second)
			writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
			return
		}
		fail(w, http.StatusBadRequest, "invalid action")
		return
	}

	cmd := `echo "===PROCS==="
ps -eo pid,pcpu,pmem,comm --sort=-pcpu 2>/dev/null | head -n 16 | tail -n +2
echo "===SEC==="
(grep -i "Failed password" /var/log/auth.log 2>/dev/null || journalctl -u ssh -u sshd -n 150 --no-pager 2>/dev/null | grep -i "Failed password") | tail -n 12`
	res := sshclient.Run(tgt, cmd, 10*time.Second, 15*time.Second)
	type proc struct {
		PID  int    `json:"pid"`
		CPU  string `json:"cpu"`
		MEM  string `json:"mem"`
		Name string `json:"name"`
	}
	procs := []proc{}
	sec := ""
	parts := strings.Split(res.Output, "===SEC===")
	if len(parts) > 0 {
		for _, ln := range strings.Split(strings.TrimPrefix(strings.TrimSpace(parts[0]), "===PROCS==="), "\n") {
			f := strings.Fields(ln)
			if len(f) >= 4 {
				pid, _ := strconv.Atoi(f[0])
				if pid > 0 {
					procs = append(procs, proc{PID: pid, CPU: f[1] + "%", MEM: f[2] + "%", Name: f[3]})
				}
			}
		}
	}
	if len(parts) > 1 {
		sec = strings.TrimSpace(parts[1])
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "processes": procs, "security": sec})
}
