package api

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/geo"
	"github.com/htzserv/MControl/internal/sshclient"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Existing MControl Tunnel & Peer Server Auto-Detection + Adoption
//  Detects tunnels created by another MControl panel instance on any server
//  (/etc/systemd/system/mcontrol-tun-*.service & /etc/mcontrol/tunnels/*)
//  and offers one-click adoption of both the tunnels and the missing peer node.
// ─────────────────────────────────────────────────────────────────────────────

const mcTunnelScanScript = `#!/bin/bash
for f in /etc/systemd/system/mcontrol-tun-*.service; do
  [ -f "$f" ] || continue
  u=$(basename "$f" .service)
  st=$(systemctl is-active "$u.service" 2>/dev/null || echo inactive)
  echo "@@UNIT $u $st"
  base64 < "$f" | tr -d '\n'; echo
  echo "@@END"
  for ext in toml yaml sh; do
    cf="/etc/mcontrol/tunnels/$u.$ext"
    if [ -f "$cf" ]; then
      echo "@@CFG $u $ext"
      base64 < "$cf" | tr -d '\n'; echo
      echo "@@END"
    fi
  done
done
if command -v ss >/dev/null 2>&1; then
  echo "@@SS"
  ss -tnH state established 2>/dev/null | awk '{print $4" "$5}' | head -n 400
  echo "@@END"
fi
echo "@@DONE"
`

type DetectedMCTunnel struct {
	Unit           string `json:"unit"`
	OrigID         int64  `json:"orig_id"`
	Name           string `json:"name"`
	Engine         string `json:"engine"`
	Transport      string `json:"transport"`
	Side           string `json:"side"` // "iran" or "foreign"
	TunnelPort     int    `json:"tunnel_port"`
	Token          string `json:"token"`
	Ports          string `json:"ports"`
	Subnet         string `json:"subnet"`
	PeerHost       string `json:"peer_host"`
	Active         bool   `json:"active"`
	AlreadyInPanel bool   `json:"already_in_panel"`
	PeerServerID   int64  `json:"peer_server_id"`
	PeerServerName string `json:"peer_server_name"`
}

type MissingMCPeer struct {
	Host          string `json:"host"`
	SuggestedRole string `json:"suggested_role"`
	TunnelCount   int    `json:"tunnel_count"`
}

type rawMCUnit struct {
	Unit    string
	State   string
	Service string
	CfgExt  string
	CfgBody string
}

var (
	mcUnitNumRe   = regexp.MustCompile(`mcontrol-tun-(\d+)`)
	mcDescRe      = regexp.MustCompile(`(?i)Description=MControl\s+([A-Za-z0-9_+-]+)\s+Tunnel\s+\d+\s*\(([^)]+)\)`)
	mcKVQuoteRe   = regexp.MustCompile(`(?m)^\s*([a-zA-Z0-9_]+)\s*=\s*"([^"]*)"`)
	mcPortPairRe  = regexp.MustCompile(`"(\d{1,5})(?:=\d{1,5})?"`)
	mcRHServiceRe = regexp.MustCompile(`\[(?:server|client)\.services\.p(\d{1,5})\]`)
	mcPaqetAddrRe = regexp.MustCompile(`(?m)^\s*addr:\s*"?([^"\s#]+)"?`)
	mcPaqetKeyRe  = regexp.MustCompile(`(?m)^\s*key:\s*"?([^"\s#]+)"?`)
	mcPaqetLstRe  = regexp.MustCompile(`(?m)^\s*listen:\s*"?[^"\s:]*:(\d{1,5})"?`)
	mcShRemoteRe  = regexp.MustCompile(`\bremote\s+([0-9a-fA-F.:]+)`)
	mcShEndptRe   = regexp.MustCompile(`\bendpoint\s+(\[[^\]]+\]:\d+|[^:\s]+:\d+)`)
	mcShSubnetRe  = regexp.MustCompile(`(10\.\d{1,3}\.\d{1,3})\.(1|2)/30`)
	mcShDportRe   = regexp.MustCompile(`--dport\s+(\d{1,5})`)
	mcShDstPortRe = regexp.MustCompile(`\bdstport\s+(\d{1,5})`)
	mcShLstPortRe = regexp.MustCompile(`\blisten-port\s+(\d{1,5})`)
)

