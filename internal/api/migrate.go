package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
)

// ─────────────────────────────────────────────────────────────────────────────
//  MTunnel → MControl migration
//
//  1. scan   : read-only, copies every MTunnel config file verbatim
//  2. save   : exact encrypted snapshot + staged tunnel rows in the database
//  3. install: static bundle (bh / rathole / paqet / ...) and verify the binaries
//  4. wipe   : remove MTunnel completely (never touches the engines installed in 3)
//  5. pair   : when both sides of a tunnel are known, re-create it under MControl
//              using the ORIGINAL config files (same ports, tokens, transport)
// ─────────────────────────────────────────────────────────────────────────────

const legacyScanScript = `#!/bin/bash
found=0
for p in /usr/bin/mtunnel /usr/local/bin/mtunnel /usr/lib/mtunnel /etc/mgre /etc/mporter /etc/mrathole /etc/mbackhaul /etc/paqet /etc/mtunnel/smart /root/mtunnel; do
  [ -e "$p" ] && { found=1; echo "@@HAS $p"; }
done
if command -v systemctl >/dev/null 2>&1; then
  systemctl list-units --all --no-legend --plain 'mbackhaul@*' 'mrathole@*' 'mpaqet@*' mgre.service mxlan.service mporter.service mporter-watchdog.service 'mtunnel-smart-*' 2>/dev/null | awk '{print "@@UNIT "$1" "$3" "$4}'
fi
ip4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')
ip6=$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1);exit}}')
echo "@@IP4 $ip4"
echo "@@IP6 $ip6"
for d in /etc/mbackhaul /etc/mrathole /etc/mgre /etc/mxlan /etc/paqet /etc/mtunnel/smart /etc/mporter; do
  [ -d "$d" ] || continue
  find "$d" -type f -size -256k 2>/dev/null | sort | while IFS= read -r f; do
    case "$f" in *.lock|*/.core_prompted) continue;; esac
    echo "@@FILE $f"
    base64 < "$f" | tr -d '\n'; echo
    echo "@@END"
  done
done
echo "@@DONE found=$found"
`

