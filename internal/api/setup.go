package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/sysinfo"
)

func (h *Handler) cacheDir() string {
	dir := filepath.Join(h.store.DataDir(), "cache")
	_ = os.MkdirAll(dir, 0750)
	return dir
}

func (h *Handler) createDistToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	h.distMu.Lock()
	defer h.distMu.Unlock()
	if h.distTokens == nil {
		h.distTokens = make(map[string]time.Time)
	}
	now := time.Now()
	for k, exp := range h.distTokens {
		if now.After(exp) {
			delete(h.distTokens, k)
		}
	}
	h.distTokens[tok] = now.Add(15 * time.Minute)
	return tok
}

func (h *Handler) validDistToken(tok string) bool {
	h.distMu.Lock()
	defer h.distMu.Unlock()
	if h.distTokens == nil {
		return false
	}
	exp, ok := h.distTokens[tok]
	return ok && time.Now().Before(exp)
}

// ServeDist serves cached binaries/scripts from Master to nodes via a short-lived signed token:
// GET /_mc_dist/<token>/<relative-path>
func (h *Handler) ServeDist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/_mc_dist/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || !h.validDistToken(parts[0]) {
		http.NotFound(w, r)
		return
	}
	rel := filepath.Clean("/" + parts[1])
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	base := h.cacheDir()
	full := filepath.Join(base, rel)
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, full)
}

func (h *Handler) masterDistBaseURL(token string) string {
	dir := h.store.DataDir()
	scheme := "http"
	if _, e1 := os.Stat(filepath.Join(dir, "tls.crt")); e1 == nil {
		if _, e2 := os.Stat(filepath.Join(dir, "tls.key")); e2 == nil {
			scheme = "https"
		}
	}
	st := h.store.GetSettings()
	host := strings.TrimSpace(st.ListenDomain)
	if host == "" {
		stats := sysinfo.Collect()
		if stats.IPv4 != "" && stats.IPv4 != "N/A" {
			host = stats.IPv4
		} else {
			host = "127.0.0.1"
		}
	}
	port := st.PanelPort
	if port == "" {
		port = h.currentPort
	}
	if port == "" {
		port = "2053"
	}
	return fmt.Sprintf("%s://%s:%s/_mc_dist/%s", scheme, host, port, token)
}

func (h *Handler) ensureCachedFile(relPath string, urls ...string) error {
	base := h.cacheDir()
	dest := filepath.Join(base, filepath.FromSlash(relPath))
	if fi, err := os.Stat(dest); err == nil && !fi.IsDir() && fi.Size() > 16 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		return err
	}
	client := &http.Client{Timeout: 90 * time.Second}
	var lastErr error
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		resp, err := client.Get(u)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
			continue
		}
		tmp := dest + ".dl.tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			resp.Body.Close()
			return err
		}
		_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 160<<20))
		f.Close()
		resp.Body.Close()
		if copyErr != nil {
			_ = os.Remove(tmp)
			lastErr = copyErr
			continue
		}
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no download URL available for %s", relPath)
	}
	return lastErr
}

const setupInspectScript = `
arch=$(uname -m 2>/dev/null || echo x86_64)
case "$arch" in aarch64|arm64) arch="arm64" ;; *) arch="amd64" ;; esac
os_name=$(awk -F= '/^PRETTY_NAME=/{gsub(/"/,""); print $2; exit}' /etc/os-release 2>/dev/null || uname -s)
has_bin() { command -v "$1" >/dev/null 2>&1 || [ -x "/usr/bin/$1" ] || [ -x "/usr/local/bin/$1" ] || [ -x "/opt/$1/$1" ] || [ -x "/root/$1" ]; }
pr_ok=1
for p in curl ip iptables socat jq openssl tar python3; do
  command -v "$p" >/dev/null 2>&1 || pr_ok=0
done
core_ok=0; core_ver=""
if systemctl is-active --quiet mcontrol-agent.service 2>/dev/null; then
  core_ok=1
  core_ver="v2"
fi
bh=0; has_bin backhaul && bh=1
rh=0; has_bin rathole && rh=1
pq=0; has_bin paqet && pq=1
gs=0; has_bin gost && gs=1
hp=0; has_bin haproxy && hp=1
sp=0; (has_bin speedtest || has_bin speedtest-cli) && sp=1
ag=0; systemctl is-active --quiet mcontrol-agent.service 2>/dev/null && ag=1
printf '%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s\n' "$arch" "$os_name" "$pr_ok" "$core_ok" "$core_ver" "$bh" "$rh" "$pq" "$gs" "$hp" "$sp" "$ag"
`