func splitHostPortLoose(raw string) (string, int) {
	s := strings.TrimSpace(strings.Trim(raw, `"'`))
	if s == "" {
		return "", 0
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		return strings.Trim(h, "[]"), atoiSafe(p)
	}
	// Fallback if no brackets or single colon
	if idx := strings.LastIndex(s, ":"); idx > 0 {
		h := strings.Trim(s[:idx], "[]")
		p := atoiSafe(s[idx+1:])
		if p > 0 {
			return h, p
		}
	}
	return strings.Trim(s, "[]"), 0
}

func isUsableRemoteIP(ipStr string) bool {
	ipStr = strings.Trim(strings.TrimSpace(ipStr), "[]")
	if i := strings.Index(ipStr, "%"); i > 0 {
		ipStr = ipStr[:i]
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
		return false
	}
	return true
}

func parseMControlScan(out string, srv *db.Server) []DetectedMCTunnel {
	units := map[string]*rawMCUnit{}
	var order []string
	var ssLines []string

	lines := strings.Split(out, "\n")
	mode := ""
	curUnit := ""
	curExt := ""
	var buf strings.Builder

	flush := func() {
		if mode == "unit" && curUnit != "" {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(buf.String())); err == nil {
				units[curUnit].Service = string(raw)
			}
		} else if mode == "cfg" && curUnit != "" {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(buf.String())); err == nil {
				units[curUnit].CfgExt = curExt
				units[curUnit].CfgBody = string(raw)
			}
		}
		buf.Reset()
		mode = ""
	}

	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case strings.HasPrefix(ln, "@@UNIT "):
			flush()
			f := strings.Fields(ln)
			if len(f) >= 2 {
				u := f[1]
				st := ""
				if len(f) >= 3 {
					st = f[2]
				}
				if _, ok := units[u]; !ok {
					units[u] = &rawMCUnit{Unit: u, State: st}
					order = append(order, u)
				}
				curUnit = u
				mode = "unit"
			}
		case strings.HasPrefix(ln, "@@CFG "):
			flush()
			f := strings.Fields(ln)
			if len(f) >= 3 {
				u, ext := f[1], f[2]
				if _, ok := units[u]; !ok {
					units[u] = &rawMCUnit{Unit: u}
					order = append(order, u)
				}
				curUnit = u
				curExt = ext
				mode = "cfg"
			}
		case ln == "@@SS":
			flush()
			mode = "ss"
		case ln == "@@END":
			flush()
		case strings.HasPrefix(ln, "@@DONE"):
			flush()
		default:
			if mode == "unit" || mode == "cfg" {
				buf.WriteString(strings.TrimSpace(ln))
			} else if mode == "ss" && strings.TrimSpace(ln) != "" {
				ssLines = append(ssLines, strings.TrimSpace(ln))
			}
		}
	}
	flush()

	// Build localPort -> remote peer IP map from live ss output
	livePeerByPort := map[int]string{}
	for _, sl := range ssLines {
		f := strings.Fields(sl)
		if len(f) < 2 {
			continue
		}
		_, lport := splitHostPortLoose(f[0])
		rhost, _ := splitHostPortLoose(f[1])
		if lport > 0 && isUsableRemoteIP(rhost) && !hostMatches(rhost, srv) {
			if _, exists := livePeerByPort[lport]; !exists {
				livePeerByPort[lport] = rhost
			}
		}
	}

	sort.Strings(order)
	var result []DetectedMCTunnel
	for _, uName := range order {
		ru := units[uName]
		var origID int64
		if m := mcUnitNumRe.FindStringSubmatch(uName); len(m) == 2 {
			if v, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				origID = v
			}
		}

		dt := DetectedMCTunnel{
			Unit:   uName,
			OrigID: origID,
			Name:   uName,
			Side:   srv.Role,
			Active: strings.TrimSpace(ru.State) == "active",
		}
		if dt.Side == "" || dt.Side == "auto" {
			if strings.EqualFold(srv.CountryCode, "IR") {
				dt.Side = "iran"
			} else {
				dt.Side = "foreign"
			}
		}

		// Parse Description & ExecStart from systemd service
		if m := mcDescRe.FindStringSubmatch(ru.Service); len(m) == 3 {
			dt.Engine = strings.ToLower(strings.TrimSpace(m[1]))
			dt.Name = strings.TrimSpace(m[2])
		}
		lowSvc := strings.ToLower(ru.Service)
		if dt.Engine == "" {
			switch {
			case strings.Contains(lowSvc, "rathole"):
				dt.Engine = "rathole"
			case strings.Contains(lowSvc, "paqet"):
				dt.Engine = "paqet"
			case strings.Contains(lowSvc, "backhaul") || strings.Contains(lowSvc, "/bh ") || strings.Contains(lowSvc, "bh -c"):
				dt.Engine = "backhaul"
			case ru.CfgExt == "yaml":
				dt.Engine = "paqet"
			case ru.CfgExt == "sh":
				dt.Engine = "gre"
			default:
				dt.Engine = "backhaul"
			}
		}

		cfg := ru.CfgBody
		switch ru.CfgExt {
		case "toml":
			if strings.Contains(cfg, "[server]") {
				dt.Side = "iran"
			} else if strings.Contains(cfg, "[client]") {
				dt.Side = "foreign"
			}
			kvs := map[string]string{}
			for _, m := range mcKVQuoteRe.FindAllStringSubmatch(cfg, -1) {
				if len(m) == 3 {
					kvs[strings.ToLower(m[1])] = m[2]
				}
			}
			if tr := kvs["transport"]; tr != "" {
				dt.Transport = tr
			} else if dt.Engine == "rathole" {
				dt.Transport = "tcp"
			}
			if tok := kvs["token"]; tok != "" {
				dt.Token = tok
			} else if tok := kvs["default_token"]; tok != "" {
				dt.Token = tok
			}
			if ra := kvs["remote_addr"]; ra != "" {
				ph, pp := splitHostPortLoose(ra)
				if ph != "" && !hostMatches(ph, srv) {
					dt.PeerHost = ph
				}
				if pp > 0 {
					dt.TunnelPort = pp
				}
			}
			if ba := kvs["bind_addr"]; ba != "" && dt.TunnelPort == 0 {
				_, bp := splitHostPortLoose(ba)
				if bp > 0 {
					dt.TunnelPort = bp
				}
			}
			// Extract forwarded ports
			portSet := map[int]bool{}
			if idx := strings.Index(cfg, "ports = ["); idx >= 0 {
				end := strings.Index(cfg[idx:], "]")
				if end > 0 {
					for _, pm := range mcPortPairRe.FindAllStringSubmatch(cfg[idx:idx+end], -1) {
						if p := atoiSafe(pm[1]); p > 0 {
							portSet[p] = true
						}
					}
				}
			}
			for _, pm := range mcRHServiceRe.FindAllStringSubmatch(cfg, -1) {
				if p := atoiSafe(pm[1]); p > 0 {
					portSet[p] = true
				}
			}
			if len(portSet) > 0 {
				var ps []int
				for p := range portSet {
					ps = append(ps, p)
				}
				sort.Ints(ps)
				var ss []string
				for _, p := range ps {
					ss = append(ss, strconv.Itoa(p))
				}
				dt.Ports = strings.Join(ss, ",")
			}

		case "yaml":
			dt.Engine = "paqet"
			dt.Transport = "raw"
			if strings.Contains(cfg, `role: "server"`) || strings.Contains(cfg, "role: server") {
				dt.Side = "foreign"
			} else if strings.Contains(cfg, `role: "client"`) || strings.Contains(cfg, "role: client") {
				dt.Side = "iran"
			}
			if m := mcPaqetKeyRe.FindStringSubmatch(cfg); len(m) == 2 {
				dt.Token = m[1]
			}
			for _, m := range mcPaqetAddrRe.FindAllStringSubmatch(cfg, -1) {
				ph, pp := splitHostPortLoose(m[1])
				if pp > 0 && dt.TunnelPort == 0 {
					dt.TunnelPort = pp
				}
				if isUsableRemoteIP(ph) && !hostMatches(ph, srv) && dt.PeerHost == "" {
					dt.PeerHost = ph
				}
			}
			portSet := map[int]bool{}
			for _, m := range mcPaqetLstRe.FindAllStringSubmatch(cfg, -1) {
				if p := atoiSafe(m[1]); p > 0 {
					portSet[p] = true
				}
			}
			if len(portSet) > 0 {
				var ps []int
				for p := range portSet {
					ps = append(ps, p)
				}
				sort.Ints(ps)
				var ss []string
				for _, p := range ps {
					ss = append(ss, strconv.Itoa(p))
				}
				dt.Ports = strings.Join(ss, ",")
			}

		case "sh":
			lowCfg := strings.ToLower(cfg)
			switch {
			case strings.Contains(lowCfg, "type wireguard") || strings.Contains(lowCfg, "wg set"):
				dt.Engine = "wireguard"
				dt.Transport = "udp"
			case strings.Contains(lowCfg, "type vxlan"):
				dt.Engine = "vxlan"
				dt.Transport = "udp"
			case strings.Contains(lowCfg, "mode ipip"):
				dt.Engine = "ipip"
				dt.Transport = "ipip"
			default:
				dt.Engine = "gre"
				dt.Transport = "gre"
			}
			if m := mcShRemoteRe.FindStringSubmatch(cfg); len(m) == 2 && isUsableRemoteIP(m[1]) && !hostMatches(m[1], srv) {
				dt.PeerHost = m[1]
			}
			if m := mcShEndptRe.FindStringSubmatch(cfg); len(m) == 2 {
				ph, pp := splitHostPortLoose(m[1])
				if isUsableRemoteIP(ph) && !hostMatches(ph, srv) {
					dt.PeerHost = ph
				}
				if pp > 0 {
					dt.TunnelPort = pp
				}
			}
			if m := mcShDstPortRe.FindStringSubmatch(cfg); len(m) == 2 && dt.TunnelPort == 0 {
				dt.TunnelPort = atoiSafe(m[1])
			}
			if m := mcShLstPortRe.FindStringSubmatch(cfg); len(m) == 2 && dt.TunnelPort == 0 {
				dt.TunnelPort = atoiSafe(m[1])
			}
			if m := mcShSubnetRe.FindStringSubmatch(cfg); len(m) == 3 {
				dt.Subnet = m[1]
				if m[2] == "1" {
					dt.Side = "iran"
				} else {
					dt.Side = "foreign"
				}
			}
			portSet := map[int]bool{}
			for _, m := range mcShDportRe.FindAllStringSubmatch(cfg, -1) {
				if p := atoiSafe(m[1]); p > 0 {
					portSet[p] = true
				}
			}
			if len(portSet) > 0 {
				var ps []int
				for p := range portSet {
					ps = append(ps, p)
				}
				sort.Ints(ps)
				var ss []string
				for _, p := range ps {
					ss = append(ss, strconv.Itoa(p))
				}
				dt.Ports = strings.Join(ss, ",")
			}
		}

		// Live socket fallback for server-side tunnels where remote IP isn't stored in config
		if dt.PeerHost == "" && dt.TunnelPort > 0 {
			if peerIP, ok := livePeerByPort[dt.TunnelPort]; ok {
				dt.PeerHost = peerIP
			}
		}
		if dt.Transport == "" {
			dt.Transport = "tcp"
		}
		result = append(result, dt)
	}
	return result
}