// legacyWipeScript removes MTunnel completely. It deliberately does NOT delete
// /usr/local/bin/{bh,rathole,paqet,realm,gost,jq,iperf3}: those were just
// (re)installed by MControl and the old MTunnel "owned binary" cleanup could
// otherwise remove them again.
const legacyWipeScript = `#!/bin/bash
systemctl stop mtunnel-smart-watchdog.timer mtunnel-smart-agent.service mgre-watchdog.timer mxlan-watchdog.timer mporter-watchdog.service 2>/dev/null
[ -x /usr/bin/mgre ] && /usr/bin/mgre --teardown-all 2>/dev/null
[ -x /usr/bin/mxlan ] && /usr/bin/mxlan --teardown-all 2>/dev/null
systemctl stop 'mbackhaul@*' 'mrathole@*' 'mpaqet@*' mgre.service mxlan.service mporter.service mporter-watchdog.service mbackhaul-apply.service mrathole-apply.service mpaqet-apply.service mporter-iptables.service mporter-obfs.service 2>/dev/null
systemctl disable 'mbackhaul@*' 'mrathole@*' 'mpaqet@*' mgre.service mxlan.service mporter.service mporter-watchdog.service mtunnel-smart-watchdog.timer mtunnel-smart-agent.service mgre-watchdog.timer mxlan-watchdog.timer mbackhaul-apply.service mrathole-apply.service mpaqet-apply.service mporter-iptables.service mporter-obfs.service 2>/dev/null
for bin in iptables ip6tables; do
  command -v "$bin" >/dev/null 2>&1 || continue
  while iptables -D INPUT -p tcp --dport 29876 -m comment --comment MTUNNEL_SMART_AGENT -j ACCEPT 2>/dev/null; do :; done
  for table in filter nat mangle raw; do
    "$bin" -t "$table" -S 2>/dev/null | grep -E '^-A .*--comment "?(MGRE|MXLAN|MPORTER|MBH|RAT|MPAQET|OBFS_CNT)_' | sed 's/^-A /-D /' | while IFS= read -r rule; do
      eval "$bin -t $table $rule" 2>/dev/null
    done
  done
  for chain in MGRE_GUARD MGRE6_GUARD MXLAN_GUARD MXLAN6_GUARD; do
    "$bin" -F "$chain" 2>/dev/null; "$bin" -X "$chain" 2>/dev/null
  done
done
if command -v crontab >/dev/null 2>&1; then
  tmpc=$(mktemp) && {
    crontab -l 2>/dev/null | awk '!/mbackhaul@|mrathole@|mpaqet@|\/usr\/bin\/mgre|\/usr\/bin\/mxlan|\/usr\/local\/bin\/mporter-watchdog/' > "$tmpc"
    crontab "$tmpc" 2>/dev/null; rm -f "$tmpc"
  }
fi
rm -f /etc/systemd/system/mtunnel-smart-agent.service /etc/systemd/system/mtunnel-smart-watchdog.service /etc/systemd/system/mtunnel-smart-watchdog.timer \
      /etc/systemd/system/mgre-watchdog.service /etc/systemd/system/mgre-watchdog.timer /etc/systemd/system/mxlan-watchdog.service /etc/systemd/system/mxlan-watchdog.timer \
      /etc/systemd/system/mbackhaul-apply.service /etc/systemd/system/mrathole-apply.service /etc/systemd/system/mpaqet-apply.service \
      /etc/systemd/system/mporter-iptables.service /etc/systemd/system/mporter-obfs.service \
      /etc/systemd/system/mgre.service /etc/systemd/system/mxlan.service /etc/systemd/system/mporter.service /etc/systemd/system/mporter-watchdog.service \
      /etc/systemd/system/mbackhaul@.service /etc/systemd/system/mrathole@.service /etc/systemd/system/mpaqet@.service
rm -f /etc/systemd/system/multi-user.target.wants/mbackhaul@*.service /etc/systemd/system/multi-user.target.wants/mrathole@*.service /etc/systemd/system/multi-user.target.wants/mpaqet@*.service
rm -f /usr/local/bin/mhealer_daemon.sh /usr/local/bin/mporter-watchdog.sh /usr/local/bin/mporter-iptables.sh /usr/local/bin/mporter-obfs.sh /etc/mhealer.conf
rm -f /var/lock/mporter-state.lock /run/mporter-backends.tsv
if [ -f /etc/mporter/.owns_gost ]; then systemctl disable --now gost.service 2>/dev/null; rm -f /etc/systemd/system/gost.service; rm -rf /etc/gost; fi
if [ -f /etc/mporter/.owns_realm ]; then systemctl disable --now realm.service 2>/dev/null; rm -f /etc/systemd/system/realm.service; rm -rf /etc/realm; fi
if [ -f /etc/mporter/.owns_haproxy ]; then
  systemctl disable --now haproxy.service 2>/dev/null
  DEBIAN_FRONTEND=noninteractive apt-get purge -y haproxy >/dev/null 2>&1
  rm -rf /etc/haproxy /var/lib/haproxy
fi
rm -rf /run/mtunnel-healer
systemctl daemon-reload 2>/dev/null
rm -rf /var/lib/mtunnel/owned-binaries /usr/lib/mtunnel /etc/mgre /etc/mporter /etc/mrathole /etc/mbackhaul /etc/paqet /etc/mtunnel/smart /root/mtunnel
rmdir /etc/mtunnel 2>/dev/null
rm -f /usr/bin/mtunnel /usr/local/bin/mtunnel /usr/bin/msmart /usr/bin/mgre /usr/bin/mxlan /usr/bin/mbackhaul /usr/bin/mpaqet /usr/bin/mporter /usr/bin/mrathole /usr/bin/mdoctor /usr/bin/linktest /usr/bin/mmirror
rm -f /etc/sysctl.d/99-mtunnel-doctor.conf /etc/sysctl.d/99-mtunnel-bbr.conf /etc/sysctl.d/99-mporter-ipv4.conf /etc/sysctl.d/99-mporter-ipv6.conf
left=""
for p in /etc/mgre /etc/mporter /etc/mrathole /etc/mbackhaul /etc/paqet /etc/mtunnel/smart /root/mtunnel /usr/bin/mtunnel; do [ -e "$p" ] && left="$left $p"; done
if [ -n "$left" ]; then echo "WIPE_INCOMPLETE:$left"; else echo "WIPE_OK"; fi
`

// ── dump parsing ─────────────────────────────────────────────────────────────

type legacyDump struct {
	Found bool              `json:"found"`
	Has   []string          `json:"has"`
	Units []string          `json:"units"`
	IPv4  string            `json:"ipv4"`
	IPv6  string            `json:"ipv6"`
	Files map[string]string `json:"files"`
}

