package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
)

func randTunnelToken() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handler) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListTunnels()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tunnels": list})
}

func (h *Handler) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string `json:"name"`
		Engine          string `json:"engine"`
		Transport       string `json:"transport"`
		IranServerID    int64  `json:"iran_server_id"`
		ForeignServerID int64  `json:"foreign_server_id"`
		TunnelPort      int    `json:"tunnel_port"`
		Token           string `json:"token"`
		Ports           string `json:"ports"`
		Forwarder       string `json:"forwarder"`
		IPMode          string `json:"ip_mode"` // v4 | v6 | 6to4 | 6to6
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Engine = strings.ToLower(strings.TrimSpace(req.Engine))
	req.Transport = strings.ToLower(strings.TrimSpace(req.Transport))
	if req.Name == "" {
		fail(w, http.StatusBadRequest, "tunnel name is required")
		return
	}
	if req.IranServerID <= 0 || req.ForeignServerID <= 0 || req.IranServerID == req.ForeignServerID {
		fail(w, http.StatusBadRequest, "select two distinct servers (Iran & Foreign)")
		return
	}
	switch req.Engine {
	case "backhaul", "rathole", "paqet", "gre", "ip6gre", "vxlan":
	default:
		fail(w, http.StatusBadRequest, "unsupported engine (backhaul, rathole, paqet, gre, ip6gre, vxlan)")
		return
	}
	if req.TunnelPort <= 0 || req.TunnelPort > 65535 {
		req.TunnelPort = 3080
	}
	if req.Token == "" {
		req.Token = randTunnelToken()
	}
	if req.Transport == "" {
		req.Transport = "tcp"
	}
	if req.IPMode == "v6" || req.IPMode == "6to4" || req.IPMode == "6to6" {
		if !strings.Contains(req.Transport, "@") {
			req.Transport = req.Transport + "@" + req.IPMode
		}
	}
	normPorts, portList, err := normalizePorts(req.Ports)
	if err != nil || len(portList) == 0 {
		fail(w, http.StatusBadRequest, "at least one valid forward port (1-65535) is required")
		return
	}
	if req.Engine == "gre" || req.Engine == "ip6gre" || req.Engine == "vxlan" {
		if req.Forwarder != "realm" {
			req.Forwarder = "iptables"
		}
	} else {
		req.Forwarder = "internal"
	}

	iranSrv, err := h.store.GetServerWithSecret(req.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Iran server not found")
		return
	}
	forSrv, err := h.store.GetServerWithSecret(req.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Foreign server not found")
		return
	}

	tun := &db.Tunnel{
		Name:            req.Name,
		Engine:          req.Engine,
		Transport:       req.Transport,
		IranServerID:    req.IranServerID,
		ForeignServerID: req.ForeignServerID,
		TunnelPort:      req.TunnelPort,
		Token:           req.Token,
		Ports:           normPorts,
		Forwarder:       req.Forwarder,
		Status:          "deploying",
	}
	id, err := h.store.AddTunnel(tun)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tun.ID = id
	tun.Subnet = fmt.Sprintf("10.10.%d", (id%240)+10)

	iranCmd, forCmd := buildTunnelDeployScripts(tun, iranSrv, forSrv, portList)

	var wg sync.WaitGroup
	var resIran, resFor sshclient.Result
	wg.Add(2)
	go func() {
		defer wg.Done()
		resIran = h.runOnServer(iranSrv, iranCmd, 60*time.Second)
	}()
	go func() {
		defer wg.Done()
		resFor = h.runOnServer(forSrv, forCmd, 60*time.Second)
	}()
	wg.Wait()

	var errs []string
	if resIran.Err != nil {
		errs = append(errs, "Iran: "+resIran.Err.Error())
	}
	if resFor.Err != nil {
		errs = append(errs, "Foreign: "+resFor.Err.Error())
	}
	if len(errs) > 0 {
		errMsg := strings.Join(errs, " | ")
		_ = h.store.UpdateTunnelStatus(id, "error", errMsg)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "id": id, "error": errMsg,
			"output": "=== IRAN ===\n" + resIran.Output + "\n\n=== FOREIGN ===\n" + resFor.Output,
		})
		return
	}
	_ = h.store.UpdateTunnelStatus(id, "active", "")
	log.Printf("tunnel #%d (%s / %s) deployed Iran(%d)<->Foreign(%d)", id, tun.Name, tun.Engine, iranSrv.ID, forSrv.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "id": id,
		"output": "=== IRAN ===\n" + resIran.Output + "\n\n=== FOREIGN ===\n" + resFor.Output,
	})
}