func (h *Handler) handleServerSetupStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	res := h.runOnServer(srv, setupInspectScript, 15*time.Second)
	if res.Err != nil && res.Output == "" {
		fail(w, http.StatusBadGateway, "failed to inspect server: "+res.Err.Error())
		return
	}
	lines := strings.Split(strings.TrimSpace(res.Output), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	p := strings.Split(last, "|")
	if len(p) < 12 {
		fail(w, http.StatusBadGateway, "unexpected inspect output: "+last)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"status": map[string]any{
			"arch":         p[0],
			"os":           p[1],
			"prereqs":      p[2] == "1",
			"core":         p[3] == "1",
			"core_version": p[4],
			"backhaul":     p[5] == "1",
			"rathole":      p[6] == "1",
			"paqet":        p[7] == "1",
			"gost":         p[8] == "1",
			"haproxy":      p[9] == "1",
			"speedtest":    p[10] == "1",
			"agent":        p[11] == "1",
		},
	})
}

const prereqsInstallCmd = `
set -e
echo "=== [1/3] Installing Phase 2 OS & Networking Prerequisites ==="
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq curl wget ca-certificates iproute2 iptables socat jq openssl tar gzip xz-utils python3 net-tools conntrack ethtool iputils-ping lsof >/dev/null
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y -q curl wget ca-certificates iproute iptables socat jq openssl tar gzip xz python3 net-tools conntrack-tools ethtool iputils lsof >/dev/null
elif command -v yum >/dev/null 2>&1; then
  yum install -y -q curl wget ca-certificates iproute iptables socat jq openssl tar gzip xz python3 net-tools conntrack-tools ethtool iputils lsof >/dev/null
else
  echo "Warning: unsupported package manager; skipping apt/dnf."
fi
echo "=== [2/3] Loading Kernel Tunnel Modules (GRE / IP6GRE / VXLAN / TUN) ==="
for m in ip_gre ip6_gre vxlan tun; do
  modprobe "$m" 2>/dev/null || true
done
mkdir -p /etc/modules-load.d
printf "ip_gre\nip6_gre\nvxlan\ntun\n" > /etc/modules-load.d/mcontrol.conf
echo "=== [3/3] Ensuring Kernel Forwarding & Sysctl Defaults ==="
sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null 2>&1 || true
echo "Prerequisites installed successfully."
`

func (h *Handler) buildCoreCmd(source, ghBase, irBase, masterBase string) (string, error) {
	switch source {
	case "mirror":
		return fmt.Sprintf(`echo "=== Installing MTunnel Core from Iran Mirror ==="; curl -fsSL %s | bash -s -- --remote %s --no-launch`,
			sshclient.ShQuote(irBase+"/install.sh"), sshclient.ShQuote(irBase)), nil
	case "master":
		if err := h.ensureCachedFile("mtunnel/install.sh", ghBase+"/install.sh", irBase+"/install.sh"); err != nil {
			return "", fmt.Errorf("failed to cache mtunnel/install.sh on Master: %w", err)
		}
		// Check if user uploaded a pre-built mtunnel binary in cache/mtunnel/mtunnel
		if _, err := os.Stat(filepath.Join(h.cacheDir(), "mtunnel", "mtunnel")); err == nil {
			return fmt.Sprintf(`echo "=== Installing MTunnel Core directly from Master Cache ==="; curl -kfsSL %s -o /usr/bin/mtunnel && chmod +x /usr/bin/mtunnel && echo "MTunnel binary installed from Master: $(/usr/bin/mtunnel --version 2>/dev/null | head -n1)"`,
				sshclient.ShQuote(masterBase+"/mtunnel/mtunnel")), nil
		}
		return fmt.Sprintf(`echo "=== Installing MTunnel Core script from Master ==="; curl -kfsSL %s | bash -s -- --online --no-launch`,
			sshclient.ShQuote(masterBase+"/mtunnel/install.sh")), nil
	default:
		return fmt.Sprintf(`echo "=== Installing MTunnel Core from Official GitHub ==="; curl -fsSL %s | bash -s -- --online --no-launch`,
			sshclient.ShQuote(ghBase+"/install.sh")), nil
	}
}