func parseLegacyDump(out string) legacyDump {
	d := legacyDump{Files: map[string]string{}}
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "@@HAS "):
			d.Has = append(d.Has, strings.TrimSpace(ln[6:]))
		case strings.HasPrefix(ln, "@@UNIT "):
			d.Units = append(d.Units, strings.TrimSpace(ln[7:]))
		case strings.HasPrefix(ln, "@@IP4"):
			d.IPv4 = strings.TrimSpace(strings.TrimPrefix(ln, "@@IP4"))
		case strings.HasPrefix(ln, "@@IP6"):
			d.IPv6 = strings.TrimSpace(strings.TrimPrefix(ln, "@@IP6"))
		case strings.HasPrefix(ln, "@@FILE "):
			path := strings.TrimSpace(ln[7:])
			if i+1 < len(lines) {
				raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[i+1]))
				if err == nil {
					d.Files[path] = string(raw)
				}
				i++
			}
		case strings.HasPrefix(ln, "@@DONE"):
			d.Found = strings.Contains(ln, "found=1")
		}
	}
	if len(d.Files) > 0 {
		d.Found = true
	}
	return d
}

func parseKV(content string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(strings.ReplaceAll(content, "\r", ""), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		i := strings.Index(ln, "=")
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(ln[:i])
		v := strings.TrimSpace(ln[i+1:])
		v = strings.Trim(v, `"'`)
		m[k] = v
	}
	return m
}

var safeLegacyName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

