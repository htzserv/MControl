package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/storage"
)

func parsePortList(raw string) []string {
	var out []string
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"443"}
	}
	return out
}

func tunnelIPs(t *storage.Tunnel) (string, string) {
	loc := strings.TrimSpace(t.LocalIP)
	rem := strings.TrimSpace(t.RemoteIP)
	octet := (t.ID % 240) + 10
	if loc == "" {
		loc = fmt.Sprintf("10.10.%d.1", octet)
	}
	if rem == "" {
		rem = fmt.Sprintf("10.10.%d.2", octet)
	}
	return loc, rem
}

func buildDeployScripts(t *storage.Tunnel, iran, foreign *storage.Server) (iranCmd, foreignCmd string) {
	unit := fmt.Sprintf("mcontrol-tun-%d", t.ID)
	ports := parsePortList(t.Ports)
	mtu := t.MTU
	if mtu < 1200 || mtu > 1500 {
		mtu = 1420
	}
	locIP, remIP := tunnelIPs(t)

	switch t.Engine {
	case "gre", "ip6gre", "vxlan":
		mode := t.Engine
		ifName := fmt.Sprintf("mc%s%d", strings.ReplaceAll(mode, "ip6", "6"), t.ID)
		if len(ifName) > 14 {
			ifName = fmt.Sprintf("mctun%d", t.ID)
		}
		iranRemote := foreign.Host
		foreignRemote := iran.Host
		if mode == "ip6gre" {
			if foreign.IPv6 != "" && foreign.IPv6 != "N/A" {
				iranRemote = foreign.IPv6
			}
			if iran.IPv6 != "" && iran.IPv6 != "N/A" {
				foreignRemote = iran.IPv6
			}
		}

		mkKernel := func(myLocal, myRemote, tunLocal, tunPeer string) string {
			var setupLines string
			switch mode {
			case "ip6gre":
				setupLines = fmt.Sprintf(`ip -6 tunnel del %s 2>/dev/null || true
ip -6 tunnel add %s mode ip6gre local %s remote %s ttl 255
ip link set %s mtu %d up
ip addr add %s/30 dev %s`, ifName, ifName, myLocal, myRemote, ifName, mtu, tunLocal, ifName)
			case "vxlan":
				vni := 1000 + t.ID
				setupLines = fmt.Sprintf(`ip link del %s 2>/dev/null || true
ip link add %s type vxlan id %d remote %s dstport %d
ip link set %s mtu %d up
ip addr add %s/30 dev %s`, ifName, ifName, vni, myRemote, t.TunnelPort, ifName, mtu, tunLocal, ifName)
			default: // gre
				setupLines = fmt.Sprintf(`ip tunnel del %s 2>/dev/null || true
ip tunnel add %s mode gre local %s remote %s ttl 255
ip link set %s mtu %d up
ip addr add %s/30 dev %s`, ifName, ifName, myLocal, myRemote, ifName, mtu, tunLocal, ifName)
			}

			return fmt.Sprintf(`mkdir -p /etc/mcontrol
cat <<'EOF' > /etc/mcontrol/%s.sh
#!/usr/bin/env bash
set -e
sysctl -w net.ipv4.ip_forward=1 >/dev/null
%s
EOF
chmod +x /etc/mcontrol/%s.sh
cat <<'EOF' > /etc/systemd/system/%s.service
[Unit]
Description=MControl Kernel Tunnel %s (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/etc/mcontrol/%s.sh
ExecStop=/sbin/ip link del %s

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now %s.service
ip addr show %s | head -n 4
echo "DEPLOY_OK"`, unit, setupLines, unit, unit, t.Name, mode, unit, ifName, unit, ifName)
		}

		iranLocal := iran.Host
		foreignLocal := foreign.Host
		if mode == "ip6gre" {
			if iran.IPv6 != "" && iran.IPv6 != "N/A" {
				iranLocal = iran.IPv6
			}
			if foreign.IPv6 != "" && foreign.IPv6 != "N/A" {
				foreignLocal = foreign.IPv6
			}
		}
		iranCmd = mkKernel(iranLocal, iranRemote, locIP, remIP)
		foreignCmd = mkKernel(foreignLocal, foreignRemote, remIP, locIP)
		return

	case "rathole":
		var srvSvcs, cliSvcs strings.Builder
		for _, p := range ports {
			srvSvcs.WriteString(fmt.Sprintf("\n[server.services.p%s]\ntoken = \"%s\"\nbind_addr = \"0.0.0.0:%s\"\n", p, t.Token, p))
			cliSvcs.WriteString(fmt.Sprintf("\n[client.services.p%s]\ntoken = \"%s\"\nlocal_addr = \"127.0.0.1:%s\"\n", p, t.Token, p))
		}
		iranConf := fmt.Sprintf("[server]\nbind_addr = \"0.0.0.0:%d\"\n%s", t.TunnelPort, srvSvcs.String())
		forConf := fmt.Sprintf("[client]\nremote_addr = \"%s:%d\"\n%s", iran.Host, t.TunnelPort, cliSvcs.String())

		mkRathole := func(conf string) string {
			return fmt.Sprintf(`mkdir -p /etc/mcontrol
if ! command -v rathole >/dev/null 2>&1 && [ ! -x /usr/local/bin/rathole ]; then
  ARCH=$(uname -m | sed 's/x86_64/x86_64-unknown-linux-gnu/;s/aarch64/aarch64-unknown-linux-musl/')
  (curl -fsSL --connect-timeout 8 "https://github.com/rapiz1/rathole/releases/latest/download/rathole-${ARCH}.zip" -o /tmp/rh.zip || curl -fsSL --connect-timeout 10 "https://gh-proxy.com/https://github.com/rapiz1/rathole/releases/latest/download/rathole-${ARCH}.zip" -o /tmp/rh.zip) && unzip -o /tmp/rh.zip -d /usr/local/bin/ && chmod +x /usr/local/bin/rathole && rm -f /tmp/rh.zip
fi
cat <<'EOF' > /etc/mcontrol/%s.toml
%s
EOF
cat <<'EOF' > /etc/systemd/system/%s.service
[Unit]
Description=MControl Rathole %s
After=network-online.target

[Service]
ExecStart=/usr/local/bin/rathole /etc/mcontrol/%s.toml
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now %s.service
systemctl is-active %s.service
echo "DEPLOY_OK"`, unit, conf, unit, t.Name, unit, unit, unit)
		}
		return mkRathole(iranConf), mkRathole(forConf)

	case "paqet":
		var fwdLines []string
		for _, p := range ports {
			fwdLines = append(fwdLines, fmt.Sprintf("  - listen: \"0.0.0.0:%s\"\n    target: \"127.0.0.1:%s\"\n    protocol: \"tcp\"", p, p))
		}
		iranConf := fmt.Sprintf(`role: "client"
log:
  level: "info"
server:
  addr: "%s:%d"
transport:
  protocol: "kcp"
  kcp:
    mode: "fast"
    key: "%s"
forward:
%s
`, foreign.Host, t.TunnelPort, t.Token, strings.Join(fwdLines, "\n"))

		forConf := fmt.Sprintf(`role: "server"
log:
  level: "info"
listen:
  addr: "0.0.0.0:%d"
transport:
  protocol: "kcp"
  kcp:
    mode: "fast"
    key: "%s"
`, t.TunnelPort, t.Token)

		mkPaqet := func(conf string) string {
			return fmt.Sprintf(`mkdir -p /etc/mcontrol
if [ ! -x /usr/local/bin/paqet ]; then
  ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  (curl -fsSL --connect-timeout 8 "https://github.com/hanselime/paqet/releases/latest/download/paqet_linux_${ARCH}.tar.gz" -o /tmp/pq.tar.gz || curl -fsSL --connect-timeout 10 "https://gh-proxy.com/https://github.com/hanselime/paqet/releases/latest/download/paqet_linux_${ARCH}.tar.gz" -o /tmp/pq.tar.gz) && tar -xzf /tmp/pq.tar.gz -C /usr/local/bin/ && chmod +x /usr/local/bin/paqet* 2>/dev/null || true
  if [ ! -x /usr/local/bin/paqet ] && [ -x /usr/local/bin/paqet_linux_${ARCH} ]; then mv /usr/local/bin/paqet_linux_${ARCH} /usr/local/bin/paqet; fi
  rm -f /tmp/pq.tar.gz
fi
cat <<'EOF' > /etc/mcontrol/%s.yaml
%s
EOF
cat <<'EOF' > /etc/systemd/system/%s.service
[Unit]
Description=MControl Paqet %s
After=network-online.target

[Service]
ExecStart=/usr/local/bin/paqet run -c /etc/mcontrol/%s.yaml
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now %s.service
systemctl is-active %s.service
echo "DEPLOY_OK"`, unit, conf, unit, t.Name, unit, unit, unit)
		}
		return mkPaqet(iranConf), mkPaqet(forConf)

	default: // backhaul
		transport := t.Transport
		if transport == "" {
			transport = "tcp"
		}
		var portLines []string
		for _, p := range ports {
			portLines = append(portLines, fmt.Sprintf(`"%s=%s"`, p, p))
		}
		iranConf := fmt.Sprintf(`[server]
bind_addr = "0.0.0.0:%d"
transport = "%s"
token = "%s"
keepalive_period = 75
nodelay = true
heartbeat = 40
channel_size = 2048
ports = [%s]
`, t.TunnelPort, transport, t.Token, strings.Join(portLines, ", "))

		forConf := fmt.Sprintf(`[client]
remote_addr = "%s:%d"
transport = "%s"
token = "%s"
connection_pool = 8
aggressive_pool = false
keepalive_period = 75
nodelay = true
retry_interval = 3
`, iran.Host, t.TunnelPort, transport, t.Token)

		mkBackhaul := func(conf string) string {
			return fmt.Sprintf(`mkdir -p /etc/mcontrol
if [ ! -x /usr/local/bin/backhaul ]; then
  ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  (curl -fsSL --connect-timeout 8 "https://github.com/Musixal/Backhaul/releases/latest/download/backhaul_linux_${ARCH}.tar.gz" -o /tmp/bh.tar.gz || curl -fsSL --connect-timeout 10 "https://gh-proxy.com/https://github.com/Musixal/Backhaul/releases/latest/download/backhaul_linux_${ARCH}.tar.gz" -o /tmp/bh.tar.gz) && tar -xzf /tmp/bh.tar.gz -C /usr/local/bin/ backhaul && chmod +x /usr/local/bin/backhaul && rm -f /tmp/bh.tar.gz
fi
cat <<'EOF' > /etc/mcontrol/%s.toml
%s
EOF
cat <<'EOF' > /etc/systemd/system/%s.service
[Unit]
Description=MControl Backhaul %s
After=network-online.target

[Service]
ExecStart=/usr/local/bin/backhaul -c /etc/mcontrol/%s.toml
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now %s.service
systemctl is-active %s.service
echo "DEPLOY_OK"`, unit, conf, unit, t.Name, unit, unit, unit)
		}
		return mkBackhaul(iranConf), mkBackhaul(forConf)
	}
}

