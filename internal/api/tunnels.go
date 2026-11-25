package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
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
		IPMode           string `json:"ip_mode"` // v4 | v6 | 6to4 | 6to6
		IranIPVer        string `json:"iran_ip_ver"`
		ForeignIPVer     string `json:"foreign_ip_ver"`
		Underlay         string `json:"underlay"`           // "" | "ip" | "gre" | "vxlan"
		UnderlayTunnelID int64  `json:"underlay_tunnel_id"` // 0 = auto-create, >0 = existing GRE/VXLAN tunnel ID
		Subnet           string `json:"subnet"`
		VIPTargets       string `json:"vip_targets"`
		LBMode           string `json:"lb_mode"` // none | roundrobin | random
		// Backhaul v0.9.3 advanced tuning & Anti-DPI options
		Preset          string `json:"preset"`           // balanced | many_users | gaming | streaming | max_capacity
		SNI             string `json:"sni"`
		UTLSFingerprint string `json:"utls_fingerprint"`
		EdgeIP          string `json:"edge_ip"`
		TLSFragment     bool   `json:"tls_fragment"`
		FallbackAddr    string `json:"fallback_addr"`
		XHTTPPath       string `json:"xhttp_path"`
		XHTTPHost       string `json:"xhttp_host"`
		XHTTPMode       string `json:"xhttp_mode"`
		KCPMode         string `json:"kcp_mode"`
		KCPCrypt        string `json:"kcp_crypt"`
		KCPNoFEC        bool   `json:"kcp_nofec"`
		KernelEncap   string `json:"kernel_encap"`    // "" | "fou"
		KernelIPsec   string `json:"kernel_ipsec"`    // "" | "aes-gcm-128" | "aes-gcm-256" | "chacha20"
		ForwardMode   string `json:"forward_mode"`    // "nat" | "transparent"
		MTU           int    `json:"mtu"`             // 0 = auto (1420), or explicit 1280..1500
		RateLimitMbps int    `json:"rate_limit_mbps"` // 0 = unlimited, >0 = tc CAKE/TBF cap
		// Phase 3 Backhaul v1.0.0 options (Parallax, Multi-Transport Failover, Game Mode)
		ParallaxLanes      int    `json:"parallax_lanes"`
		ParallaxStallSec   int    `json:"parallax_stall_sec"`
		FailoverTransports string `json:"failover_transports"`
		FailoverRestoreSec int    `json:"failover_restore_sec"`
		GameMode           bool   `json:"game_mode"`
		GameCopies         int    `json:"game_copies"`
		GamePorts          string `json:"game_ports"`
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
	if !safeLegacyName.MatchString(req.Name) {
		fail(w, http.StatusBadRequest, "tunnel name must be 1-64 chars: letters, numbers, dot, underscore, hyphen only")
		return
	}
	if req.IranServerID <= 0 || req.ForeignServerID <= 0 || req.IranServerID == req.ForeignServerID {
		fail(w, http.StatusBadRequest, "select two distinct servers (Iran & Foreign)")
		return
	}
	switch req.Engine {
	case "backhaul", "rathole", "paqet", "gre", "ip6gre", "ipip", "vxlan", "wireguard":
	default:
		fail(w, http.StatusBadRequest, "unsupported engine (backhaul, rathole, paqet, gre, ip6gre, ipip, vxlan, wireguard)")
		return
	}
	if req.TunnelPort <= 0 || req.TunnelPort > 65535 {
		req.TunnelPort = 3080
	}
	if req.Token == "" {
		req.Token = randTunnelToken()
	} else if !safeLegacyName.MatchString(req.Token) {
		fail(w, http.StatusBadRequest, "tunnel token must contain only letters, numbers, dot, underscore, hyphen")
		return
	}
	if req.Transport == "" {
		req.Transport = "tcp"
	}
	if req.Engine == "backhaul" || req.Engine == "rathole" || req.Engine == "paqet" {
		if req.IranIPVer == "v6" {
			req.IPMode = "v6"
		} else if req.IPMode != "v6" {
			req.IPMode = "v4"
		}
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
	if req.Engine == "gre" || req.Engine == "ip6gre" || req.Engine == "ipip" || req.Engine == "vxlan" || req.Engine == "wireguard" {
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

	req.Underlay = strings.ToLower(strings.TrimSpace(req.Underlay))
	if (req.Engine == "backhaul" || req.Engine == "rathole" || req.Engine == "paqet") && (req.Underlay == "gre" || req.Underlay == "vxlan") {
		var baseSub string
		if req.UnderlayTunnelID > 0 {
			uTun, uErr := h.store.GetTunnel(req.UnderlayTunnelID)
			if uErr != nil {
				fail(w, http.StatusNotFound, "Selected underlying GRE/VXLAN tunnel not found")
				return
			}
			baseSub = uTun.Subnet
			if baseSub == "" {
				baseSub = fmt.Sprintf("10.10.%d", (uTun.ID%240)+10)
			}
		} else {
			// Auto-create underlying GRE or VXLAN carrier tunnel between Iran & Foreign
			uEng := req.Underlay
			uMode := "v4"
			if req.IranIPVer == "v6" || req.ForeignIPVer == "v6" {
				if uEng == "gre" {
					uEng = "ip6gre"
					uMode = "6to4"
				} else {
					uMode = "v6"
				}
			}
			uPort := req.TunnelPort + 100
			if uPort > 65000 {
				uPort = req.TunnelPort - 100
			}
			uTun := &db.Tunnel{
				Name:            req.Name + "-" + strings.ToUpper(req.Underlay),
				Engine:          uEng,
				Transport:       "raw@" + uMode,
				IranServerID:    req.IranServerID,
				ForeignServerID: req.ForeignServerID,
				TunnelPort:      uPort,
				Token:           randTunnelToken(),
				Ports:           strconv.Itoa(req.TunnelPort),
				Forwarder:       "iptables",
				LBMode:          "none",
				Status:          "deploying",
			}
			uID, uErr := h.store.AddTunnel(uTun)
			if uErr != nil {
				fail(w, http.StatusInternalServerError, "failed to create underlay tunnel: "+uErr.Error())
				return
			}
			uTun.ID = uID
			uTun.Subnet = fmt.Sprintf("10.10.%d", (uID%240)+10)
			baseSub = uTun.Subnet
			uIranCmd, uForCmd := buildTunnelDeployScripts(uTun, iranSrv, forSrv, []int{req.TunnelPort})
			var uwg sync.WaitGroup
			var urI, urF sshclient.Result
			uwg.Add(2)
			go func() { defer uwg.Done(); urI = h.runOnServer(iranSrv, uIranCmd, 45*time.Second) }()
			go func() { defer uwg.Done(); urF = h.runOnServer(forSrv, uForCmd, 45*time.Second) }()
			uwg.Wait()
			if urI.Err != nil || urF.Err != nil {
				_ = h.store.UpdateTunnelStatus(uID, "error", urI.Output+" "+urF.Output)
				fail(w, http.StatusBadGateway, "underlying "+strings.ToUpper(req.Underlay)+" tunnel failed to deploy")
				return
			}
			_ = h.store.UpdateTunnelStatus(uID, "active", "")
		}
		req.Subnet = baseSub
		baseT, _ := splitTransportIPMode(req.Transport)
		req.Transport = baseT + "@" + req.Underlay
	}

	if req.Engine == "backhaul" {
		baseT, suffix := splitTransportIPMode(req.Transport)
		if baseT == "tcp+tls" {
			baseT = "tls"
			if suffix != "" {
				req.Transport = baseT + "@" + suffix
			} else {
				req.Transport = baseT
			}
		}
	}
	extraObj := backhaulExtraOpts{
		Preset:          strings.TrimSpace(req.Preset),
		SNI:             strings.TrimSpace(req.SNI),
		UTLSFingerprint: strings.TrimSpace(req.UTLSFingerprint),
		EdgeIP:          strings.TrimSpace(req.EdgeIP),
		TLSFragment:     req.TLSFragment,
		FallbackAddr:    strings.TrimSpace(req.FallbackAddr),
		XHTTPPath:       strings.TrimSpace(req.XHTTPPath),
		XHTTPHost:       strings.TrimSpace(req.XHTTPHost),
		XHTTPMode:       strings.TrimSpace(req.XHTTPMode),
		KCPMode:         strings.TrimSpace(req.KCPMode),
		KCPCrypt:        strings.TrimSpace(req.KCPCrypt),
		KCPNoFEC:        req.KCPNoFEC,
		KernelEncap:     strings.TrimSpace(req.KernelEncap),
		KernelIPsec:     strings.TrimSpace(req.KernelIPsec),
		ForwardMode:     strings.TrimSpace(req.ForwardMode),
		MTU:             req.MTU,
		RateLimitMbps:      req.RateLimitMbps,
		ParallaxLanes:      req.ParallaxLanes,
		ParallaxStallSec:   req.ParallaxStallSec,
		FailoverTransports: strings.TrimSpace(req.FailoverTransports),
		FailoverRestoreSec: req.FailoverRestoreSec,
		GameMode:           req.GameMode,
		GameCopies:         req.GameCopies,
		GamePorts:          strings.TrimSpace(req.GamePorts),
	}
	extraBytes, _ := json.Marshal(extraObj)

	tun := &db.Tunnel{
		Name:            req.Name,
		Engine:          req.Engine,
		Transport:       req.Transport,
		ExtraJSON:       string(extraBytes),
		IranServerID:    req.IranServerID,
		ForeignServerID: req.ForeignServerID,
		TunnelPort:      req.TunnelPort,
		Token:           req.Token,
		Ports:           normPorts,
		Forwarder:       req.Forwarder,
		Subnet:          strings.TrimSpace(req.Subnet),
		VIPTargets:      strings.TrimSpace(req.VIPTargets),
		LBMode:          strings.TrimSpace(req.LBMode),
		Status:          "deploying",
	}
	id, err := h.store.AddTunnel(tun)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tun.ID = id
	if tun.Subnet == "" {
		tun.Subnet = fmt.Sprintf("10.10.%d", (id%240)+10)
	}
	switch tun.LBMode {
	case "roundrobin", "random", "lowest_ping", "lightest", "least_traffic":
	default:
		tun.LBMode = "none"
	}

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
	} else if (ipMode == "gre" || ipMode == "vxlan") && t.Subnet != "" {
		iranAddr = t.Subnet + ".1"
		bindWild = t.Subnet + ".1"
	}

	switch t.Engine {
	case "backhaul":
		if baseTrans == "tcp+tls" {
			baseTrans = "tls"
		}
		var ex backhaulExtraOpts
		if strings.TrimSpace(t.ExtraJSON) != "" {
			_ = json.Unmarshal([]byte(t.ExtraJSON), &ex)
		}
		// Apply hardware/traffic performance preset
		connPool, muxCon, chSize, hbSec, kaSec, rcvBuf, strBuf := 8, 8, 2048, 10, 20, 4194304, 2097152
		switch ex.Preset {
		case "many_users":
			connPool, muxCon, chSize, rcvBuf, strBuf = 12, 16, 4096, 8388608, 2097152
		case "gaming":
			connPool, muxCon, chSize, hbSec, kaSec, strBuf = 4, 4, 1024, 5, 10, 1048576
		case "streaming":
			connPool, muxCon, chSize, rcvBuf, strBuf = 10, 8, 4096, 8388608, 4194304
		case "max_capacity":
			connPool, muxCon, chSize, rcvBuf, strBuf = 16, 24, 8192, 16777216, 4194304
		}

		var portLines []string
		for _, p := range ports {
			portLines = append(portLines, fmt.Sprintf(`"%d=%d"`, p, p))
		}
		var srvExtra, cliExtra strings.Builder
		if strings.HasSuffix(baseTrans, "mux") {
			fmt.Fprintf(&srvExtra, "mux_con = %d\nmux_version = 2\nmux_framesize = 32768\nmux_recievebuffer = %d\nmux_streambuffer = %d\n", muxCon, rcvBuf, strBuf)
			fmt.Fprintf(&cliExtra, "mux_version = 2\nmux_framesize = 32768\nmux_recievebuffer = %d\nmux_streambuffer = %d\n", rcvBuf, strBuf)
		}
		isTLS := baseTrans == "tls" || baseTrans == "tlsmux" || baseTrans == "wss" || baseTrans == "wssmux" || baseTrans == "xhttps" || baseTrans == "xhttpsmux"
		if isTLS {
			sni := ex.SNI
			if sni == "" {
				sni = "www.cloudflare.com"
			}
			fp := ex.UTLSFingerprint
			if fp == "" {
				fp = "chrome"
			}
			fmt.Fprintf(&srvExtra, "sni = %q\n", sni)
			fmt.Fprintf(&cliExtra, "sni = %q\nutls_fingerprint = %q\n", sni, fp)
			if ex.TLSFragment {
				cliExtra.WriteString("tls_fragment = true\n")
			}
		}
		if ex.EdgeIP != "" {
			fmt.Fprintf(&cliExtra, "edge_ip = %q\n", ex.EdgeIP)
		}
		if strings.HasPrefix(baseTrans, "xhttp") {
			xp := ex.XHTTPPath
			if xp == "" {
				xp = "/api/v1/stream"
			}
			xm := ex.XHTTPMode
			if xm == "" {
				xm = "auto"
			}
			fmt.Fprintf(&srvExtra, "xhttp_path = %q\nxhttp_mode = %q\n", xp, xm)
			fmt.Fprintf(&cliExtra, "xhttp_path = %q\nxhttp_mode = %q\n", xp, xm)
			if ex.XHTTPHost != "" {
				fmt.Fprintf(&srvExtra, "xhttp_host = %q\n", ex.XHTTPHost)
				fmt.Fprintf(&cliExtra, "xhttp_host = %q\n", ex.XHTTPHost)
			}
			if ex.FallbackAddr != "" {
				fmt.Fprintf(&srvExtra, "fallback_addr = %q\n", ex.FallbackAddr)
			}
		}
		if baseTrans == "kcp" || baseTrans == "kcpmux" {
			km := ex.KCPMode
			if km == "" {
				if ex.Preset == "gaming" {
					km = "fast3"
				} else {
					km = "fast2"
				}
			}
			kc := ex.KCPCrypt
			if kc == "" {
				kc = "aes"
			}
			fmt.Fprintf(&srvExtra, "kcp_mode = %q\nkcp_crypt = %q\nkcp_nofec = %v\n", km, kc, ex.KCPNoFEC)
			fmt.Fprintf(&cliExtra, "kcp_mode = %q\nkcp_crypt = %q\nkcp_nofec = %v\n", km, kc, ex.KCPNoFEC)
		}
		if baseTrans == "parallax" || baseTrans == "parallaxmux" || baseTrans == "failover" {
			lanes := ex.ParallaxLanes
			if lanes < 2 || lanes > 12 {
				lanes = 4
			}
			stall := ex.ParallaxStallSec
			if stall < 2 || stall > 30 {
				stall = 4
			}
			fmt.Fprintf(&srvExtra, "parallax_lanes = %d\nparallax_stall_sec = %d\n", lanes, stall)
			fmt.Fprintf(&cliExtra, "parallax_lanes = %d\nparallax_stall_sec = %d\n", lanes, stall)
		}
		if baseTrans == "failover" {
			rawList := ex.FailoverTransports
			if rawList == "" {
				rawList = "tlsmux,xhttpsmux,parallaxmux"
			}
			var qItems []string
			for _, p := range strings.Split(rawList, ",") {
				v := strings.ToLower(strings.TrimSpace(p))
				if v != "" && v != "failover" {
					qItems = append(qItems, fmt.Sprintf("%q", v))
				}
			}
			if len(qItems) < 2 {
				qItems = []string{`"tlsmux"`, `"xhttpsmux"`}
			}
			restoreSec := ex.FailoverRestoreSec
			if restoreSec < 30 {
				restoreSec = 300
			}
			fmt.Fprintf(&srvExtra, "failover_transports = [%s]\nfailover_probe_sec = 5\nfailover_restore_sec = %d\n", strings.Join(qItems, ", "), restoreSec)
			fmt.Fprintf(&cliExtra, "failover_transports = [%s]\n", strings.Join(qItems, ", "))
			// Also include TLS/uTLS & XHTTP defaults so sub-carriers in failover work out of the box
			sni := ex.SNI
			if sni == "" {
				sni = "www.cloudflare.com"
			}
			fp := ex.UTLSFingerprint
			if fp == "" {
				fp = "chrome"
			}
			fmt.Fprintf(&srvExtra, "sni = %q\nxhttp_path = \"/api/v1/stream\"\n", sni)
			fmt.Fprintf(&cliExtra, "sni = %q\nutls_fingerprint = %q\nxhttp_path = \"/api/v1/stream\"\n", sni, fp)
			if ex.TLSFragment {
				cliExtra.WriteString("tls_fragment = true\n")
			}
		}
		if ex.GameMode {
			copies := ex.GameCopies
			if copies < 1 || copies > 4 {
				copies = 3
			}
			gBase := t.TunnelPort + 10
			var gPorts []string
			srcGP := ex.GamePorts
			if srcGP == "" {
				srcGP = t.Ports
			}
			for _, raw := range strings.Split(srcGP, ",") {
				raw = strings.TrimSpace(raw)
				if raw != "" {
					if strings.Contains(raw, "=") {
						gPorts = append(gPorts, fmt.Sprintf("%q", raw))
					} else {
						gPorts = append(gPorts, fmt.Sprintf(`"%s=%s"`, raw, raw))
					}
				}
			}
			if len(gPorts) > 0 {
				fmt.Fprintf(&srvExtra, "game_mode = true\ngame_copies = %d\ngame_base_port = %d\ngame_ports = [%s]\n", copies, gBase, strings.Join(gPorts, ", "))
				fmt.Fprintf(&cliExtra, "game_mode = true\ngame_copies = %d\ngame_base_port = %d\n", copies, gBase)
			}
		}

		iranToml := fmt.Sprintf(`[server]
bind_addr = "%s:%d"
transport = "%s"
token = "%s"
nodelay = true
keepalive_period = %d
heartbeat = %d
channel_size = %d
sniffer = false
%sports = [%s]
`, bindWild, t.TunnelPort, baseTrans, t.Token, kaSec, hbSec, chSize, srvExtra.String(), strings.Join(portLines, ", "))

		forToml := fmt.Sprintf(`[client]
remote_addr = "%s"
transport = "%s"
token = "%s"
connection_pool = %d
aggressive_pool = true
retry_interval = 1
dial_timeout = 10
nodelay = true
keepalive_period = %d
%s`, formatHostPort(iranAddr, t.TunnelPort), baseTrans, t.Token, connPool, kaSec, cliExtra.String())

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
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=$BIN -c %s/%s.toml
Restart=always
RestartSec=1
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
			srvSvcs.WriteString(fmt.Sprintf("\n[server.services.p%d]\ntype = \"tcp\"\nbind_addr = \"0.0.0.0:%d\"\nnodelay = true\n", p, p))
			cliSvcs.WriteString(fmt.Sprintf("\n[client.services.p%d]\ntype = \"tcp\"\nlocal_addr = \"127.0.0.1:%d\"\nnodelay = true\n", p, p))
		}
		iranToml := fmt.Sprintf(`[server]
bind_addr = "%s:%d"
default_token = "%s"
heartbeat_interval = 15

[server.transport]
type = "tcp"

[server.transport.tcp]
nodelay = true
keepalive_secs = 15
keepalive_interval = 5
%s`, bindWild, t.TunnelPort, t.Token, srvSvcs.String())

		forToml := fmt.Sprintf(`[client]
remote_addr = "%s"
default_token = "%s"
heartbeat_timeout = 40
retry_interval = 1

[client.transport]
type = "tcp"

[client.transport.tcp]
nodelay = true
keepalive_secs = 15
keepalive_interval = 5
%s`, formatHostPort(iranAddr, t.TunnelPort), t.Token, cliSvcs.String())

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
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=$BIN %s/%s.toml
Restart=always
RestartSec=1
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
		// Real Paqet uses YAML configs + `paqet run -c <file.yaml>` + raw NOTRACK & mangle RST DROP rules.
		// Foreign (Exit) runs role: "server" on TunnelPort; Iran (Entry) runs role: "client" with forward entries.
		tag := fmt.Sprintf("mc_tun_%d", t.ID)
		yamlPath := fmt.Sprintf("%s/%s.yaml", cfgDir, unit)

		var fwdYaml strings.Builder
		var iranIpt strings.Builder
		for _, p := range ports {
			fwdYaml.WriteString(fmt.Sprintf("  - listen: \"0.0.0.0:%d\"\n    target: \"127.0.0.1:%d\"\n    protocol: \"tcp\"\n", p, p))
			iranIpt.WriteString(fmt.Sprintf(`iptables -w 5 -t raw -C PREROUTING -p tcp --dport %d -j NOTRACK -m comment --comment %s 2>/dev/null || iptables -w 5 -t raw -A PREROUTING -p tcp --dport %d -j NOTRACK -m comment --comment %s 2>/dev/null || true
iptables -w 5 -t raw -C OUTPUT -p tcp --sport %d -j NOTRACK -m comment --comment %s 2>/dev/null || iptables -w 5 -t raw -A OUTPUT -p tcp --sport %d -j NOTRACK -m comment --comment %s 2>/dev/null || true
iptables -w 5 -t mangle -C OUTPUT -p tcp --sport %d --tcp-flags RST RST -j DROP -m comment --comment %s 2>/dev/null || iptables -w 5 -t mangle -A OUTPUT -p tcp --sport %d --tcp-flags RST RST -j DROP -m comment --comment %s 2>/dev/null || true
`, p, tag, p, tag, p, tag, p, tag, p, tag, p, tag))
		}

		// Target address for Iran client to reach Foreign server (handles Direct IP or Over GRE/VXLAN)
		paqetServerHost := forAddr
		if (ipMode == "gre" || ipMode == "vxlan") && t.Subnet != "" {
			paqetServerHost = t.Subnet + ".2"
		}

		forCmd := fmt.Sprintf(`set -e
BIN=$(command -v paqet 2>/dev/null || true)
[ -n "$BIN" ] || { echo "Paqet binary not installed! Run Setup -> Install Static Bundle first." >&2; exit 1; }
mkdir -p %s
IFACE=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1);exit}}')
LIP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')
GW=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="via"){print $(i+1);exit}}')
[ -n "$GW" ] && ping -c 1 -W 1 "$GW" >/dev/null 2>&1 || true
MAC=$(ip neigh show "$GW" dev "$IFACE" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="lladdr"){print $(i+1);exit}}')
[ -n "$MAC" ] || MAC=$(ip neigh show dev "$IFACE" 2>/dev/null | awk '/lladdr/{print $5; exit}')
[ -n "$IFACE" ] && [ -n "$LIP" ] && [ -n "$MAC" ] || { echo "Could not detect network interface/IP/gateway MAC for Paqet" >&2; exit 1; }
cat <<EOF > %s
role: "server"
log:
  level: "info"
listen:
  addr: ":%d"
network:
  interface: "${IFACE}"
  ipv4:
    addr: "${LIP}:%d"
    router_mac: "${MAC}"
  tcp:
    local_flag: ["PA"]
transport:
  protocol: "kcp"
  conn: 4
  kcp:
    key: "%s"
    mode: "fast"
    block: "aes-128-gcm"
    mtu: 1350
EOF
chmod 600 %s
iptables -w 5 -t raw -C PREROUTING -p tcp --dport %d -j NOTRACK -m comment --comment %s 2>/dev/null || iptables -w 5 -t raw -A PREROUTING -p tcp --dport %d -j NOTRACK -m comment --comment %s 2>/dev/null || true
iptables -w 5 -t raw -C OUTPUT -p tcp --sport %d -j NOTRACK -m comment --comment %s 2>/dev/null || iptables -w 5 -t raw -A OUTPUT -p tcp --sport %d -j NOTRACK -m comment --comment %s 2>/dev/null || true
iptables -w 5 -t mangle -C OUTPUT -p tcp --sport %d --tcp-flags RST RST -j DROP -m comment --comment %s 2>/dev/null || iptables -w 5 -t mangle -A OUTPUT -p tcp --sport %d --tcp-flags RST RST -j DROP -m comment --comment %s 2>/dev/null || true
cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Paqet Server %d (%s)
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=root
ExecStart=$BIN run -c %s
Restart=always
RestartSec=1
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable %s.service && systemctl restart %s.service && sleep 1 && (systemctl is-active %s.service || true)
`, cfgDir, yamlPath, t.TunnelPort, t.TunnelPort, t.Token, yamlPath,
			t.TunnelPort, tag, t.TunnelPort, tag, t.TunnelPort, tag, t.TunnelPort, tag, t.TunnelPort, tag, t.TunnelPort, tag,
			unit, t.ID, t.Name, yamlPath, unit, unit, unit)

		iranCmd := fmt.Sprintf(`set -e
BIN=$(command -v paqet 2>/dev/null || true)
[ -n "$BIN" ] || { echo "Paqet binary not installed! Run Setup -> Install Static Bundle first." >&2; exit 1; }
mkdir -p %s
IFACE=$(ip -4 route get %s 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1);exit}}')
LIP=$(ip -4 route get %s 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')
GW=$(ip -4 route get %s 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="via"){print $(i+1);exit}}')
[ -n "$GW" ] && ping -c 1 -W 1 "$GW" >/dev/null 2>&1 || true
MAC=$(ip neigh show "$GW" dev "$IFACE" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="lladdr"){print $(i+1);exit}}')
[ -n "$MAC" ] || MAC=$(ip neigh show dev "$IFACE" 2>/dev/null | awk '/lladdr/{print $5; exit}')
[ -n "$IFACE" ] && [ -n "$LIP" ] && [ -n "$MAC" ] || { echo "Could not detect network interface/IP/gateway MAC for Paqet client" >&2; exit 1; }
cat <<EOF > %s
role: "client"
log:
  level: "info"
forward:
%snetwork:
  interface: "${IFACE}"
  ipv4:
    addr: "${LIP}:0"
    router_mac: "${MAC}"
  tcp:
    local_flag: ["PA"]
    remote_flag: ["PA"]
server:
  addr: "%s"
transport:
  protocol: "kcp"
  conn: 4
  kcp:
    key: "%s"
    mode: "fast"
    block: "aes-128-gcm"
    mtu: 1350
EOF
chmod 600 %s
%scat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl Paqet Client %d (%s)
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
User=root
ExecStart=$BIN run -c %s
Restart=always
RestartSec=1
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload && systemctl enable %s.service && systemctl restart %s.service && sleep 1 && (systemctl is-active %s.service || true)
`, cfgDir, sshclient.ShQuote(paqetServerHost), sshclient.ShQuote(paqetServerHost), sshclient.ShQuote(paqetServerHost),
			yamlPath, fwdYaml.String(), formatHostPort(paqetServerHost, t.TunnelPort), t.Token, yamlPath, iranIpt.String(),
			unit, t.ID, t.Name, yamlPath, unit, unit, unit)
		return iranCmd, forCmd

	default:
		var ex backhaulExtraOpts
		if strings.TrimSpace(t.ExtraJSON) != "" {
			_ = json.Unmarshal([]byte(t.ExtraJSON), &ex)
		}
		ifName := fmt.Sprintf("mctun%d", t.ID)
		iranLocal := t.Subnet + ".1/30"
		forLocal := t.Subnet + ".2/30"
		forPeerIP := t.Subnet + ".2"
		tag := fmt.Sprintf("mc_tun_%d", t.ID)
		mtuVal := ex.MTU
		if mtuVal < 1200 || mtuVal > 1500 {
			mtuVal = 1420
			if ex.KernelIPsec != "" || t.Engine == "wireguard" {
				mtuVal = 1380
			}
		}
		useFOU := ex.KernelEncap == "fou" && (t.Engine == "gre" || t.Engine == "ipip") && !strings.Contains(iranAddr, ":")
		fouPort := t.TunnelPort
		if fouPort <= 0 {
			fouPort = 5500 + int(t.ID%500)
		}

		var iranLink, forLink, iranPre, forPre, iranPost, forPost string
		switch t.Engine {
		case "vxlan":
			vni := 100 + t.ID
			iranLink = fmt.Sprintf("ip link add %s type vxlan id %d remote %s local %s dstport %d", ifName, vni, forAddr, iranAddr, t.TunnelPort)
			forLink = fmt.Sprintf("ip link add %s type vxlan id %d remote %s local %s dstport %d", ifName, vni, iranAddr, forAddr, t.TunnelPort)
		case "wireguard":
			k1 := sha256.Sum256([]byte("mc-wg-ir:" + t.Token))
			k1[0] &= 248
			k1[31] &= 127
			k1[31] |= 64
			k2 := sha256.Sum256([]byte("mc-wg-fr:" + t.Token))
			k2[0] &= 248
			k2[31] &= 127
			k2[31] |= 64
			irPrivB64 := base64.StdEncoding.EncodeToString(k1[:])
			frPrivB64 := base64.StdEncoding.EncodeToString(k2[:])
			iranLink = fmt.Sprintf(`command -v wg >/dev/null 2>&1 || (apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard-tools) >/dev/null 2>&1 || dnf install -y -q wireguard-tools >/dev/null 2>&1 || true
ip link add %s type wireguard
printf '%%s\n' %s > /etc/mcontrol/tunnels/%s.key && chmod 600 /etc/mcontrol/tunnels/%s.key
FR_PUB=$(printf '%%s\n' %s | wg pubkey)
wg set %s private-key /etc/mcontrol/tunnels/%s.key listen-port %d peer "$FR_PUB" endpoint %s allowed-ips 0.0.0.0/0,::/0 persistent-keepalive 20`,
				ifName, sshclient.ShQuote(irPrivB64), unit, unit, sshclient.ShQuote(frPrivB64), ifName, unit, t.TunnelPort, formatHostPort(forAddr, t.TunnelPort))
			forLink = fmt.Sprintf(`command -v wg >/dev/null 2>&1 || (apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq wireguard-tools) >/dev/null 2>&1 || dnf install -y -q wireguard-tools >/dev/null 2>&1 || true
ip link add %s type wireguard
printf '%%s\n' %s > /etc/mcontrol/tunnels/%s.key && chmod 600 /etc/mcontrol/tunnels/%s.key
IR_PUB=$(printf '%%s\n' %s | wg pubkey)
wg set %s private-key /etc/mcontrol/tunnels/%s.key listen-port %d peer "$IR_PUB" endpoint %s allowed-ips 0.0.0.0/0,::/0 persistent-keepalive 20`,
				ifName, sshclient.ShQuote(frPrivB64), unit, unit, sshclient.ShQuote(irPrivB64), ifName, unit, t.TunnelPort, formatHostPort(iranAddr, t.TunnelPort))
		case "ipip":
			if useFOU {
				iranPre = fmt.Sprintf("modprobe fou 2>/dev/null || true\nip fou add port %d ipproto 4 2>/dev/null || true\n", fouPort)
				forPre = iranPre
				iranLink = fmt.Sprintf("ip link add %s type ipip remote %s local %s ttl 255 encap fou encap-sport auto encap-dport %d", ifName, foreign.Host, iran.Host, fouPort)
				forLink = fmt.Sprintf("ip link add %s type ipip remote %s local %s ttl 255 encap fou encap-sport auto encap-dport %d", ifName, iran.Host, foreign.Host, fouPort)
			} else {
				iranLink = fmt.Sprintf("ip tunnel add %s mode ipip remote %s local %s ttl 255", ifName, foreign.Host, iran.Host)
				forLink = fmt.Sprintf("ip tunnel add %s mode ipip remote %s local %s ttl 255", ifName, iran.Host, foreign.Host)
			}
		default:
			if t.Engine == "ip6gre" || ipMode == "6to4" || ipMode == "6to6" {
				iranLink = fmt.Sprintf("ip -6 tunnel add %s mode ip6gre remote %s local %s key %d ttl 255", ifName, forAddr, iranAddr, t.ID)
				forLink = fmt.Sprintf("ip -6 tunnel add %s mode ip6gre remote %s local %s key %d ttl 255", ifName, iranAddr, forAddr, t.ID)
			} else if useFOU {
				iranPre = fmt.Sprintf("modprobe fou 2>/dev/null || true\nip fou add port %d ipproto 47 2>/dev/null || true\n", fouPort)
				forPre = iranPre
				iranLink = fmt.Sprintf("ip link add %s type gre remote %s local %s key %d ttl 255 encap fou encap-sport auto encap-dport %d", ifName, foreign.Host, iran.Host, t.ID, fouPort)
				forLink = fmt.Sprintf("ip link add %s type gre remote %s local %s key %d ttl 255 encap fou encap-sport auto encap-dport %d", ifName, iran.Host, foreign.Host, t.ID, fouPort)
			} else {
				iranLink = fmt.Sprintf("ip tunnel add %s mode gre remote %s local %s key %d ttl 255", ifName, foreign.Host, iran.Host, t.ID)
				forLink = fmt.Sprintf("ip tunnel add %s mode gre remote %s local %s key %d ttl 255", ifName, iran.Host, foreign.Host, t.ID)
			}
		}

		if ex.KernelIPsec != "" && t.Engine != "wireguard" {
			ipsecSetup := func(localIP, remoteIP string, spiOut, spiIn uint32) string {
				kOut := sha256.Sum256([]byte(fmt.Sprintf("mc-xfrm:%s:%d", t.Token, spiOut)))
				kIn := sha256.Sum256([]byte(fmt.Sprintf("mc-xfrm:%s:%d", t.Token, spiIn)))
				aeadOut, aeadIn := "", ""
				switch ex.KernelIPsec {
				case "aes-gcm-256":
					aeadOut = fmt.Sprintf("aead 'rfc4106(gcm(aes))' 0x%x%08x 128", kOut[:], spiOut)
					aeadIn = fmt.Sprintf("aead 'rfc4106(gcm(aes))' 0x%x%08x 128", kIn[:], spiIn)
				case "chacha20":
					aeadOut = fmt.Sprintf("aead 'rfc7539esp(chacha20,poly1305)' 0x%x%08x 128", kOut[:], spiOut)
					aeadIn = fmt.Sprintf("aead 'rfc7539esp(chacha20,poly1305)' 0x%x%08x 128", kIn[:], spiIn)
				default:
					aeadOut = fmt.Sprintf("aead 'rfc4106(gcm(aes))' 0x%x%08x 128", kOut[:16], spiOut)
					aeadIn = fmt.Sprintf("aead 'rfc4106(gcm(aes))' 0x%x%08x 128", kIn[:16], spiIn)
				}
				xproto := "gre"
				if t.Engine == "ipip" {
					xproto = "ipencap"
				} else if t.Engine == "vxlan" || useFOU {
					xproto = "udp"
				}
				return fmt.Sprintf(`modprobe esp4 esp6 xfrm_user 2>/dev/null || true
ip xfrm state delete src %s dst %s proto esp spi 0x%x 2>/dev/null || true
ip xfrm state delete src %s dst %s proto esp spi 0x%x 2>/dev/null || true
ip xfrm policy delete src %s dst %s proto %s dir out 2>/dev/null || true
ip xfrm policy delete src %s dst %s proto %s dir in 2>/dev/null || true
ip xfrm state add src %s dst %s proto esp spi 0x%x reqid %d mode transport %s
ip xfrm state add src %s dst %s proto esp spi 0x%x reqid %d mode transport %s
ip xfrm policy add src %s dst %s proto %s dir out tmpl src %s dst %s proto esp reqid %d mode transport
ip xfrm policy add src %s dst %s proto %s dir in tmpl src %s dst %s proto esp reqid %d mode transport
`, localIP, remoteIP, spiOut, remoteIP, localIP, spiIn,
					localIP, remoteIP, xproto, remoteIP, localIP, xproto,
					localIP, remoteIP, spiOut, t.ID, aeadOut,
					remoteIP, localIP, spiIn, t.ID, aeadIn,
					localIP, remoteIP, xproto, localIP, remoteIP, t.ID,
					remoteIP, localIP, xproto, remoteIP, localIP, t.ID)
			}
			spiA := uint32(0x10000 + t.ID*2)
			spiB := uint32(0x10000 + t.ID*2 + 1)
			iranPre += ipsecSetup(iranAddr, forAddr, spiA, spiB)
			forPre += ipsecSetup(forAddr, iranAddr, spiB, spiA)
		}

		if ex.RateLimitMbps > 0 {
			shapeCmd := fmt.Sprintf(`tc qdisc del dev %s root 2>/dev/null || true
tc qdisc replace dev %s root cake bandwidth %dmbit besteffort nat 2>/dev/null \
  || (tc qdisc replace dev %s root handle 1: tbf rate %dmbit burst 64kbit latency 40ms && tc qdisc replace dev %s parent 1:1 fq_codel) 2>/dev/null || true
`, ifName, ifName, ex.RateLimitMbps, ifName, ex.RateLimitMbps, ifName)
			iranPost += shapeCmd
			forPost += shapeCmd
		}

		// Build target VIP list for single-peer or multi-VIP load balancing
		var targets []string
		if strings.TrimSpace(t.VIPTargets) != "" {
			for _, raw := range strings.Split(t.VIPTargets, ",") {
				ip := strings.TrimSpace(raw)
				if ip != "" {
					targets = append(targets, ip)
				}
			}
		}
		if len(targets) == 0 {
			targets = []string{forPeerIP}
		}
		var iptRules strings.Builder
		for _, p := range ports {
			for _, proto := range []string{"tcp", "udp"} {
				if len(targets) == 1 || t.LBMode == "" || t.LBMode == "none" {
					iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -m comment --comment %s -j DNAT --to-destination %s:%d\n", proto, p, tag, targets[0], p))
				} else {
					n := len(targets)
					for i, dst := range targets {
						rem := n - i
						if rem == 1 {
							iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -m comment --comment %s -j DNAT --to-destination %s:%d\n", proto, p, tag, dst, p))
						} else if t.LBMode == "random" {
							prob := 1.0 / float64(rem)
							iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -m statistic --mode random --probability %.4f -m comment --comment %s -j DNAT --to-destination %s:%d\n", proto, p, prob, tag, dst, p))
						} else if t.LBMode == "lowest_ping" || t.LBMode == "lightest" || t.LBMode == "least_traffic" {
							// Smart target selection evaluated dynamically at startup via $BEST_VIP
							if i == 0 {
								iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -m comment --comment %s -j DNAT --to-destination ${BEST_VIP}:%d\n", proto, p, tag, p))
							}
							break
						} else {
							iptRules.WriteString(fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -m statistic --mode nth --every %d --packet 0 -m comment --comment %s -j DNAT --to-destination %s:%d\n", proto, p, rem, tag, dst, p))
						}
					}
				}
			}
		}
		if ex.ForwardMode != "transparent" {
			iptRules.WriteString(fmt.Sprintf("iptables -t nat -A POSTROUTING -o %s -m comment --comment %s -j MASQUERADE\n", ifName, tag))
		} else {
			markID := int(100 + (t.ID % 200))
			forPost += fmt.Sprintf(`iptables -t mangle -A PREROUTING -i %s -m conntrack --ctstate NEW -m comment --comment %s -j CONNMARK --set-mark %d
iptables -t mangle -A OUTPUT -m connmark --mark %d -m comment --comment %s -j CONNMARK --restore-mark
iptables -t mangle -A PREROUTING -m connmark --mark %d -m comment --comment %s -j CONNMARK --restore-mark
ip rule del fwmark %d table %d 2>/dev/null || true
ip rule add fwmark %d table %d priority %d
ip route replace default via %s dev %s table %d
`, ifName, tag, markID, markID, tag, markID, tag, markID, markID, markID, markID, 7700+markID, t.Subnet+".1", ifName, markID)
		}
		iptRules.WriteString(fmt.Sprintf("iptables -C FORWARD -i %s -m comment --comment %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -i %s -m comment --comment %s -j ACCEPT\n", ifName, tag, ifName, tag))
		iptRules.WriteString(fmt.Sprintf("iptables -C FORWARD -o %s -m comment --comment %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -o %s -m comment --comment %s -j ACCEPT\n", ifName, tag, ifName, tag))
		iptRules.WriteString(fmt.Sprintf("iptables -t mangle -C FORWARD -o %s -p tcp --tcp-flags SYN,RST SYN -m comment --comment %s -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null || iptables -t mangle -A FORWARD -o %s -p tcp --tcp-flags SYN,RST SYN -m comment --comment %s -j TCPMSS --clamp-mss-to-pmtu\n", ifName, tag, ifName, tag))
		iptRules.WriteString(fmt.Sprintf("iptables -t mangle -C FORWARD -i %s -p tcp --tcp-flags SYN,RST SYN -m comment --comment %s -j TCPMSS --clamp-mss-to-pmtu 2>/dev/null || iptables -t mangle -A FORWARD -i %s -p tcp --tcp-flags SYN,RST SYN -m comment --comment %s -j TCPMSS --clamp-mss-to-pmtu\n", ifName, tag, ifName, tag))
		if len(targets) > 1 && (t.LBMode == "lowest_ping" || t.LBMode == "lightest" || t.LBMode == "least_traffic") {
			var selector string
			tList := strings.Join(targets, " ")
			switch t.LBMode {
			case "lowest_ping":
				selector = fmt.Sprintf(`BEST_VIP="%s"; BEST_VAL=999999
for vip in %s; do
  ms=$(ping -c 2 -W 1 "$vip" 2>/dev/null | awk -F'/' 'END{print ($5==""?999999:int($5*10))}')
  if [ "${ms:-999999}" -lt "$BEST_VAL" ]; then BEST_VAL="$ms"; BEST_VIP="$vip"; fi
done
`, targets[0], tList)
			case "lightest":
				selector = fmt.Sprintf(`BEST_VIP="%s"; BEST_VAL=999999
for vip in %s; do
  cnt=$(ss -Htan dst "$vip" 2>/dev/null | wc -l)
  if [ "${cnt:-999999}" -lt "$BEST_VAL" ]; then BEST_VAL="$cnt"; BEST_VIP="$vip"; fi
done
`, targets[0], tList)
			case "least_traffic":
				selector = fmt.Sprintf(`BEST_VIP="%s"; BEST_VAL=999999999999
for vip in %s; do
  byt=$(iptables -t nat -L PREROUTING -vnx 2>/dev/null | awk -v ip="$vip" '$0 ~ ip {s+=$2} END{print s+0}')
  if [ "${byt:-0}" -lt "$BEST_VAL" ]; then BEST_VAL="$byt"; BEST_VIP="$vip"; fi
done
`, targets[0], tList)
			}
			iptRulesStr := selector + iptRules.String()
			iptRules.Reset()
			iptRules.WriteString(iptRulesStr)
		}

		makeKernel := func(preCmd, linkCmd, cidr, extraRules, postCmd string) string {
			return fmt.Sprintf(`set -e
mkdir -p %s
cat <<'EOF' > %s/%s.sh
#!/usr/bin/env bash
set -e
sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null 2>&1 || true
sysctl -w net.ipv4.conf.all.rp_filter=0 >/dev/null 2>&1 || true
sysctl -w net.ipv4.conf.default.rp_filter=0 >/dev/null 2>&1 || true
modprobe ip_gre 2>/dev/null || true
modprobe ip6_gre 2>/dev/null || true
modprobe ipip 2>/dev/null || true
modprobe vxlan 2>/dev/null || true
modprobe wireguard 2>/dev/null || true
ip link del %s 2>/dev/null || true
ip tunnel del %s 2>/dev/null || true
%s%s
ip addr add %s dev %s
ip link set %s mtu %d up
sysctl -w net.ipv4.conf.%s.rp_filter=0 >/dev/null 2>&1 || true
for tbl in nat raw mangle; do iptables-save -t "$tbl" | grep -v "%s" | iptables-restore -T "$tbl" 2>/dev/null || true; done
%s%s
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
if ! systemctl restart %s.service 2>/dev/null; then journalctl -u %s.service -n 8 --no-pager -o cat >&2; exit 1; fi
ip -br addr show %s
`, cfgDir, cfgDir, unit, ifName, ifName, preCmd, linkCmd, cidr, ifName, ifName, mtuVal, ifName, tag, extraRules, postCmd, cfgDir, unit, unit, t.ID, t.Name, cfgDir, unit, ifName, unit, unit, unit, ifName)
		}
		return makeKernel(iranPre, iranLink, iranLocal, iptRules.String(), iranPost), makeKernel(forPre, forLink, forLocal, "", forPost)
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
rm -f /etc/systemd/system/%s.service /etc/mcontrol/tunnels/%s.toml /etc/mcontrol/tunnels/%s.sh /etc/mcontrol/tunnels/%s.yaml /etc/mcontrol/tunnels/%s.key
ip link del %s 2>/dev/null || true
ip tunnel del %s 2>/dev/null || true
for tbl in nat raw mangle; do iptables-save -t "$tbl" | grep -v "%s" | iptables-restore -T "$tbl" 2>/dev/null || true; done
systemctl daemon-reload
echo "Cleaned."`, unit, unit, unit, unit, unit, unit, ifName, ifName, tag)

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

var pingSeqRe = regexp.MustCompile(`icmp_seq=(\d+).*?time=([0-9]+(?:\.[0-9]+)?)`)

func (h *Handler) handleTunnelPing8(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IranServerID    int64  `json:"iran_server_id"`
		ForeignServerID int64  `json:"foreign_server_id"`
		IranIPVer       string `json:"iran_ip_ver"`
		ForeignIPVer    string `json:"foreign_ip_ver"`
		Direction       string `json:"direction"` // "" | "iran_to_foreign" | "foreign_to_iran"
	}
	if !readJSON(w, r, &req) {
		return
	}
	iranSrv, err := h.store.GetServerWithSecret(req.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Entry server not found")
		return
	}
	forSrv, err := h.store.GetServerWithSecret(req.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Exit server not found")
		return
	}

	srcSrv, dstSrv := iranSrv, forSrv
	useV6 := req.ForeignIPVer == "v6"
	if req.Direction == "foreign_to_iran" {
		srcSrv, dstSrv = forSrv, iranSrv
		useV6 = req.IranIPVer == "v6"
	}
	target := dstSrv.Host
	if useV6 {
		if dstSrv.IPv6 != "" && dstSrv.IPv6 != "N/A" {
			target = dstSrv.IPv6
		} else {
			r6 := h.runOnServer(dstSrv, `sysctl -w net.ipv6.conf.all.disable_ipv6=0 >/dev/null 2>&1 || true; ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}'`, 8*time.Second)
			if s := strings.TrimSpace(r6.Output); s != "" && strings.Contains(s, ":") {
				target = s
			} else {
				fail(w, http.StatusBadRequest, dstSrv.Name+" does not have a global IPv6 address")
				return
			}
		}
	}

	pingFlag := "-4"
	prepV6 := ""
	if useV6 || strings.Contains(target, ":") {
		pingFlag = "-6"
		prepV6 = "sysctl -w net.ipv6.conf.all.disable_ipv6=0 >/dev/null 2>&1 || true; "
	}
	cmd := fmt.Sprintf(`%sping %s -c 8 -i 0.25 -W 2 %s 2>&1 || true`, prepV6, pingFlag, sshclient.ShQuote(target))
	res := h.runOnServer(srcSrv, cmd, 22*time.Second)

	samples := make([]float64, 8)
	for i := range samples {
		samples[i] = -1 // -1 means lost/timeout
	}
	var sum float64
	received := 0
	for _, line := range strings.Split(res.Output, "\n") {
		if m := pingSeqRe.FindStringSubmatch(line); len(m) == 3 {
			seq, _ := strconv.Atoi(m[1])
			ms, _ := strconv.ParseFloat(m[2], 64)
			if seq >= 1 && seq <= 8 {
				samples[seq-1] = math.Round(ms*10) / 10
				sum += ms
				received++
			}
		}
	}
	avgMs := 0.0
	if received > 0 {
		avgMs = math.Round((sum/float64(received))*10) / 10
	}
	lossPct := math.Round((float64(8-received)/8.0)*1000) / 10

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"target":   target,
		"ip_ver":   map[bool]string{true: "IPv6", false: "IPv4"}[useV6],
		"samples":  samples,
		"received": received,
		"sent":     8,
		"avg_ms":   avgMs,
		"loss_pct": lossPct,
	})
}