var paqetKeyRe = regexp.MustCompile(`(?m)^\s*key:\s*"?([^"\s]+)"?\s*$`)

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// extractLegacyTunnels turns the raw files into staged tunnel sides.
// "unsupported" lists data that MControl cannot re-create automatically.
func extractLegacyTunnels(serverID int64, d legacyDump) (tuns []db.LegacyTunnel, unsupported []string) {
	paths := make([]string, 0, len(d.Files))
	for p := range d.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		switch {
		// Backhaul: /etc/mbackhaul/tunnels/<name>.meta + <name>.toml
		case strings.HasPrefix(p, "/etc/mbackhaul/tunnels/") && strings.HasSuffix(p, ".meta"):
			name := strings.TrimSuffix(p[len("/etc/mbackhaul/tunnels/"):], ".meta")
			kv := parseKV(d.Files[p])
			cfg, ok := d.Files["/etc/mbackhaul/tunnels/"+name+".toml"]
			t := db.LegacyTunnel{
				ServerID: serverID, Engine: "backhaul", Name: name, Transport: kv["TRANSPORT"],
				TunnelPort: atoiSafe(kv["TUN_PORT"]), Token: kv["TOKEN"], Ports: kv["PORTS"],
				PeerHost: kv["REMOTE_IP"], Config: cfg, Status: "pending",
			}
			if kv["ROLE"] == "1" {
				t.Side = "iran"
			} else {
				t.Side = "foreign"
			}
			if !ok || strings.TrimSpace(cfg) == "" {
				t.Status, t.Note = "error", "config .toml missing next to .meta"
			}
			if kv["FORWARDER"] != "" && kv["FORWARDER"] != "backhaul" {
				t.Note = "external forwarder '" + kv["FORWARDER"] + "' was used; its rules are kept in the snapshot only"
			}
			tuns = append(tuns, t)

		// Rathole: /etc/mrathole/tunnels/<name>/meta.conf + config.toml
		case strings.HasPrefix(p, "/etc/mrathole/tunnels/") && strings.HasSuffix(p, "/meta.conf"):
			name := strings.TrimSuffix(p[len("/etc/mrathole/tunnels/"):], "/meta.conf")
			kv := parseKV(d.Files[p])
			cfg, ok := d.Files["/etc/mrathole/tunnels/"+name+"/config.toml"]
			t := db.LegacyTunnel{
				ServerID: serverID, Engine: "rathole", Name: name, Transport: "tcp",
				TunnelPort: atoiSafe(kv["LINK_PORT"]), Token: kv["TOKEN"], Ports: kv["TCP_PORTS"],
				PeerHost: kv["REMOTE_IP"], Config: cfg, Status: "pending",
			}
			if kv["TYPE"] == "1" {
				t.Side = "iran"
			} else {
				t.Side = "foreign"
			}
			if kv["UDP_PORTS"] != "" {
				t.Note = "UDP ports in original config: " + kv["UDP_PORTS"]
			}
			if !ok || strings.TrimSpace(cfg) == "" {
				t.Status, t.Note = "error", "config.toml missing next to meta.conf"
			}
			tuns = append(tuns, t)

		// Paqet: /etc/paqet/<name>.meta + <name>.yaml
		case strings.HasPrefix(p, "/etc/paqet/") && strings.HasSuffix(p, ".meta"):
			name := strings.TrimSuffix(p[len("/etc/paqet/"):], ".meta")
			kv := parseKV(d.Files[p])
			cfg, ok := d.Files["/etc/paqet/"+name+".yaml"]
			t := db.LegacyTunnel{
				ServerID: serverID, Engine: "paqet", Name: name, Transport: "kcp",
				TunnelPort: atoiSafe(kv["TUN_PORT"]), Ports: kv["TCP_PORTS"], PeerHost: kv["REMOTE_IP"],
				Config: cfg, Status: "pending",
			}
			if m := paqetKeyRe.FindStringSubmatch(cfg); len(m) == 2 {
				t.Token, t.PairKey = m[1], m[1]
			}
			if kv["ROLE"] == "1" {
				t.Side = "iran"
			} else {
				t.Side = "foreign"
			}
			if !ok || strings.TrimSpace(cfg) == "" {
				t.Status, t.Note = "error", ".yaml missing next to .meta"
			}
			tuns = append(tuns, t)

		// Kernel tunnels: stored only, never auto re-created (different builder).
		case strings.HasPrefix(p, "/etc/mgre/tunnels/") && strings.HasSuffix(p, ".conf"),
			strings.HasPrefix(p, "/etc/mgre/vxlan/") && strings.HasSuffix(p, ".conf"):
			engine := "gre"
			base := strings.TrimPrefix(p, "/etc/mgre/tunnels/")
			if strings.HasPrefix(p, "/etc/mgre/vxlan/") {
				engine = "vxlan"
				base = strings.TrimPrefix(p, "/etc/mgre/vxlan/")
			}
			name := strings.TrimSuffix(base, ".conf")
			kv := parseKV(d.Files[p])
			t := db.LegacyTunnel{
				ServerID: serverID, Engine: engine, Name: name, Transport: kv["TUN_PROTO"] + kv["FAB_PROTO"],
				PairKey: kv["SYNC_KEY"], Ports: strings.Trim(kv["FWD_TCP"]+","+kv["FWD_UDP"], ","),
				PeerHost: kv["REMOTE_PUB"], Config: d.Files[p], Status: "snapshot_only",
				Note: "kernel tunnel saved verbatim, not re-created automatically",
			}
			if kv["TYPE"] == "1" {
				t.Side = "iran"
			} else {
				t.Side = "foreign"
			}
			tuns = append(tuns, t)
			unsupported = append(unsupported, strings.ToUpper(engine)+" tunnel '"+name+"'")
		}
	}

	// Names come from the remote file system, so they are untrusted: they end up in
	// unit files and shell heredocs. Anything unusual is kept in the snapshot only.
	for i := range tuns {
		t := &tuns[i]
		if t.Status != "pending" {
			continue
		}
		if !safeLegacyName.MatchString(t.Name) {
			t.Status, t.Note = "error", "unsafe tunnel name, kept in the snapshot only"
		} else if strings.Contains(t.Config, "MC_CFG_EOF") || strings.Contains(t.Config, "MC_CERT_EOF") {
			t.Status, t.Note = "error", "config contains a reserved marker, kept in the snapshot only"
		}
	}

	hasPorter, hasSmart := false, false
	for p := range d.Files {
		if strings.HasPrefix(p, "/etc/mporter/") {
			hasPorter = true
		}
		if strings.HasPrefix(p, "/etc/mtunnel/smart/") {
			hasSmart = true
		}
	}
	if hasPorter {
		unsupported = append(unsupported, "MPorter forwarding rules (HAProxy / Gost / iptables)")
	}
	if hasSmart {
		unsupported = append(unsupported, "MTunnel Smart settings")
	}
	return tuns, unsupported
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) scanLegacy(srv *db.Server) (legacyDump, error) {
	res := h.runOnServer(srv, legacyScanScript, 30*time.Second)
	if res.Err != nil && res.Output == "" {
		return legacyDump{}, res.Err
	}
	return parseLegacyDump(res.Output), nil
}

// legacySummary is the short info returned right after a server is added.
func (h *Handler) legacySummary(srv *db.Server) map[string]any {
	d, err := h.scanLegacy(srv)
	if err != nil {
		return map[string]any{"found": false, "error": err.Error()}
	}
	tuns, unsupported := extractLegacyTunnels(srv.ID, d)
	return map[string]any{"found": d.Found, "tunnels": len(tuns), "unsupported": unsupported}
}