func (s *Server) handleTenantDeployTunnel(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := s.engine.GetTunnel(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iran, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "entry server not found")
		return
	}
	foreign, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "exit server not found")
		return
	}

	iranCmd, foreignCmd := buildDeployScripts(tun, iran, foreign)

	var wg sync.WaitGroup
	var resIran, resFor sshclient.Result
	wg.Add(2)
	go func() {
		defer wg.Done()
		resIran = sshclient.Run(toSSHTarget(iran), iranCmd, 10*time.Second, 60*time.Second)
	}()
	go func() {
		defer wg.Done()
		resFor = sshclient.Run(toSSHTarget(foreign), foreignCmd, 10*time.Second, 60*time.Second)
	}()
	wg.Wait()

	okIran := resIran.Err == nil && strings.Contains(resIran.Output, "DEPLOY_OK")
	okFor := resFor.Err == nil && strings.Contains(resFor.Output, "DEPLOY_OK")
	status := "active"
	if !okIran || !okFor {
		status = "error"
	}
	logOut := fmt.Sprintf("=== ENTRY (%s) ===\n%s\n%v\n\n=== EXIT (%s) ===\n%s\n%v",
		iran.Name, strings.TrimSpace(resIran.Output), resIran.Err,
		foreign.Name, strings.TrimSpace(resFor.Output), resFor.Err)
	_ = s.engine.UpdateTunnelRuntime(r.Context(), claims.UserID, id, status, logOut, "")

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     okIran && okFor,
		"status": status,
		"log":    logOut,
	})
}

