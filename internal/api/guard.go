package api

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/sshclient"
)

type GuardPort struct {
	Proto     string `json:"proto"`
	Port      int    `json:"port"`
	Process   string `json:"process"`
	Scope     string `json:"scope"` // all | iran | closed
	Protected bool   `json:"protected"`
	Reason    string `json:"reason,omitempty"`
}

func (h *Handler) handleGuardScan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	scanCmd := `
HAS_NFT=0; command -v nft >/dev/null 2>&1 && HAS_NFT=1
GUARD_ON=0; [ "$HAS_NFT" = "1" ] && nft list table inet mcontrol_guard >/dev/null 2>&1 && GUARD_ON=1
echo "META|${HAS_NFT}|${GUARD_ON}"
ss -H -tulnp 2>/dev/null | awk '{
  proto=$1; gsub(/6$/,"",proto);
  addr=$5;
  n=split(addr, a, ":"); port=a[n];
  proc="";
  for(i=6;i<=NF;i++) proc=proc" "$i;
  if(port ~ /^[0-9]+$/) print "PORT|"proto"|"port"|"proc
}' | sort -u
`
	res := h.runOnServer(srv, scanCmd, 15*time.Second)
	if res.Err != nil {
		fail(w, http.StatusBadGateway, "guard scan failed: "+res.Err.Error())
		return
	}
	hasNft, guardOn := false, false
	seen := map[string]*GuardPort{}
	for _, ln := range strings.Split(res.Output, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "META|") {
			p := strings.Split(ln, "|")
			if len(p) >= 3 {
				hasNft = p[1] == "1"
				guardOn = p[2] == "1"
			}
			continue
		}
		if !strings.HasPrefix(ln, "PORT|") {
			continue
		}
		p := strings.SplitN(ln, "|", 4)
		if len(p) < 4 {
			continue
		}
		proto := strings.ToLower(strings.TrimSpace(p[1]))
		if proto != "tcp" && proto != "udp" {
			continue
		}
		portNum, err := strconv.Atoi(strings.TrimSpace(p[2]))
		if err != nil || portNum < 1 || portNum > 65535 {
			continue
		}
		proc := extractProcName(p[3])
		key := fmt.Sprintf("%s/%d", proto, portNum)
		if _, dup := seen[key]; dup {
			continue
		}
		gp := &GuardPort{
			Proto:   proto,
			Port:    portNum,
			Process: proc,
			Scope:   "all",
		}
		if portNum == 22 || portNum == srv.Port || strings.Contains(proc, "sshd") {
			gp.Protected = true
			gp.Reason = "SSH"
		} else if portNum == srv.AgentPort || strings.Contains(proc, "mcontrol") {
			gp.Protected = true
			gp.Reason = "MControl"
		}
		seen[key] = gp
	}

	ports := make([]GuardPort, 0, len(seen))
	for _, gp := range seen {
		ports = append(ports, *gp)
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Protected != ports[j].Protected {
			return ports[i].Protected
		}
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		return ports[i].Proto < ports[j].Proto
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"has_nftables": hasNft,
		"active":       guardOn,
		"ports":        ports,
	})
}

func extractProcName(raw string) string {
	i := strings.Index(raw, `users:(("`)
	if i >= 0 {
		rest := raw[i+len(`users:(("`):]
		if j := strings.Index(rest, `"`); j > 0 {
			return rest[:j]
		}
	}
	return "service"
}