func (h *Handler) buildEngineCmd(engine, arch, source, ghBase, irBase, masterBase string) string {
	if arch != "arm64" {
		arch = "amd64"
	}
	if engine == "haproxy" {
		return `echo "=== Installing HAProxy ==="; (command -v apt-get >/dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq haproxy) || (command -v dnf >/dev/null 2>&1 && dnf install -y -q haproxy) || (command -v yum >/dev/null 2>&1 && yum install -y -q haproxy); haproxy -v | head -n1`
	}

	// Candidate direct download URLs for each tunnel engine
	var ghURL, irURL, assetHint string
	switch engine {
	case "backhaul":
		ghURL = fmt.Sprintf("https://github.com/Musixal/Backhaul/releases/latest/download/backhaul_linux_%s.tar.gz", arch)
		irURL = fmt.Sprintf("%s/bin/backhaul_linux_%s.tar.gz", irBase, arch)
		assetHint = "backhaul"
	case "rathole":
		if arch == "arm64" {
			ghURL = "https://github.com/rapiz1/rathole/releases/latest/download/rathole-aarch64-unknown-linux-musl.zip"
		} else {
			ghURL = "https://github.com/rapiz1/rathole/releases/latest/download/rathole-x86_64-unknown-linux-gnu.zip"
		}
		irURL = fmt.Sprintf("%s/bin/rathole_linux_%s.tar.gz", irBase, arch)
		assetHint = "rathole"
	case "paqet":
		ghURL = fmt.Sprintf("%s/bin/paqet_linux_%s", ghBase, arch)
		irURL = fmt.Sprintf("%s/bin/paqet_linux_%s", irBase, arch)
		assetHint = "paqet"
	case "gost":
		ghURL = fmt.Sprintf("https://github.com/go-gost/gost/releases/download/v3.0.0/gost_3.0.0_linux_%s.tar.gz", arch)
		irURL = fmt.Sprintf("%s/bin/gost_linux_%s.tar.gz", irBase, arch)
		assetHint = "gost"
	default:
		return fmt.Sprintf("echo 'Unknown engine: %s'", engine)
	}

	var dlURL string
	curlFlags := "-fsSL"
	switch source {
	case "mirror":
		dlURL = irURL
	case "master":
		curlFlags = "-kfsSL"
		// Prefer custom uploaded raw binary in Master cache (engines/<name> or engines/<arch>/<name>)
		customRel := ""
		for _, cand := range []string{
			fmt.Sprintf("engines/%s/%s", arch, engine),
			fmt.Sprintf("engines/%s", engine),
			fmt.Sprintf("engines/%s/%s.tar.gz", arch, engine),
		} {
			if fi, err := os.Stat(filepath.Join(h.cacheDir(), filepath.FromSlash(cand))); err == nil && !fi.IsDir() && fi.Size() > 16 {
				customRel = cand
				break
			}
		}
		if customRel == "" {
			// Auto-cache from GitHub or Mirror onto Master first
			ext := ""
			if strings.HasSuffix(ghURL, ".tar.gz") {
				ext = ".tar.gz"
			} else if strings.HasSuffix(ghURL, ".zip") {
				ext = ".zip"
			}
			customRel = fmt.Sprintf("engines/%s/%s%s", arch, engine, ext)
			if err := h.ensureCachedFile(customRel, ghURL, irURL); err != nil {
				return fmt.Sprintf("echo 'Failed to cache %s on Master: %s' >&2; exit 1", engine, strings.ReplaceAll(err.Error(), "'", ""))
			}
		}
		dlURL = masterBase + "/" + customRel
	default:
		dlURL = ghURL
	}

	return fmt.Sprintf(`
set -e
echo "=== Installing %s (%s) from %s ==="
TMPD=$(mktemp -d)
trap 'rm -rf "$TMPD"' EXIT
URL=%s
curl %s "$URL" -o "$TMPD/pkg"
if file "$TMPD/pkg" 2>/dev/null | grep -qi 'gzip'; then
  tar -xzf "$TMPD/pkg" -C "$TMPD"
  BIN=$(find "$TMPD" -maxdepth 3 -type f -name %s | head -n1)
  [ -n "$BIN" ] || BIN=$(find "$TMPD" -maxdepth 3 -type f ! -name pkg | head -n1)
  install -m 0755 "$BIN" /usr/local/bin/%s
elif head -c2 "$TMPD/pkg" | grep -q 'PK'; then
  python3 -c "import zipfile,sys; zipfile.ZipFile(sys.argv[1]).extractall(sys.argv[2])" "$TMPD/pkg" "$TMPD"
  BIN=$(find "$TMPD" -maxdepth 3 -type f -name %s | head -n1)
  install -m 0755 "$BIN" /usr/local/bin/%s
else
  install -m 0755 "$TMPD/pkg" /usr/local/bin/%s
fi
echo "%s installed to /usr/local/bin/%s"
`, engine, arch, source, sshclient.ShQuote(dlURL), curlFlags, sshclient.ShQuote(assetHint), engine, sshclient.ShQuote(assetHint), engine, engine, engine, engine)
}