func (s *Server) handleTenantTunnelAction(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"` // start | stop | restart | status | cleanup
	}
	if !readJSON(w, r, &req) {
		return
	}
	tun, err := s.engine.GetTunnel(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iran, err1 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.IranServerID)
	foreign, err2 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.ForeignServerID)
	if err1 != nil || err2 != nil {
		fail(w, http.StatusNotFound, "linked servers not found")
		return
	}

	unit := fmt.Sprintf("mcontrol-tun-%d.service", tun.ID)
	var cmd string
	switch req.Action {
	case "start", "stop", "restart":
		cmd = fmt.Sprintf("systemctl %s %s && systemctl is-active %s || true", req.Action, unit, unit)
	case "status":
		cmd = fmt.Sprintf("systemctl is-active %s || true", unit)
	case "cleanup":
		cmd = fmt.Sprintf("systemctl disable --now %s 2>/dev/null || true; rm -f /etc/systemd/system/%s /etc/mcontrol/mcontrol-tun-%d.* && systemctl daemon-reload && echo CLEANED", unit, unit, tun.ID)
	default:
		fail(w, http.StatusBadRequest, "invalid tunnel action")
		return
	}

	var wg sync.WaitGroup
	var r1, r2 sshclient.Result
	wg.Add(2)
	go func() { defer wg.Done(); r1 = sshclient.Run(toSSHTarget(iran), cmd, 10*time.Second, 20*time.Second) }()
	go func() { defer wg.Done(); r2 = sshclient.Run(toSSHTarget(foreign), cmd, 10*time.Second, 20*time.Second) }()
	wg.Wait()

	newStatus := tun.Status
	if req.Action == "stop" || req.Action == "cleanup" {
		newStatus = "stopped"
	} else if strings.Contains(r1.Output, "active") && strings.Contains(r2.Output, "active") {
		newStatus = "active"
	} else if req.Action == "start" || req.Action == "restart" || req.Action == "status" {
		newStatus = "error"
	}
	out := fmt.Sprintf("Entry(%s): %s | Exit(%s): %s", iran.Name, strings.TrimSpace(r1.Output), foreign.Name, strings.TrimSpace(r2.Output))
	_ = s.engine.UpdateTunnelRuntime(r.Context(), claims.UserID, id, newStatus, out, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": newStatus, "output": out})
}