// StartTunnelWatchdog checks active MControl tunnels every 45 seconds and automatically
// restarts any systemd tunnel unit that dropped or became inactive on either side.
type backhaulExtraOpts struct {
	Preset          string `json:"preset,omitempty"`
	SNI             string `json:"sni,omitempty"`
	UTLSFingerprint string `json:"utls_fingerprint,omitempty"`
	EdgeIP          string `json:"edge_ip,omitempty"`
	TLSFragment     bool   `json:"tls_fragment,omitempty"`
	FallbackAddr    string `json:"fallback_addr,omitempty"`
	XHTTPPath       string `json:"xhttp_path,omitempty"`
	XHTTPHost       string `json:"xhttp_host,omitempty"`
	XHTTPMode       string `json:"xhttp_mode,omitempty"`
	KCPMode         string `json:"kcp_mode,omitempty"`
	KCPCrypt        string `json:"kcp_crypt,omitempty"`
	KCPNoFEC        bool   `json:"kcp_nofec,omitempty"`
	KernelEncap     string `json:"kernel_encap,omitempty"`
	KernelIPsec     string `json:"kernel_ipsec,omitempty"`
	ForwardMode     string `json:"forward_mode,omitempty"`
	MTU             int    `json:"mtu,omitempty"`
	RateLimitMbps      int    `json:"rate_limit_mbps,omitempty"`
	ParallaxLanes      int    `json:"parallax_lanes,omitempty"`
	ParallaxStallSec   int    `json:"parallax_stall_sec,omitempty"`
	FailoverTransports string `json:"failover_transports,omitempty"`
	FailoverRestoreSec int    `json:"failover_restore_sec,omitempty"`
	GameMode           bool   `json:"game_mode,omitempty"`
	GameCopies         int    `json:"game_copies,omitempty"`
	GamePorts          string `json:"game_ports,omitempty"`
}