func (h *Handler) handleServerSetupRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"` // prereqs | core | tunnels | engine | all
		Engine string `json:"engine"` // backhaul | rathole | paqet | gost | haproxy
		Source string `json:"source"` // github | mirror | master
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Source != "mirror" && req.Source != "master" {
		req.Source = "github"
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	if srv.AuthMode != "peer" && srv.Password == "" {
		fail(w, http.StatusBadRequest, "SSH credential or Peer Agent v2 is required for Setup")
		return
	}

	st := h.store.GetSettings()
	ghBase := strings.TrimRight(st.GitHubRepoURL, "/")
	irBase := strings.TrimRight(st.IranMirrorURL, "/")
	masterBase := ""
	if req.Source == "master" {
		masterBase = h.masterDistBaseURL(h.createDistToken())
	}

	// Detect remote arch quickly when installing engines
	arch := "amd64"
	if req.Action == "engine" || req.Action == "tunnels" || req.Action == "all" {
		ar := h.runOnServer(srv, `uname -m`, 8*time.Second)
		if strings.Contains(ar.Output, "aarch64") || strings.Contains(ar.Output, "arm64") {
			arch = "arm64"
		}
	}

	var parts []string
	switch req.Action {
	case "prereqs":
		parts = append(parts, prereqsInstallCmd)
	case "core":
		if srv.PeerCode == "" {
			fail(w, http.StatusBadRequest, "Use Pair (Peer Code) first to install MControl Peer Agent v2")
			return
		}
		parts = append(parts, sshclient.PeerAgentInstallScript(srv.PeerCode, srv.AgentPort))
	case "engine":
		parts = append(parts, h.buildEngineCmd(req.Engine, arch, req.Source, ghBase, irBase, masterBase))
	case "tunnels":
		for _, eng := range []string{"backhaul", "rathole", "gost", "haproxy"} {
			parts = append(parts, "("+h.buildEngineCmd(eng, arch, req.Source, ghBase, irBase, masterBase)+") || echo 'Warning: "+eng+" skipped/failed'")
		}
	case "all":
		parts = append(parts, prereqsInstallCmd)
		for _, eng := range []string{"backhaul", "rathole", "gost", "haproxy"} {
			parts = append(parts, "("+h.buildEngineCmd(eng, arch, req.Source, ghBase, irBase, masterBase)+") || echo 'Warning: "+eng+" skipped/failed'")
		}
		parts = append(parts, `echo "=== Full Setup Finished ==="`)
	default:
		fail(w, http.StatusBadRequest, "unsupported setup action")
		return
	}

	fullCmd := strings.Join(parts, "\n")
	log.Printf("setup action=%s engine=%s source=%s on server %d by %s", req.Action, req.Engine, req.Source, id, r.Header.Get("X-MC-User"))
	res := h.runOnServer(srv, fullCmd, 180*time.Second)

	// Re-probe server to refresh core status
	go func(sid int64) { _, _ = h.probeServer(sid) }(id)

	errStr := ""
	if res.Err != nil {
		errStr = res.Err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     res.Err == nil,
		"output": res.Output,
		"error":  errStr,
	})
}

func (h *Handler) handleCacheList(w http.ResponseWriter, r *http.Request) {
	base := h.cacheDir()
	type item struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
		Mod  int64  `json:"mod"`
	}
	var items []item
	_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.HasSuffix(p, ".tmp") {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err == nil {
			items = append(items, item{Path: filepath.ToSlash(rel), Size: info.Size(), Mod: info.ModTime().Unix()})
		}
		return nil
	})
	if items == nil {
		items = []item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

func (h *Handler) handleCacheUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 160<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, "failed to parse upload")
		return
	}
	target := strings.TrimSpace(r.FormValue("target"))
	allowed := map[string]string{
		"backhaul": "engines/backhaul",
		"rathole":  "engines/rathole",
		"paqet":    "engines/paqet",
		"gost":     "engines/gost",
	}
	rel, ok := allowed[target]
	if !ok {
		fail(w, http.StatusBadRequest, "invalid cache target (backhaul, rathole, paqet, gost)")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "binary/archive file is required")
		return
	}
	defer file.Close()

	dest := filepath.Join(h.cacheDir(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0750); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := io.Copy(out, file)
	out.Close()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": rel, "size": n})
}

func (h *Handler) handleCacheDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	rel := strings.TrimPrefix(filepath.Clean("/"+req.Path), "/")
	if rel == "" || strings.Contains(rel, "..") {
		fail(w, http.StatusBadRequest, "invalid path")
		return
	}
	base := h.cacheDir()
	full := filepath.Join(base, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, base+string(os.PathSeparator)) {
		fail(w, http.StatusBadRequest, "invalid path")
		return
	}
	_ = os.Remove(full)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