func (s *Server) handleTenantTunnelPing(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := s.engine.GetTunnel(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iran, err1 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.IranServerID)
	foreign, err2 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.ForeignServerID)
	if err1 != nil || err2 != nil {
		fail(w, http.StatusNotFound, "linked servers not found")
		return
	}

	pingCmd := func(dst string) string {
		return fmt.Sprintf(`ping -c 3 -W 2 %s 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%%.1f ms", $5; else print "timeout"}'`, sshclient.ShQuote(dst))
	}

	var wg sync.WaitGroup
	var r1, r2 sshclient.Result
	wg.Add(2)
	go func() { defer wg.Done(); r1 = sshclient.Run(toSSHTarget(iran), pingCmd(foreign.Host), 10*time.Second, 15*time.Second) }()
	go func() { defer wg.Done(); r2 = sshclient.Run(toSSHTarget(foreign), pingCmd(iran.Host), 10*time.Second, 15*time.Second) }()
	wg.Wait()

	summary := fmt.Sprintf("IR→FR: %s · FR→IR: %s", strings.TrimSpace(r1.Output), strings.TrimSpace(r2.Output))
	_ = s.engine.UpdateTunnelRuntime(r.Context(), claims.UserID, id, tun.Status, "", summary)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ping": summary})
}