func (h *Handler) scanMControlOnServer(srv *db.Server) ([]DetectedMCTunnel, []MissingMCPeer, error) {
	res := h.runOnServer(srv, mcTunnelScanScript, 25*time.Second)
	if res.Err != nil && res.Output == "" {
		return nil, nil, res.Err
	}
	tuns := parseMControlScan(res.Output, srv)
	existingSrvs, _ := h.store.ListServers()
	existingTuns, _ := h.store.ListTunnels()

	missingMap := map[string]*MissingMCPeer{}
	for i := range tuns {
		t := &tuns[i]
		// Check if already tracked in this panel's database
		for _, et := range existingTuns {
			if et.IranServerID != srv.ID && et.ForeignServerID != srv.ID {
				continue
			}
			if (t.OrigID > 0 && et.ID == t.OrigID) ||
				(strings.EqualFold(et.Engine, t.Engine) && t.TunnelPort > 0 && et.TunnelPort == t.TunnelPort) {
				t.AlreadyInPanel = true
				break
			}
		}
		// Check if peer host already exists in this panel
		if t.PeerHost != "" {
			for j := range existingSrvs {
				if existingSrvs[j].ID != srv.ID && hostMatches(t.PeerHost, &existingSrvs[j]) {
					t.PeerServerID = existingSrvs[j].ID
					t.PeerServerName = existingSrvs[j].Name
					break
				}
			}
			if t.PeerServerID == 0 && !t.AlreadyInPanel {
				mp, ok := missingMap[t.PeerHost]
				if !ok {
					sRole := "foreign"
					if t.Side == "foreign" {
						sRole = "iran"
					}
					mp = &MissingMCPeer{Host: t.PeerHost, SuggestedRole: sRole}
					missingMap[t.PeerHost] = mp
				}
				mp.TunnelCount++
			}
		}
	}

	var missing []MissingMCPeer
	for _, mp := range missingMap {
		missing = append(missing, *mp)
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Host < missing[j].Host })
	return tuns, missing, nil
}