func (h *Handler) bundleInstallCmd(source string) (string, error) {
	st := h.store.GetSettings()
	irBase := strings.TrimRight(st.IranMirrorURL, "/")
	bundleGH := defaultBundleGH
	bundleIR := irBase + "/" + bundleFileName
	dlURL := bundleGH
	curlFlags := "-fsSL"
	switch source {
	case "mirror":
		dlURL = bundleIR
	case "master":
		curlFlags = "-kfsSL"
		if err := h.ensureCachedFile("bundle/"+bundleFileName, bundleGH, bundleIR); err != nil {
			return "", fmt.Errorf("failed to cache bundle on Master: %w", err)
		}
		dlURL = h.masterDistBaseURL(h.createDistToken()) + "/bundle/" + bundleFileName
	}
	return fmt.Sprintf(`set -e
TMPD=$(mktemp -d)
trap 'rm -rf "$TMPD"' EXIT
curl %s %s -o "$TMPD/bundle.tar.gz"
tar -xzf "$TMPD/bundle.tar.gz" -C "$TMPD"
cp -af "$TMPD/bin/"* /usr/local/bin/
chmod +x /usr/local/bin/bh /usr/local/bin/backhaul /usr/local/bin/rathole /usr/local/bin/paqet /usr/local/bin/realm /usr/local/bin/iperf3 /usr/local/bin/speedtest /usr/local/bin/jq /usr/local/bin/socat 2>/dev/null || true
%s
echo "bundle installed"
`, curlFlags, sshclient.ShQuote(dlURL), kernelModulesCmd), nil
}

// engineCheckCmd verifies that every engine needed by the found tunnels is present and executable.
func engineCheckCmd(engines map[string]bool) string {
	var b strings.Builder
	b.WriteString("miss=''\n")
	if engines["backhaul"] {
		b.WriteString(`B=$(command -v bh 2>/dev/null || command -v backhaul 2>/dev/null); [ -n "$B" ] && [ -x "$B" ] || miss="$miss backhaul(bh)"` + "\n")
	}
	if engines["rathole"] {
		b.WriteString(`command -v rathole >/dev/null 2>&1 || miss="$miss rathole"` + "\n")
	}
	if engines["paqet"] {
		b.WriteString(`command -v paqet >/dev/null 2>&1 || miss="$miss paqet"` + "\n")
	}
	b.WriteString(`if [ -n "$miss" ]; then echo "MISSING:$miss"; else echo "ENGINES_OK"; fi` + "\n")
	return b.String()
}

var certPathRe = regexp.MustCompile(`/etc/mbackhaul/certs/[A-Za-z0-9._-]+`)

// verbatimDeployScript writes the ORIGINAL config of one tunnel side under the
// MControl unit name (so MControl's start/stop/restart/logs/delete keep working).
func verbatimDeployScript(engine, unit, name string, tunnelID int64, cfg string, certFiles map[string]string) string {
	cfgDir := "/etc/mcontrol/tunnels"
	ext := "toml"
	if engine == "paqet" {
		ext = "yaml"
	}
	cfgPath := fmt.Sprintf("%s/%s.%s", cfgDir, unit, ext)

	var b strings.Builder
	b.WriteString("set -e\n")
	switch engine {
	case "backhaul":
		b.WriteString(`BIN=$(command -v bh 2>/dev/null || command -v backhaul 2>/dev/null || true)` + "\n")
	case "rathole":
		b.WriteString(`BIN=$(command -v rathole 2>/dev/null || true)` + "\n")
	default:
		b.WriteString(`BIN=$(command -v paqet 2>/dev/null || true)` + "\n")
	}
	b.WriteString(`[ -n "$BIN" ] || { echo "engine binary not installed" >&2; exit 1; }` + "\n")
	b.WriteString("mkdir -p " + cfgDir + " /etc/mcontrol/certs\n")
	for fn, content := range certFiles {
		b.WriteString(fmt.Sprintf("cat <<'MC_CERT_EOF' > /etc/mcontrol/certs/%s\n%s\nMC_CERT_EOF\nchmod 600 /etc/mcontrol/certs/%s\n", fn, strings.TrimRight(content, "\n"), fn))
	}
	b.WriteString(fmt.Sprintf("cat <<'MC_CFG_EOF' > %s\n%s\nMC_CFG_EOF\nchmod 600 %s\n", cfgPath, strings.TrimRight(cfg, "\n"), cfgPath))

	exec := ""
	switch engine {
	case "backhaul":
		exec = "$BIN -c " + cfgPath
	case "rathole":
		exec = "$BIN " + cfgPath
	default:
		exec = "$BIN run -c " + cfgPath
	}
	b.WriteString(fmt.Sprintf(`cat <<EOF > /etc/systemd/system/%s.service
[Unit]
Description=MControl %s Tunnel %d (%s, migrated from MTunnel)
After=network-online.target

[Service]
Type=simple
ExecStart=%s
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
`, unit, engine, tunnelID, name, exec, unit, unit, unit))
	return b.String()
}