func (h *Handler) StartTunnelWatchdog() {
	go func() {
		for {
			time.Sleep(45 * time.Second)
			if h.isStopped() {
				return
			}
			list, err := h.store.ListTunnels()
			if err != nil || len(list) == 0 {
				continue
			}
			sem := make(chan struct{}, 6)
			var wg sync.WaitGroup
			for _, tun := range list {
				if tun.Status != "active" {
					continue
				}
				t := tun
				wg.Add(1)
				sem <- struct{}{}
				go func() {
					defer wg.Done()
					defer func() { <-sem }()
					unit := fmt.Sprintf("mcontrol-tun-%d.service", t.ID)
					// Verify systemd unit is active AND listening control port (or kernel interface) is actually up
					iranHeal := fmt.Sprintf(`if [ -f /etc/systemd/system/%s ]; then
  if ! systemctl is-active --quiet %s; then systemctl restart %s; fi
fi`, unit, unit, unit)
					if t.Engine == "backhaul" || t.Engine == "rathole" {
						iranHeal = fmt.Sprintf(`if [ -f /etc/systemd/system/%s ]; then
  if ! systemctl is-active --quiet %s || ! ss -H -tuln "sport = :%d" 2>/dev/null | grep -q .; then
    systemctl restart %s
  fi
fi`, unit, unit, t.TunnelPort, unit)
					} else if t.Engine == "gre" || t.Engine == "ip6gre" || t.Engine == "vxlan" {
						ifName := fmt.Sprintf("mctun%d", t.ID)
						iranHeal = fmt.Sprintf(`if [ -f /etc/systemd/system/%s ]; then
  if ! systemctl is-active --quiet %s || ! ip link show %s >/dev/null 2>&1; then
    systemctl restart %s
  fi
fi`, unit, unit, ifName, unit)
					}
					forHeal := fmt.Sprintf(`if [ -f /etc/systemd/system/%s ]; then systemctl is-active --quiet %s || systemctl restart %s; fi`, unit, unit, unit)
					var nwg sync.WaitGroup
					if iranSrv, err := h.store.GetServerWithSecret(t.IranServerID); err == nil {
						nwg.Add(1)
						go func() { defer nwg.Done(); _ = h.runOnServer(iranSrv, iranHeal, 12*time.Second) }()
					}
					if forSrv, err := h.store.GetServerWithSecret(t.ForeignServerID); err == nil {
						nwg.Add(1)
						go func() { defer nwg.Done(); _ = h.runOnServer(forSrv, forHeal, 12*time.Second) }()
					}
					nwg.Wait()
				}()
			}
			wg.Wait()
		}
	}()
}