func splitTransportIPMode(raw string) (transport, ipMode string) {
	parts := strings.SplitN(raw, "@", 2)
	transport = parts[0]
	if len(parts) == 2 {
		ipMode = parts[1]
	} else {
		ipMode = "v4"
	}
	return
}

func hostForMode(s *db.Server, ipMode string) string {
	if (ipMode == "v6" || ipMode == "6to4" || ipMode == "6to6") && s.IPv6 != "" && s.IPv6 != "N/A" {
		return s.IPv6
	}
	return s.Host
}

func formatHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func buildTunnelDeployScripts(t *db.Tunnel, iran, foreign *db.Server, ports []int) (string, string) {
	unit := fmt.Sprintf("mcontrol-tun-%d", t.ID)
	cfgDir := "/etc/mcontrol/tunnels"
	baseTrans, ipMode := splitTransportIPMode(t.Transport)
	iranAddr := hostForMode(iran, ipMode)
	forAddr := hostForMode(foreign, ipMode)
	bindWild := "0.0.0.0"
	if ipMode == "v6" || ipMode == "6to6" {
		bindWild = "[::]"
	}

	switch t.Engine {
	case "backhaul":
		var portLines []string
		for _, p := range ports {
			portLines = append(portLines, fmt.Sprintf(`"%d=%d"`, p, p))
		}
		iranToml := fmt.Sprintf(`[server]
bind_addr = "%s:%d"
transport = "%s"
token = "%s"
nodelay = true
keepalive_period = 75
heartbeat = 40
channel_size = 2048
sniffer = false
ports = [%s]
`, bindWild, t.TunnelPort, baseTrans, t.Token, strings.Join(portLines, ", "))

		forToml := fmt.Sprintf(`[client]
remote_addr = "%s"
transport = "%s"
token = "%s"
connection_pool = 8
retry_interval = 3
nodelay = true
keepalive_period = 75
`, formatHostPort(iranAddr, t.TunnelPort), baseTrans, t.Token)

		makeBH := func(toml string) string {
			return fmt.Sprintf(`set -e
BIN=$(command -v bh 2>/dev/null || command -v backhaul 2>/dev/null || true)
[ -n "$BIN" ] || { echo "Backhaul binary (bh) not installed! Run Setup -> Install Static Bundle first." >&2; exit 1; }
mkdir -p %s
cat <<'EOF' > %s/%s.toml
%sEOF
cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Backhaul Tunnel %d (%s)
After=network-online.target

[Service]
Type=simple
ExecStart=$BIN -c %s/%s.toml
Restart=always
RestartSec=2
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable %s.service
systemctl restart %s.service
sleep 1
systemctl is-active %s.service || true
`, cfgDir, cfgDir, unit, toml, unit, t.ID, t.Name, cfgDir, unit, unit, unit, unit)
		}
		return makeBH(iranToml), makeBH(forToml)

	case "rathole":
		var srvSvcs, cliSvcs strings.Builder
		for _, p := range ports {
			srvSvcs.WriteString(fmt.Sprintf("\n[server.services.p%d]\nbind_addr = \"0.0.0.0:%d\"\n", p, p))
			cliSvcs.WriteString(fmt.Sprintf("\n[client.services.p%d]\nlocal_addr = \"127.0.0.1:%d\"\n", p, p))
		}
		iranToml := fmt.Sprintf("[server]\nbind_addr = \"%s:%d\"\ndefault_token = \"%s\"\n%s", bindWild, t.TunnelPort, t.Token, srvSvcs.String())
		forToml := fmt.Sprintf("[client]\nremote_addr = \"%s\"\ndefault_token = \"%s\"\n%s", formatHostPort(iranAddr, t.TunnelPort), t.Token, cliSvcs.String())
		makeRH := func(toml string) string {
			return fmt.Sprintf(`set -e
BIN=$(command -v rathole 2>/dev/null || true)
[ -n "$BIN" ] || { echo "Rathole binary not installed! Run Setup -> Install Static Bundle first." >&2; exit 1; }
mkdir -p %s
cat <<'EOF' > %s/%s.toml
%s
EOF
cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Rathole Tunnel %d (%s)
After=network-online.target

[Service]
Type=simple
ExecStart=$BIN %s/%s.toml
Restart=always
RestartSec=2
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable %s.service
systemctl restart %s.service
sleep 1
systemctl is-active %s.service || true
`, cfgDir, cfgDir, unit, toml, unit, t.ID, t.Name, cfgDir, unit, unit, unit, unit)
		}
		return makeRH(iranToml), makeRH(forToml)

	case "paqet":
		var fwdFlags []string
		for _, p := range ports {
			fwdFlags = append(fwdFlags, fmt.Sprintf("-f 0.0.0.0:%d=127.0.0.1:%d", p, p))
		}
		iranCmd := fmt.Sprintf(`set -e
BIN=$(command -v paqet 2>/dev/null || true)
[ -n "$BIN" ] || { echo "Paqet binary not installed! Run Setup -> Install Static Bundle first." >&2; exit 1; }
cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Paqet Server %d (%s)
After=network-online.target

[Service]
Type=simple
ExecStart=$BIN server -l 0.0.0.0:%d -k %s %s
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable %s.service && systemctl restart %s.service && sleep 1 && (systemctl is-active %s.service || true)
`, unit, t.ID, t.Name, t.TunnelPort, t.Token, strings.Join(fwdFlags, " "), unit, unit, unit)

		forCmd := fmt.Sprintf(`set -e
BIN=$(command -v paqet 2>/dev/null || true)
[ -n "$BIN" ] || { echo "Paqet binary not installed! Run Setup -> Install Static Bundle first." >&2; exit 1; }
cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Paqet Client %d (%s)
After=network-online.target

[Service]
Type=simple
ExecStart=$BIN client -r %s:%d -k %s
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable %s.service && systemctl restart %s.service && sleep 1 && (systemctl is-active %s.service || true)
`, unit, t.ID, t.Name, iran.Host, t.TunnelPort, t.Token, unit, unit, unit)
		return iranCmd, forCmd

	default:
		ifName := fmt.Sprintf("mctun%d", t.ID)
		iranLocal := t.Subnet + ".1/30"
		forLocal := t.Subnet + ".2/30"
		forPeerIP := t.Subnet + ".2"
		tag := fmt.Sprintf("mc_tun_%d", t.ID)

		var iranLink, forLink string
		if t.Engine == "vxlan" {
			vni := 100 + t.ID
			iranLink = fmt.Sprintf("ip link add %s type vxlan id %d remote %s local %s dstport %d", ifName, vni, forAddr, iranAddr, t.TunnelPort)
			forLink = fmt.Sprintf("ip link add %s type vxlan id %d remote %s local %s dstport %d", ifName, vni, iranAddr, forAddr, t.TunnelPort)
		} else if t.Engine == "ip6gre" || ipMode == "6to4" || ipMode == "6to6" {
			iranLink = fmt.Sprintf("ip -6 tunnel add %s mode ip6gre remote %s local %s ttl 255", ifName, forAddr, iranAddr)
			forLink = fmt.Sprintf("ip -6 tunnel add %s mode ip6gre remote %s local %s ttl 255", ifName, iranAddr, forAddr)
		} else {
			iranLink = fmt.Sprintf("ip tunnel add %s mode gre remote %s local %s ttl 255", ifName, foreign.Host, iran.Host)
			forLink = fmt.Sprintf("ip tunnel add %s mode gre remote %s local %s ttl 255", ifName, iran.Host, foreign.Host)
		}

		var iptRules strings.Builder
		for _, p := range ports {
			iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p tcp --dport %d -m comment --comment %s -j DNAT --to-destination %s:%d\n", p, tag, forPeerIP, p))
			iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p udp --dport %d -m comment --comment %s -j DNAT --to-destination %s:%d\n", p, tag, forPeerIP, p))
		}
		iptRules.WriteString(fmt.Sprintf("iptables -t nat -A POSTROUTING -o %s -m comment --comment %s -j MASQUERADE\n", ifName, tag))

		makeKernel := func(linkCmd, cidr, extraRules string) string {
			return fmt.Sprintf(`set -e
mkdir -p %s
cat <<'EOF' > %s/%s.sh
#!/usr/bin/env bash
set -e
sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
modprobe ip_gre 2>/dev/null || true
modprobe ip6_gre 2>/dev/null || true
modprobe vxlan 2>/dev/null || true
ip link del %s 2>/dev/null || true
ip tunnel del %s 2>/dev/null || true
%s
ip addr add %s dev %s
ip link set %s mtu 1420 up
iptables-save -t nat | grep -v "%s" | iptables-restore -T nat 2>/dev/null || true
%s
EOF
chmod 755 %s/%s.sh
cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Kernel Tunnel %d (%s)
After=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s/%s.sh
ExecStop=/sbin/ip link del %s

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable %s.service
systemctl restart %s.service
ip -br addr show %s
`, cfgDir, cfgDir, unit, ifName, ifName, linkCmd, cidr, ifName, ifName, tag, extraRules, cfgDir, unit, unit, t.ID, t.Name, cfgDir, unit, ifName, unit, unit, ifName)
		}
		return makeKernel(iranLink, iranLocal, iptRules.String()), makeKernel(forLink, forLocal, "")
	}
}