func (h *Handler) handleScanMControl(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	tuns, missing, err := h.scanMControlOnServer(srv)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "found": false, "error": err.Error(), "tunnels": []any{}, "missing_peers": []any{}})
		return
	}
	newCount := 0
	for _, t := range tuns {
		if !t.AlreadyInPanel {
			newCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"found":         newCount > 0,
		"new_count":     newCount,
		"tunnels":       tuns,
		"missing_peers": missing,
	})
}

type adoptPeerInput struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"auth_type"`
	Secret   string `json:"secret"`
}

type adoptMControlReq struct {
	Units    []string         `json:"units"`
	AddPeers []adoptPeerInput `json:"add_peers"`
	PeerMap  map[string]int64 `json:"peer_map"`
}

func renameMCUnitScript(oldID, newID int64) string {
	oldU := fmt.Sprintf("mcontrol-tun-%d", oldID)
	newU := fmt.Sprintf("mcontrol-tun-%d", newID)
	return fmt.Sprintf(`if [ -f /etc/systemd/system/%s.service ] && [ ! -f /etc/systemd/system/%s.service ]; then
  was_active=0
  systemctl is-active --quiet %s.service && was_active=1
  systemctl disable --now %s.service 2>/dev/null || true
  for ext in toml yaml sh key; do
    [ -f "/etc/mcontrol/tunnels/%s.$ext" ] && mv -f "/etc/mcontrol/tunnels/%s.$ext" "/etc/mcontrol/tunnels/%s.$ext"
  done
  sed "s/%s/%s/g" "/etc/systemd/system/%s.service" > "/etc/systemd/system/%s.service"
  rm -f "/etc/systemd/system/%s.service"
  systemctl daemon-reload
  systemctl enable %s.service 2>/dev/null || true
  [ "$was_active" = "1" ] && systemctl restart %s.service 2>/dev/null || true