// handleTunnelPMTU measures true Path MTU between Iran and Foreign nodes via DF-bit binary search
// and applies optimal tunnel MTU live on both sides.
func (h *Handler) handleTunnelPMTU(w http.ResponseWriter, r *http.Request) {
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
	target := forSrv.Host
	probeCmd := fmt.Sprintf(`lo=1200; hi=1472; best=1280
while [ $lo -le $hi ]; do
  mid=$(( (lo + hi) / 2 ))
  if ping -c 1 -W 1 -M do -s "$mid" %s >/dev/null 2>&1; then
    best=$((mid + 28))
    lo=$((mid + 1))
  else
    hi=$((mid - 1))
  fi
done
echo "PMTU=$best"`, sshclient.ShQuote(target))
	res := h.runOnServer(iranSrv, probeCmd, 25*time.Second)
	pathMTU := 1500
	for _, ln := range strings.Split(res.Output, "\n") {
		if strings.HasPrefix(ln, "PMTU=") {
			if v, e := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(ln, "PMTU="))); e == nil && v >= 1280 && v <= 1500 {
				pathMTU = v
			}
		}
	}
	overhead := 48
	switch tun.Engine {
	case "ip6gre", "vxlan":
		overhead = 64
	case "wireguard":
		overhead = 80
	case "gre", "ipip":
		overhead = 36
	}
	var ex backhaulExtraOpts
	if strings.TrimSpace(tun.ExtraJSON) != "" {
		_ = json.Unmarshal([]byte(tun.ExtraJSON), &ex)
	}
	if ex.KernelIPsec != "" {
		overhead += 56
	}
	if ex.KernelEncap == "fou" {
		overhead += 8
	}
	optMTU := pathMTU - overhead
	if optMTU < 1240 {
		optMTU = 1240
	}
	applied := false
	if tun.Engine == "gre" || tun.Engine == "ip6gre" || tun.Engine == "ipip" || tun.Engine == "vxlan" || tun.Engine == "wireguard" {
		ifName := fmt.Sprintf("mctun%d", tun.ID)
		setCmd := fmt.Sprintf("ip link set %s mtu %d 2>/dev/null || true", ifName, optMTU)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = h.runOnServer(iranSrv, setCmd, 10*time.Second) }()
		go func() { defer wg.Done(); _ = h.runOnServer(forSrv, setCmd, 10*time.Second) }()
		wg.Wait()
		applied = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"path_mtu":    pathMTU,
		"optimal_mtu": optMTU,
		"overhead":    overhead,
		"applied":     applied,
		"message":     fmt.Sprintf("Measured Path MTU = %d bytes -> Optimal Tunnel MTU = %d (overhead %dB)", pathMTU, optMTU, overhead),
	})
}