// snapshotFiles returns the verbatim files saved for a server before it was wiped.
func (h *Handler) snapshotFiles(serverID int64) map[string]string {
	snap, err := h.store.GetLatestLegacySnapshot(serverID)
	if err != nil {
		return map[string]string{}
	}
	var d legacyDump
	if json.Unmarshal([]byte(snap.Data), &d) != nil || d.Files == nil {
		return map[string]string{}
	}
	return d.Files
}

// prepareConfig rewrites TLS certificate paths (MTunnel's cert dir is removed by the wipe).
func prepareConfig(cfg string, files map[string]string) (string, map[string]string) {
	certs := map[string]string{}
	out := certPathRe.ReplaceAllStringFunc(cfg, func(p string) string {
		base := p[strings.LastIndex(p, "/")+1:]
		if content, ok := files[p]; ok {
			certs[base] = content
		}
		return "/etc/mcontrol/certs/" + base
	})
	return out, certs
}

func hostMatches(peer string, srv *db.Server) bool {
	norm := func(s string) string {
		return strings.ToLower(strings.Trim(strings.TrimSpace(s), "[]"))
	}
	p := norm(peer)
	if p == "" {
		return false
	}
	return p == norm(srv.Host) || p == norm(srv.IPv4) || p == norm(srv.IPv6)
}

// pairLegacy re-creates every tunnel whose two sides are both staged and known.
func (h *Handler) pairLegacy() []string {
	var logs []string
	pending, err := h.store.ListLegacyTunnels(0, "pending")
	if err != nil {
		return []string{"pairing failed: " + err.Error()}
	}
	var irans, foreigns []db.LegacyTunnel
	for _, t := range pending {
		if t.Engine != "backhaul" && t.Engine != "rathole" && t.Engine != "paqet" {
			continue
		}
		if t.Side == "iran" {
			irans = append(irans, t)
		} else {
			foreigns = append(foreigns, t)
		}
	}
	used := map[int64]bool{}
	for _, f := range foreigns {
		fSrv, err := h.store.GetServerWithSecret(f.ServerID)
		if err != nil {
			continue
		}
		for _, i := range irans {
			if used[i.ID] || i.Engine != f.Engine || i.ServerID == f.ServerID || i.TunnelPort != f.TunnelPort {
				continue
			}
			iSrv, err := h.store.GetServerWithSecret(i.ServerID)
			if err != nil {
				continue
			}
			if i.Token != "" && f.Token != "" && i.Token != f.Token {
				continue
			}
			if (i.Token == "" || f.Token == "") && !hostMatches(f.PeerHost, iSrv) {
				continue
			}
			used[i.ID] = true
			logs = append(logs, h.deployLegacyPair(&i, &f, iSrv, fSrv)...)
			break
		}
	}
	return logs
}