fi`, oldU, newU, oldU, oldU, oldU, oldU, newU, oldU, newU, oldU, newU, oldU, newU, newU)
}

func (h *Handler) handleAdoptMControl(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	var req adoptMControlReq
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Units) == 0 {
		fail(w, http.StatusBadRequest, "no tunnels selected to adopt")
		return
	}

	var addedServers []map[string]any
	var warnings []string

	// Step 1: Add any requested missing peer servers first
	existingSrvs, _ := h.store.ListServers()
	for _, ap := range req.AddPeers {
		host := strings.TrimSpace(ap.Host)
		if host == "" || strings.TrimSpace(ap.Secret) == "" {
			continue
		}
		if !validHostRe.MatchString(host) {
			warnings = append(warnings, "invalid peer host skipped: "+host)
			continue
		}
		already := false
		for j := range existingSrvs {
			if hostMatches(host, &existingSrvs[j]) {
				already = true
				break
			}
		}
		if already {
			continue
		}
		port := ap.Port
		if port <= 0 {
			port = 22
		}
		user := strings.TrimSpace(ap.Username)
		if user == "" {
			user = "root"
		}
		authType := strings.TrimSpace(ap.AuthType)
		if authType != "key" {
			authType = "password"
		}
		name := strings.TrimSpace(ap.Name)
		if name == "" {
			name = host
		}

		var (
			g      geo.Info
			pingMs string
			authR  sshclient.Result
			wg     sync.WaitGroup
		)
		wg.Add(3)
		go func() { defer wg.Done(); g = geo.LookupWithProxy(host, h.geoSocksProxy()) }()
		go func() { defer wg.Done(); pingMs = sshclient.MeasurePingFromMaster(host, port) }()
		go func() {
			defer wg.Done()
			authR = sshclient.Run(sshclient.Target{Host: host, Port: port, User: user, Secret: ap.Secret, AuthType: authType}, "true", 8*time.Second, 8*time.Second)
		}()
		wg.Wait()

		if authR.Err != nil {
			warnings = append(warnings, fmt.Sprintf("peer %s SSH failed: %v", host, authR.Err))
			continue
		}
		role := strings.TrimSpace(ap.Role)
		if role == "" || role == "auto" {
			if strings.EqualFold(g.CountryCode, "IR") {
				role = "iran"
			} else {
				role = "foreign"
			}
		}
		newSrv := &db.Server{
			Name:        name,
			Role:        role,
			Host:        host,
			Port:        port,
			Username:    user,
			Password:    ap.Secret,
			AuthType:    authType,
			HostKey:     authR.HostKey,
			Country:     g.Country,
			CountryCode: g.CountryCode,
			Datacenter:  g.Datacenter,
			Status:      "online",
			CPU:         "---",
			RAM:         "---",
			Uptime:      "---",
			IPv4:        host,
			PingMs:      pingMs,
		}
		newSrvID, addErr := h.store.AddServer(newSrv)
		if addErr != nil {
			warnings = append(warnings, fmt.Sprintf("peer %s save failed: %v", host, addErr))
			continue
		}
		newSrv.ID = newSrvID
		existingSrvs = append(existingSrvs, *newSrv)
		addedServers = append(addedServers, map[string]any{"id": newSrvID, "name": name, "host": host, "role": role})
		go func(sid int64) { _, _ = h.probeServer(sid) }(newSrvID)
	}

	// Step 2: Re-scan server to get fresh parsed tunnels & updated peer links
	detected, _, scanErr := h.scanMControlOnServer(srv)
	if scanErr != nil {
		fail(w, http.StatusBadGateway, "re-scan failed: "+scanErr.Error())
		return
	}
	want := map[string]bool{}
	for _, u := range req.Units {
		want[strings.TrimSpace(u)] = true
	}

	var adopted []map[string]any
	for _, dt := range detected {
		if !want[dt.Unit] || dt.AlreadyInPanel {
			continue
		}
		peerID := dt.PeerServerID
		if req.PeerMap != nil && req.PeerMap[dt.Unit] > 0 {
			peerID = req.PeerMap[dt.Unit]
		}
		if peerID == 0 {
			warnings = append(warnings, fmt.Sprintf("%s skipped: peer server (%s) is not in panel", dt.Unit, dt.PeerHost))
			continue
		}
		var iranID, forID int64
		if dt.Side == "foreign" {
			forID = srv.ID
			iranID = peerID
		} else {
			iranID = srv.ID
			forID = peerID
		}
		status := "stopped"
		if dt.Active {
			status = "running"
		}
		tRec := &db.Tunnel{
			Name:            dt.Name,
			Engine:          dt.Engine,
			Transport:       dt.Transport,
			IranServerID:    iranID,
			ForeignServerID: forID,
			TunnelPort:      dt.TunnelPort,
			Token:           dt.Token,
			Ports:           dt.Ports,
			Forwarder:       "internal",
			Subnet:          dt.Subnet,
			Status:          status,
		}
		newID, addErr := h.store.AddTunnel(tRec)
		if addErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s DB error: %v", dt.Unit, addErr))
			continue
		}
		// Keep systemd unit ID synchronized with the new DB tunnel ID so Start/Stop/Logs/Delete work seamlessly
		if dt.OrigID > 0 && newID != dt.OrigID {
			renameCmd := renameMCUnitScript(dt.OrigID, newID)
			iranSrv, _ := h.store.GetServerWithSecret(iranID)
			forSrv, _ := h.store.GetServerWithSecret(forID)
			var rwg sync.WaitGroup
			if iranSrv != nil {
				rwg.Add(1)
				go func() { defer rwg.Done(); _ = h.runOnServer(iranSrv, renameCmd, 20*time.Second) }()
			}
			if forSrv != nil {
				rwg.Add(1)
				go func() { defer rwg.Done(); _ = h.runOnServer(forSrv, renameCmd, 20*time.Second) }()
			}
			rwg.Wait()
		}
		adopted = append(adopted, map[string]any{
			"id":     newID,
			"name":   dt.Name,
			"engine": dt.Engine,
			"unit":   fmt.Sprintf("mcontrol-tun-%d", newID),
			"status": status,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"adopted":       adopted,
		"added_servers": addedServers,
		"warnings":      warnings,
	})
}