func (h *Handler) handleTunnelAction(w http.ResponseWriter, r *http.Request) {
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
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iranSrv, _ := h.store.GetServerWithSecret(tun.IranServerID)
	forSrv, _ := h.store.GetServerWithSecret(tun.ForeignServerID)
	unit := fmt.Sprintf("mcontrol-tun-%d.service", id)

	var cmd string
	switch req.Action {
	case "start", "stop", "restart":
		cmd = fmt.Sprintf("systemctl %s %s 2>&1; systemctl is-active %s 2>&1", req.Action, unit, unit)
	case "status":
		cmd = fmt.Sprintf("systemctl status %s --no-pager -l 2>&1 | head -n 22", unit)
	case "logs":
		cmd = fmt.Sprintf("journalctl -u %s -n 45 --no-pager 2>&1", unit)
	default:
		fail(w, http.StatusBadRequest, "invalid action")
		return
	}

	var wg sync.WaitGroup
	var rIran, rFor sshclient.Result
	if iranSrv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); rIran = h.runOnServer(iranSrv, cmd, 25*time.Second) }()
	}
	if forSrv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); rFor = h.runOnServer(forSrv, cmd, 25*time.Second) }()
	}
	wg.Wait()

	if req.Action == "stop" {
		_ = h.store.UpdateTunnelStatus(id, "stopped", "")
	} else if req.Action == "start" || req.Action == "restart" {
		_ = h.store.UpdateTunnelStatus(id, "active", "")
	}
	out := fmt.Sprintf("=== IRAN (%s) ===\n%s\n\n=== FOREIGN (%s) ===\n%s", tun.IranName, rIran.Output, tun.ForeignName, rFor.Output)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (h *Handler) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
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

	unit := fmt.Sprintf("mcontrol-tun-%d", id)
	ifName := fmt.Sprintf("mctun%d", id)
	tag := fmt.Sprintf("mc_tun_%d", id)
	cleanCmd := fmt.Sprintf(`systemctl disable --now %s.service 2>/dev/null || true
rm -f /etc/systemd/system/%s.service /etc/mcontrol/tunnels/%s.toml /etc/mcontrol/tunnels/%s.sh
ip link del %s 2>/dev/null || true
ip tunnel del %s 2>/dev/null || true
iptables-save -t nat | grep -v "%s" | iptables-restore -T nat 2>/dev/null || true
systemctl daemon-reload
echo "Cleaned."`, unit, unit, unit, unit, ifName, ifName, tag)

	var wg sync.WaitGroup
	if iranSrv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h.runOnServer(iranSrv, cleanCmd, 20*time.Second) }()
	}
	if forSrv != nil {
		wg.Add(1)
		go func() { defer wg.Done(); _ = h.runOnServer(forSrv, cleanCmd, 20*time.Second) }()
	}
	wg.Wait()

	_ = h.store.DeleteTunnel(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleLinkTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IranServerID    int64 `json:"iran_server_id"`
		ForeignServerID int64 `json:"foreign_server_id"`
		Port            int   `json:"port"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		req.Port = 5201
	}
	iranSrv, err := h.store.GetServerWithSecret(req.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Iran server not found")
		return
	}
	forSrv, err := h.store.GetServerWithSecret(req.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Foreign server not found")
		return
	}

	srvCmd := fmt.Sprintf(`command -v iperf3 >/dev/null 2>&1 || { echo "iperf3 missing on Foreign"; exit 1; }
nohup timeout 22 iperf3 -s -1 -p %d >/dev/null 2>&1 &
sleep 0.5
echo "iperf3 listener ready on :%d"`, req.Port, req.Port)
	rFor := h.runOnServer(forSrv, srvCmd, 10*time.Second)

	cliCmd := fmt.Sprintf(`echo "=== PING (%s -> %s) ==="
ping -c 4 -W 2 %s 2>&1 | tail -n 2 || true
echo ""
echo "=== IPERF3 BANDWIDTH TEST (5s) ==="
if command -v iperf3 >/dev/null 2>&1; then
  iperf3 -c %s -p %d -t 5 -P 4 2>&1 | tail -n 6
else
  echo "iperf3 not installed on Iran node! Run Setup -> Install Static Bundle first."
fi`, iranSrv.Name, forSrv.Name, sshclient.ShQuote(forSrv.Host), sshclient.ShQuote(forSrv.Host), req.Port)

	rIran := h.runOnServer(iranSrv, cliCmd, 35*time.Second)
	out := rIran.Output
	if rFor.Err != nil {
		out = "[Foreign listener warning: " + rFor.Output + "]\n\n" + out
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
}

func (h *Handler) handleTunnelSpeedtest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iranSrv, err := h.store.GetServerWithSecret(tun.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Iran server not found")
		return
	}
	forSrv, err := h.store.GetServerWithSecret(tun.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Foreign server not found")
		return
	}

	_, portList, _ := normalizePorts(tun.Ports)
	testPort := 49155
	if len(portList) > 0 {
		testPort = portList[0]
	}

	// For kernel tunnels (GRE/IP6GRE/VXLAN), test directly over the tunnel subnet IP (.2)
	targetHost := "127.0.0.1"
	if tun.Engine == "gre" || tun.Engine == "ip6gre" || tun.Engine == "vxlan" {
		sub := tun.Subnet
		if sub == "" {
			sub = fmt.Sprintf("10.10.%d", (tun.ID%240)+10)
		}
		targetHost = sub + ".2"
		testPort = 49155
	}

	// Start temporary sink on Foreign (if port isn't already occupied by a service)
	srvSink := fmt.Sprintf(`nohup python3 -c "
import socket, threading, time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
try:
  s.bind(('0.0.0.0',%d)); s.listen(16); s.settimeout(12)
  def h(c):
    try:
      while c.recv(65536): pass
    except: pass
    finally: c.close()
  end=time.time()+12
  while time.time()<end:
    try:
      c,_=s.accept(); threading.Thread(target=h,args=(c,),daemon=True).start()
    except: break
except Exception as e:
  print('BIND_NOTE:', e)
finally: s.close()
" >/dev/null 2>&1 & sleep 0.4`, testPort)
	_ = h.runOnServer(forSrv, srvSink, 8*time.Second)

	cliCmd := fmt.Sprintf(`echo "=== IN-TUNNEL SPEEDTEST: %s (%s) -> %s:%d ==="
python3 -c "
import socket, threading, time
host, port = '%s', %d
rtts = []
for _ in range(3):
  try:
    t0=time.time(); s=socket.create_connection((host,port),3); rtts.append((time.time()-t0)*1000); s.close()
  except Exception as e:
    print('Connect err:', e)
if rtts: print(f'RTT (min/avg): {min(rtts):.1f} ms / {sum(rtts)/len(rtts):.1f} ms')
total = [0]
buf = b'x'*65536
def worker():
  try:
    s=socket.create_connection((host,port),3)
    end=time.time()+4.0
    sent=0
    while time.time()<end: sent += s.send(buf)
    s.close(); total[0] += sent
  except: pass
t0=time.time()
ts=[threading.Thread(target=worker) for _ in range(4)]
for t in ts: t.start()
for t in ts: t.join()
dt=max(0.1, time.time()-t0)
mbps=(total[0]*8)/(dt*1e6)
print(f'Throughput (4 streams): {mbps:.1f} Mbps')
" 2>&1`, tun.Name, strings.ToUpper(tun.Engine), targetHost, testPort, targetHost, testPort)

	res := h.runOnServer(iranSrv, cliCmd, 22*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": res.Output})
}