func (h *Handler) deployLegacyPair(i, f *db.LegacyTunnel, iSrv, fSrv *db.Server) []string {
	var logs []string
	tun := &db.Tunnel{
		Name: i.Name, Engine: i.Engine, Transport: i.Transport,
		IranServerID: iSrv.ID, ForeignServerID: fSrv.ID, TunnelPort: i.TunnelPort,
		Token: i.Token, Ports: i.Ports, Forwarder: "internal", LBMode: "none", Status: "deploying",
	}
	if tun.Transport == "" {
		tun.Transport = "tcp"
	}
	id, err := h.store.AddTunnel(tun)
	if err != nil {
		return []string{"could not create tunnel record for " + i.Name + ": " + err.Error()}
	}
	unit := fmt.Sprintf("mcontrol-tun-%d", id)

	iCfg, iCerts := prepareConfig(i.Config, h.snapshotFiles(iSrv.ID))
	fCfg, fCerts := prepareConfig(f.Config, h.snapshotFiles(fSrv.ID))
	iranCmd := verbatimDeployScript(i.Engine, unit, i.Name, id, iCfg, iCerts)
	forCmd := verbatimDeployScript(f.Engine, unit, f.Name, id, fCfg, fCerts)

	var wg sync.WaitGroup
	var rI, rF sshclient.Result
	wg.Add(2)
	go func() { defer wg.Done(); rI = h.runOnServer(iSrv, iranCmd, 60*time.Second) }()
	go func() { defer wg.Done(); rF = h.runOnServer(fSrv, forCmd, 60*time.Second) }()
	wg.Wait()

	var errs []string
	if rI.Err != nil {
		errs = append(errs, "Iran: "+rI.Err.Error())
	}
	if rF.Err != nil {
		errs = append(errs, "Foreign: "+rF.Err.Error())
	}
	if len(errs) > 0 {
		msg := strings.Join(errs, " | ")
		_ = h.store.UpdateTunnelStatus(id, "error", msg)
		_ = h.store.SetLegacyTunnelState(i.ID, "error", msg, id)
		_ = h.store.SetLegacyTunnelState(f.ID, "error", msg, id)
		return append(logs, fmt.Sprintf("✖ %s (%s): re-deploy failed: %s", i.Name, i.Engine, msg))
	}
	_ = h.store.UpdateTunnelStatus(id, "active", "")
	_ = h.store.SetLegacyTunnelState(i.ID, "migrated", "re-created as MControl tunnel", id)
	_ = h.store.SetLegacyTunnelState(f.ID, "migrated", "re-created as MControl tunnel", id)
	return append(logs, fmt.Sprintf("✔ %s (%s) re-created as MControl tunnel #%d: %s ⇄ %s, same config, ports and token", i.Name, i.Engine, id, iSrv.Name, fSrv.Name))
}

// ── handlers ─────────────────────────────────────────────────────────────────

func (h *Handler) loadServerForLegacy(w http.ResponseWriter, r *http.Request) (*db.Server, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return nil, false
	}
	if srv.AuthMode != "peer" && srv.Password == "" {
		fail(w, http.StatusBadRequest, "SSH credential or Peer Agent v2 is required")
		return nil, false
	}
	return srv, true
}

// GET /api/servers/{id}/legacy/scan : read-only.
func (h *Handler) handleLegacyScan(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.loadServerForLegacy(w, r)
	if !ok {
		return
	}
	d, err := h.scanLegacy(srv)
	if err != nil {
		fail(w, http.StatusBadGateway, "scan failed: "+err.Error())
		return
	}
	tuns, unsupported := extractLegacyTunnels(srv.ID, d)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "found": d.Found, "tunnels": tuns, "unsupported": unsupported,
		"units": d.Units, "ipv4": d.IPv4, "ipv6": d.IPv6,
	})
}