func (s *Server) handleTenantTunnelMikrotik(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := s.engine.GetTunnel(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iran, _ := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.IranServerID)
	foreign, _ := s.engine.GetServerWithSecret(r.Context(), claims.UserID, tun.ForeignServerID)
	locIP, remIP := tunnelIPs(tun)
	mtu := tun.MTU
	if mtu <= 0 {
		mtu = 1420
	}
	ifName := fmt.Sprintf("mc-%s-%d", tun.Engine, tun.ID)

	var script string
	if tun.Engine == "ip6gre" {
		remV6 := foreign.Host
		if foreign.IPv6 != "" && foreign.IPv6 != "N/A" {
			remV6 = foreign.IPv6
		}
		locV6 := iran.Host
		if iran.IPv6 != "" && iran.IPv6 != "N/A" {
			locV6 = iran.IPv6
		}
		script = fmt.Sprintf(`# MControl v2 — MikroTik RouterOS v7 IP6GRE Script (%s)
/interface/gre6/add name=%s local-address=%s remote-address=%s mtu=%d clamp-tcp-mss=yes
/ip/address/add address=%s/30 interface=%s
/ip/firewall/mangle/add chain=forward out-interface=%s protocol=tcp tcp-flags=syn action=change-mss new-mss=1360 passthrough=yes
# Remote Linux Peer IP: %s`, tun.Name, ifName, locV6, remV6, mtu, locIP, ifName, ifName, remIP)
	} else {
		script = fmt.Sprintf(`# MControl v2 — MikroTik RouterOS v7 GRE Script (%s)
/interface/gre/add name=%s local-address=%s remote-address=%s mtu=%d clamp-tcp-mss=yes
/ip/address/add address=%s/30 interface=%s
/ip/firewall/mangle/add chain=forward out-interface=%s protocol=tcp tcp-flags=syn action=change-mss new-mss=1380 passthrough=yes
# Remote Linux Peer IP: %s`, tun.Name, ifName, iran.Host, foreign.Host, mtu, locIP, ifName, ifName, remIP)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "script": script})
}

// ── Network Diagnostics: Path MTU Discovery & MTR Trace ──────────────────────

func (s *Server) handleTenantDiagMTU(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req struct {
		SourceServerID int64  `json:"source_server_id"`
		TargetHost     string `json:"target_host"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, req.SourceServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "source server not found")
		return
	}
	dst := strings.TrimSpace(req.TargetHost)
	if dst == "" {
		fail(w, http.StatusBadRequest, "target host required")
		return
	}
	cmd := fmt.Sprintf(`BEST=0
for sz in 1472 1452 1432 1412 1392 1372 1352 1332 1292 1252; do
  if ping -c 1 -W 2 -M do -s $sz %s >/dev/null 2>&1; then
    BEST=$((sz + 28))
    echo "PASS payload=$sz -> MTU=$BEST"
    break
  else
    echo "FRAG payload=$sz (MTU $((sz + 28)))"
  fi
done
echo "RECOMMENDED_MTU=$BEST"`, sshclient.ShQuote(dst))
	res := sshclient.Run(toSSHTarget(srv), cmd, 10*time.Second, 30*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": strings.TrimSpace(res.Output)})
}

func (s *Server) handleTenantDiagMTR(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req struct {
		SourceServerID int64  `json:"source_server_id"`
		TargetHost     string `json:"target_host"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, req.SourceServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "source server not found")
		return
	}
	dst := strings.TrimSpace(req.TargetHost)
	if dst == "" {
		fail(w, http.StatusBadRequest, "target host required")
		return
	}
	cmd := fmt.Sprintf(`if command -v mtr >/dev/null 2>&1; then mtr -rwzbc 5 %s; elif command -v traceroute >/dev/null 2>&1; then traceroute -n -w 2 -m 20 %s; else tracepath -n %s 2>&1 || ping -c 4 %s; fi`,
		sshclient.ShQuote(dst), sshclient.ShQuote(dst), sshclient.ShQuote(dst), sshclient.ShQuote(dst))
	res := sshclient.Run(toSSHTarget(srv), cmd, 10*time.Second, 40*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": strings.TrimSpace(res.Output)})
}