// handleFindFreePort checks whether a port is free on both Iran & Foreign nodes and suggests a free port.
func (h *Handler) handleFindFreePort(w http.ResponseWriter, r *http.Request) {
	irID, _ := strconv.ParseInt(r.URL.Query().Get("iran_id"), 10, 64)
	frID, _ := strconv.ParseInt(r.URL.Query().Get("foreign_id"), 10, 64)
	pref, _ := strconv.Atoi(r.URL.Query().Get("preferred"))
	if pref < 1024 || pref > 65000 {
		pref = 3080
	}
	iranSrv, err1 := h.store.GetServerWithSecret(irID)
	forSrv, err2 := h.store.GetServerWithSecret(frID)
	if err1 != nil || err2 != nil {
		fail(w, http.StatusBadRequest, "select both Iran and Foreign servers first")
		return
	}
	listCmd := `ss -H -tuln 2>/dev/null | awk '{n=split($5,a,":"); p=a[n]; if(p~/^[0-9]+$/) print p}' | sort -nu`
	var wg sync.WaitGroup
	var r1, r2 sshclient.Result
	wg.Add(2)
	go func() { defer wg.Done(); r1 = h.runOnServer(iranSrv, listCmd, 10*time.Second) }()
	go func() { defer wg.Done(); r2 = h.runOnServer(forSrv, listCmd, 10*time.Second) }()
	wg.Wait()

	busy := map[int]bool{}
	for _, out := range []string{r1.Output, r2.Output} {
		for _, ln := range strings.Split(out, "\n") {
			if p, e := strconv.Atoi(strings.TrimSpace(ln)); e == nil {
				busy[p] = true
			}
		}
	}
	if !busy[pref] {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "free": true, "port": pref, "message": fmt.Sprintf("Port %d is free on both servers", pref)})
		return
	}
	suggested := pref
	for p := pref + 1; p < 60000; p++ {
		if !busy[p] {
			suggested = p
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"free":      false,
		"busy_port": pref,
		"port":      suggested,
		"message":   fmt.Sprintf("Port %d is BUSY — switched to free port %d", pref, suggested),
	})
}