func (h *Handler) handleGuardApply(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	var req struct {
		Ports      []GuardPort `json:"ports"`
		PingPolicy string      `json:"ping_policy"` // all | iran | closed
		SafeMode   bool        `json:"safe_mode"`
		AllowIPs   string      `json:"allow_ips"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.PingPolicy != "iran" && req.PingPolicy != "closed" {
		req.PingPolicy = "all"
	}

	trustedV4 := map[string]bool{}
	trustedV6 := map[string]bool{}
	addIP := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "N/A" {
			return
		}
		if ip := net.ParseIP(raw); ip != nil {
			if ip.To4() != nil {
				trustedV4[ip.String()] = true
			} else {
				trustedV6[ip.String()] = true
			}
		} else if _, ipNet, err := net.ParseCIDR(raw); err == nil {
			if ipNet.IP.To4() != nil {
				trustedV4[ipNet.String()] = true
			} else {
				trustedV6[ipNet.String()] = true
			}
		}
	}
	addIP(h.masterPublicIP())
	if allSrvs, err := h.store.ListServers(); err == nil {
		for _, s := range allSrvs {
			addIP(s.Host)
			addIP(s.IPv4)
			addIP(s.IPv6)
		}
	}
	for _, piece := range strings.Split(req.AllowIPs, ",") {
		addIP(piece)
	}

	var rules strings.Builder
	rules.WriteString("table inet mcontrol_guard {\n")
	rules.WriteString("  set ir_v4 {\n    type ipv4_addr\n    flags interval\n  }\n")
	if len(trustedV4) > 0 {
		list := make([]string, 0, len(trustedV4))
		for k := range trustedV4 {
			list = append(list, k)
		}
		sort.Strings(list)
		rules.WriteString("  set trusted_v4 {\n    type ipv4_addr\n    flags interval\n    elements = { " + strings.Join(list, ", ") + " }\n  }\n")
	}
	if len(trustedV6) > 0 {
		list := make([]string, 0, len(trustedV6))
		for k := range trustedV6 {
			list = append(list, k)
		}
		sort.Strings(list)
		rules.WriteString("  set trusted_v6 {\n    type ipv6_addr\n    flags interval\n    elements = { " + strings.Join(list, ", ") + " }\n  }\n")
	}

	rules.WriteString("  chain input {\n    type filter hook input priority 0; policy drop;\n")
	rules.WriteString("    iifname \"lo\" accept\n")
	rules.WriteString("    iifname \"mctun*\" accept\n")
	rules.WriteString("    ct state established,related accept\n")
	if len(trustedV4) > 0 {
		rules.WriteString("    ip saddr @trusted_v4 accept\n")
	}
	if len(trustedV6) > 0 {
		rules.WriteString("    ip6 saddr @trusted_v6 accept\n")
	}
	sshPort := srv.Port
	if sshPort <= 0 {
		sshPort = 22
	}
	rules.WriteString(fmt.Sprintf("    tcp dport { 22, %d } accept\n", sshPort))
	if srv.AgentPort > 0 {
		rules.WriteString(fmt.Sprintf("    tcp dport %d accept\n", srv.AgentPort))
	}
	rules.WriteString("    icmp type { destination-unreachable, time-exceeded } accept\n")
	rules.WriteString("    icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert } accept\n")
	switch req.PingPolicy {
	case "all":
		rules.WriteString("    icmp type echo-request accept\n    icmpv6 type echo-request accept\n")
	case "iran":
		rules.WriteString("    ip saddr @ir_v4 icmp type echo-request accept\n")
	}

	needsIranGeo := req.PingPolicy == "iran"
	for _, p := range req.Ports {
		if p.Port < 1 || p.Port > 65535 || (p.Proto != "tcp" && p.Proto != "udp") {
			continue
		}
		if p.Protected || p.Scope == "all" {
			rules.WriteString(fmt.Sprintf("    %s dport %d accept\n", p.Proto, p.Port))
		} else if p.Scope == "iran" {
			needsIranGeo = true
			rules.WriteString(fmt.Sprintf("    ip saddr @ir_v4 %s dport %d accept\n", p.Proto, p.Port))
		}
	}
	rules.WriteString("  }\n}\n")

	geoFetch := ""
	if needsIranGeo {
		geoFetch = `
mkdir -p /etc/mcontrol
if [ ! -s /etc/mcontrol/ir-v4.zone ] || [ $(find /etc/mcontrol/ir-v4.zone -mtime +7 2>/dev/null | wc -l) -gt 0 ]; then
  curl -fsSL --max-time 12 "https://www.ipdeny.com/ipblocks/data/aggregated/ir-aggregated.zone" -o /etc/mcontrol/ir-v4.zone 2>/dev/null \
    || curl -fsSL --max-time 12 "https://raw.githubusercontent.com/herrbischoff/country-ip-blocks/master/ipv4/ir.cidr" -o /etc/mcontrol/ir-v4.zone 2>/dev/null || true
fi
if [ -s /etc/mcontrol/ir-v4.zone ]; then
  CIDRS=$(grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/[0-9]+$' /etc/mcontrol/ir-v4.zone | paste -sd, -)
  [ -n "$CIDRS" ] && nft add element inet mcontrol_guard ir_v4 "{ $CIDRS }" 2>/dev/null || true
fi
`
	}

	safeTok := ""
	safeCmd := "rm -f /run/mc-guard-safe.tok\n"
	if req.SafeMode {
		safeTok = randTunnelToken()[:12]
		safeCmd = fmt.Sprintf(`echo %s > /run/mc-guard-safe.tok
nohup bash -c 'sleep 90; if [ -f /run/mc-guard-safe.tok ] && [ "$(cat /run/mc-guard-safe.tok 2>/dev/null)" = %s ]; then nft delete table inet mcontrol_guard 2>/dev/null || true; rm -f /run/mc-guard-safe.tok; fi' >/dev/null 2>&1 &
`, sshclient.ShQuote(safeTok), sshclient.ShQuote(safeTok))
	}

	applyScript := fmt.Sprintf(`set -e
if ! command -v nft >/dev/null 2>&1; then
  (apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nftables) >/dev/null 2>&1 \
    || dnf install -y -q nftables >/dev/null 2>&1 \
    || { echo "nftables is not installed and could not be auto-installed" >&2; exit 1; }
fi
mkdir -p /etc/mcontrol
cat <<'MC_GUARD_EOF' > /etc/mcontrol/guard.nft
%sMC_GUARD_EOF
nft delete table inet mcontrol_guard 2>/dev/null || true
nft -f /etc/mcontrol/guard.nft
%s%secho "Guard Firewall applied cleanly"
`, rules.String(), geoFetch, safeCmd)

	res := h.runOnServer(srv, applyScript, 35*time.Second)
	if res.Err != nil {
		fail(w, http.StatusBadGateway, "failed to apply Guard Firewall: "+res.Output+" "+res.Err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"safe_mode":  req.SafeMode,
		"safe_token": safeTok,
		"message":    strings.TrimSpace(res.Output),
	})
}

func (h *Handler) handleGuardConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	cmd := `rm -f /run/mc-guard-safe.tok && echo "Guard Firewall Safe Mode confirmed & locked"`
	res := h.runOnServer(srv, cmd, 12*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
}

func (h *Handler) handleGuardDisable(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	cmd := `rm -f /run/mc-guard-safe.tok; nft delete table inet mcontrol_guard 2>/dev/null || true; rm -f /etc/mcontrol/guard.nft; echo "Guard Firewall disabled"`
	res := h.runOnServer(srv, cmd, 15*time.Second)
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
}