// GET /api/servers/{id}/legacy/snapshot : the exact saved copy (decrypted, JSON download).
func (h *Handler) handleLegacySnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	snap, err := h.store.GetLatestLegacySnapshot(id)
	if err != nil {
		fail(w, http.StatusNotFound, "no MTunnel snapshot saved for this server")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="mtunnel-snapshot-server%d-%d.json"`, id, snap.CreatedAt))
	_, _ = w.Write([]byte(snap.Data))
}

// POST /api/servers/{id}/legacy/migrate
//
//	{ "wipe": true, "ack_unmigrated": false, "source": "github|mirror|master" }
//
// Without "wipe" it only imports + installs prerequisites (nothing is deleted).
func (h *Handler) handleLegacyMigrate(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.loadServerForLegacy(w, r)
	if !ok {
		return
	}
	var req struct {
		Wipe          bool   `json:"wipe"`
		AckUnmigrated bool   `json:"ack_unmigrated"`
		Source        string `json:"source"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source != "mirror" && req.Source != "master" {
		req.Source = "github"
	}
	var logs []string
	say := func(format string, a ...any) { logs = append(logs, fmt.Sprintf(format, a...)) }
	finish := func(okFlag bool, errMsg string, extra map[string]any) {
		out := map[string]any{"ok": okFlag, "log": logs, "error": errMsg}
		for k, v := range extra {
			out[k] = v
		}
		writeJSON(w, http.StatusOK, out)
	}

	// 1. scan
	d, err := h.scanLegacy(srv)
	if err != nil {
		fail(w, http.StatusBadGateway, "scan failed: "+err.Error())
		return
	}
	if !d.Found {
		say("No MTunnel installation found on %s.", srv.Name)
		finish(true, "", map[string]any{"found": false})
		return
	}
	tuns, unsupported := extractLegacyTunnels(srv.ID, d)
	say("Found MTunnel on %s: %d tunnel side(s), %d config file(s).", srv.Name, len(tuns), len(d.Files))

	if req.Wipe && len(unsupported) > 0 && !req.AckUnmigrated {
		say("Not removing anything: these items cannot be re-created automatically: %s", strings.Join(unsupported, "; "))
		finish(false, "unmigrated data present: confirm (ack_unmigrated) to remove MTunnel anyway; a full copy is kept in the snapshot", map[string]any{"unsupported": unsupported, "needs_ack": true})
		return
	}

	// 2. exact snapshot + staged rows (must succeed before anything is changed)
	raw, err := json.Marshal(d)
	if err != nil {
		finish(false, "snapshot encode failed: "+err.Error(), nil)
		return
	}
	snapID, err := h.store.AddLegacySnapshot(srv.ID, string(raw))
	if err != nil {
		finish(false, "could not save snapshot (nothing was changed): "+err.Error(), nil)
		return
	}
	say("Saved exact encrypted snapshot #%d of all MTunnel files.", snapID)
	engines := map[string]bool{}
	staged := 0
	for i := range tuns {
		t := tuns[i]
		if _, err := h.store.AddLegacyTunnel(&t); err != nil {
			finish(false, "could not stage tunnel '"+t.Name+"' (nothing was changed): "+err.Error(), nil)
			return
		}
		staged++
		if t.Status == "pending" {
			engines[t.Engine] = true
		}
		say("  • %s [%s, %s side] link port %d, ports %s", t.Name, t.Engine, t.Side, t.TunnelPort, t.Ports)
	}

	// 3. install prerequisites, then verify
	cmd, err := h.bundleInstallCmd(req.Source)
	if err != nil {
		finish(false, err.Error(), nil)
		return
	}
	say("Installing prerequisites and tunnel engines (source: %s)...", req.Source)
	ins := h.runOnServer(srv, cmd, 180*time.Second)
	if ins.Err != nil {
		finish(false, "prerequisite install failed (MTunnel left untouched): "+ins.Err.Error(), map[string]any{"output": ins.Output})
		return
	}
	chk := h.runOnServer(srv, engineCheckCmd(engines), 20*time.Second)
	if !strings.Contains(chk.Output, "ENGINES_OK") {
		finish(false, "engine check failed (MTunnel left untouched): "+strings.TrimSpace(chk.Output), map[string]any{"output": ins.Output})
		return
	}
	say("Prerequisites installed and verified.")

	if !req.Wipe {
		say("Import finished. MTunnel was NOT removed (wipe not requested).")
		finish(true, "", map[string]any{"found": true, "staged": staged, "snapshot_id": snapID, "wiped": false})
		return
	}

	// 4. wipe
	say("Removing MTunnel from %s...", srv.Name)
	wp := h.runOnServer(srv, legacyWipeScript, 120*time.Second)
	if !strings.Contains(wp.Output, "WIPE_OK") {
		finish(false, "MTunnel removal incomplete: "+strings.TrimSpace(wp.Output), map[string]any{"output": wp.Output, "staged": staged, "snapshot_id": snapID})
		return
	}
	say("MTunnel removed completely (engines installed by MControl were kept).")
	log.Printf("legacy MTunnel migrated+wiped on server %d by %s (snapshot %d, %d sides)", srv.ID, r.Header.Get("X-MC-User"), snapID, staged)

	// 5. re-create paired tunnels under MControl
	pl := h.pairLegacy()
	logs = append(logs, pl...)
	left, _ := h.store.ListLegacyTunnels(srv.ID, "pending")
	if len(left) > 0 {
		say("%d tunnel side(s) wait for their other side: add the other server to MControl and its tunnels will be re-created automatically.", len(left))
	}
	finish(true, "", map[string]any{"found": true, "staged": staged, "snapshot_id": snapID, "wiped": true, "waiting": len(left)})
}

// GET /api/legacy/tunnels : staged sides and their status.
func (h *Handler) handleLegacyList(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListLegacyTunnels(0, "")
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tunnels": list})
}

// POST /api/legacy/pair : retry pairing (e.g. after the second server was added).
func (h *Handler) handleLegacyPair(w http.ResponseWriter, r *http.Request) {
	logs := h.pairLegacy()
	if len(logs) == 0 {
		logs = []string{"Nothing to pair: no staged tunnel has both of its servers in MControl yet."}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": logs})
}
