package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/sshclient"
)

// ── Protocol Matrix Tester (ICMPv4, ICMPv6, TCP, UDP, GRE/IP6GRE, Engines) ──

func (s *Server) handleTenantDiagProtocolMatrix(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req struct {
		IranServerID    int64 `json:"iran_server_id"`
		ForeignServerID int64 `json:"foreign_server_id"`
		TestPort        int   `json:"test_port"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.TestPort <= 0 || req.TestPort > 65535 {
		req.TestPort = 443
	}
	iran, err1 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, req.IranServerID)
	foreign, err2 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, req.ForeignServerID)
	if err1 != nil || err2 != nil {
		fail(w, http.StatusNotFound, "both entry and exit servers are required")
		return
	}

	targetV6 := foreign.IPv6
	if targetV6 == "" || targetV6 == "N/A" {
		targetV6 = ""
	}

	cmd := fmt.Sprintf(`
V4=%s
V6=%s
PORT=%d

# 1. ICMP IPv4
P4=$(ping -c 3 -W 2 "$V4" 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%%.1f ms", $5; else print "FAIL"}')
echo "ICMP_V4=$P4"

# 2. ICMP IPv6
if [ -n "$V6" ]; then
  P6=$(ping -6 -c 3 -W 2 "$V6" 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%%.1f ms", $5; else print "FAIL"}')
else
  P6="NO_IPV6"
fi
echo "ICMP_V6=$P6"

# 3. TCP Handshake
T0=$(date +%%s%%3N 2>/dev/null || echo 0)
if timeout 3 bash -c "cat < /dev/null > /dev/tcp/$V4/$PORT" 2>/dev/null; then
  T1=$(date +%%s%%3N 2>/dev/null || echo 0)
  echo "TCP_PORT=$((T1 - T0)) ms"
else
  if timeout 3 bash -c "cat < /dev/null > /dev/tcp/$V4/22" 2>/dev/null; then
    T1=$(date +%%s%%3N 2>/dev/null || echo 0)
    echo "TCP_PORT=$((T1 - T0)) ms (port 22)"
  else
    echo "TCP_PORT=BLOCKED"
  fi
fi

# 4. Kernel GRE / IP6GRE module support
modprobe ip_gre 2>/dev/null || true
modprobe ip6_gre 2>/dev/null || true
GRE_OK=$(lsmod 2>/dev/null | grep -q ip_gre && echo "READY" || echo "MISSING")
GRE6_OK=$(lsmod 2>/dev/null | grep -q ip6_gre && echo "READY" || echo "MISSING")
echo "GRE_MOD=$GRE_OK"
echo "IP6GRE_MOD=$GRE6_OK"

# 5. Installed tunnel binaries
BH=$([ -x /usr/local/bin/backhaul ] && echo "INSTALLED" || echo "AUTO_INSTALL")
RH=$([ -x /usr/local/bin/rathole ] && echo "INSTALLED" || echo "AUTO_INSTALL")
PQ=$([ -x /usr/local/bin/paqet ] && echo "INSTALLED" || echo "AUTO_INSTALL")
echo "BIN_BACKHAUL=$BH"
echo "BIN_RATHOLE=$RH"
echo "BIN_PAQET=$PQ"
`, sshclient.ShQuote(foreign.Host), sshclient.ShQuote(targetV6), req.TestPort)

	res := sshclient.Run(toSSHTarget(iran), cmd, 10*time.Second, 30*time.Second)
	if res.Err != nil && !sshclient.IsExitError(res.Err) {
		fail(w, http.StatusBadGateway, res.Err.Error())
		return
	}

	kv := map[string]string{}
	for _, ln := range strings.Split(res.Output, "\n") {
		p := strings.SplitN(strings.TrimSpace(ln), "=", 2)
		if len(p) == 2 {
			kv[p[0]] = p[1]
		}
	}

	recommended := "Backhaul (TCPMUX)"
	if kv["ICMP_V6"] != "" && kv["ICMP_V6"] != "FAIL" && kv["ICMP_V6"] != "NO_IPV6" && kv["IP6GRE_MOD"] == "READY" {
		recommended = "IP6GRE (Kernel IPv6) or Backhaul"
	} else if kv["TCP_PORT"] == "BLOCKED" {
		recommended = "Paqet (KCP/UDP)"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"iran":        iran.Name,
		"foreign":     foreign.Name,
		"matrix":      kv,
		"recommended": recommended,
	})
}

// ── MSmart: Multi-Tunnel Benchmark & Smart Failover ──────────────────────────

func (s *Server) handleTenantMSmartBenchmark(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	tunnels, err := s.engine.ListTunnels(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(tunnels) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": []any{}, "best_tunnel_id": 0})
		return
	}

	type benchRow struct {
		TunnelID int64  `json:"tunnel_id"`
		Name     string `json:"name"`
		Engine   string `json:"engine"`
		Status   string `json:"status"`
		PingMs   string `json:"ping_ms"`
		Score    int    `json:"score"`
	}

	results := make([]benchRow, len(tunnels))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for i, t := range tunnels {
		wg.Add(1)
		go func(idx int, tunID int64, name, eng string, irID, frID int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ir, err1 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, irID)
			fr, err2 := s.engine.GetServerWithSecret(r.Context(), claims.UserID, frID)
			if err1 != nil || err2 != nil {
				results[idx] = benchRow{TunnelID: tunID, Name: name, Engine: eng, Status: "error", PingMs: "N/A", Score: 0}
				return
			}
			unit := fmt.Sprintf("mcontrol-tun-%d.service", tunID)
			cmd := fmt.Sprintf(`ST=$(systemctl is-active %s 2>/dev/null || echo inactive)
P=$(ping -c 2 -W 2 %s 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%%.1f", $5; else print "9999"}')
echo "$ST|$P"`, unit, sshclient.ShQuote(fr.Host))

			res := sshclient.Run(toSSHTarget(ir), cmd, 8*time.Second, 15*time.Second)
			parts := strings.SplitN(strings.TrimSpace(res.Output), "|", 2)
			st := "inactive"
			pingStr := "timeout"
			score := 10
			if len(parts) == 2 {
				st = strings.TrimSpace(parts[0])
				pVal := strings.TrimSpace(parts[1])
				if pVal != "9999" && pVal != "" {
					pingStr = pVal + " ms"
					score = 80
					if st == "active" {
						score = 100
					}
				}
			}
			results[idx] = benchRow{
				TunnelID: tunID,
				Name:     name,
				Engine:   eng,
				Status:   st,
				PingMs:   pingStr,
				Score:    score,
			}
			_ = s.engine.UpdateTunnelRuntime(r.Context(), claims.UserID, tunID, st, "", pingStr)
		}(i, t.ID, t.Name, t.Engine, t.IranServerID, t.ForeignServerID)
	}
	wg.Wait()

	var bestID int64
	bestScore := -1
	for _, r := range results {
		if r.Score > bestScore {
			bestScore = r.Score
			bestID = r.TunnelID
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"results":        results,
		"best_tunnel_id": bestID,
	})
}

// ── APT Mirror & Network Optimizer (mirror.sh port) ──────────────────────────

func (s *Server) handleTenantServerMirror(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Mirror string `json:"mirror"` // arvan | iranserver | hetzner | default
	}
	if !readJSON(w, r, &req) {
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}

	var mirrorURL string
	switch req.Mirror {
	case "arvan":
		mirrorURL = "http://mirror.arvancloud.ir/ubuntu"
	case "iranserver":
		mirrorURL = "http://mirror.iranserver.com/ubuntu"
	case "hetzner":
		mirrorURL = "http://mirror.hetzner.com/ubuntu/packages"
	default:
		mirrorURL = "http://archive.ubuntu.com/ubuntu"
	}

	cmd := fmt.Sprintf(`if [ -f /etc/apt/sources.list ]; then
  cp /etc/apt/sources.list /etc/apt/sources.list.mc.bak 2>/dev/null || true
  sed -i -E 's|https?://[^/]+/ubuntu|%s|g' /etc/apt/sources.list
fi
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  cp /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list.d/ubuntu.sources.mc.bak 2>/dev/null || true
  sed -i -E 's|https?://[^/]+/ubuntu|%s|g' /etc/apt/sources.list.d/ubuntu.sources
fi
apt-get update -o Acquire::http::Timeout=10 2>&1 | tail -n 5
echo "MIRROR_OK: %s"`, mirrorURL, mirrorURL, mirrorURL)

	res := sshclient.Run(toSSHTarget(srv), cmd, 10*time.Second, 45*time.Second)
	okRes := res.Err == nil && strings.Contains(res.Output, "MIRROR_OK")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     okRes,
		"output": strings.TrimSpace(res.Output),
	})
}
