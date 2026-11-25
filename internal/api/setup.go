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

const bundleFileName = "mcontrol-bundle-linux-amd64.tar.gz"
const defaultBundleGH = "https://github.com/htzserv/MControl/releases/download/bundle-latest/" + bundleFileName

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


func (h *Handler) HasDistToken(r *http.Request) bool {
	rest := strings.TrimPrefix(r.URL.Path, "/_mc_dist/")
	parts := strings.SplitN(rest, "/", 2)
	return len(parts) == 2 && h.validDistToken(parts[0])
}

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
	rel := strings.TrimPrefix(filepath.Clean("/"+parts[1]), "/")
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
	client := &http.Client{Timeout: 120 * time.Second}
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
		_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 200<<20))
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
has_bin() { command -v "$1" >/dev/null 2>&1 || [ -x "/usr/local/bin/$1" ] || [ -x "/usr/bin/$1" ]; }
ip3=0; has_bin iperf3 && ip3=1
sp=0; (has_bin speedtest || has_bin speedtest-cli) && sp=1
jqo=0; has_bin jq && jqo=1
bh=0; (has_bin bh || has_bin backhaul) && bh=1
rh=0; has_bin rathole && rh=1
pq=0; has_bin paqet && pq=1
rl=0; has_bin realm && rl=1
km=0; [ "$(cat /proc/sys/net/ipv4/ip_forward 2>/dev/null)" = "1" ] && km=1
ag=0; systemctl is-active --quiet mcontrol-agent.service 2>/dev/null && ag=1
printf '%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s\n' "$arch" "$os_name" "$ip3" "$sp" "$jqo" "$bh" "$rh" "$pq" "$rl" "$km" "$ag"
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
	if len(p) < 11 {
		fail(w, http.StatusBadGateway, "unexpected inspect output: "+last)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"status": map[string]any{
			"arch":      p[0],
			"os":        p[1],
			"iperf3":    p[2] == "1",
			"speedtest": p[3] == "1",
			"jq":        p[4] == "1",
			"backhaul":  p[5] == "1",
			"rathole":   p[6] == "1",
			"paqet":     p[7] == "1",
			"realm":     p[8] == "1",
			"kernel":    p[9] == "1",
			"agent":     p[10] == "1",
		},
	})
}

const kernelModulesCmd = `
echo "=== Enabling Kernel Tunnel Modules & IP Forwarding ==="
for m in ip_gre ip6_gre vxlan tun; do
  modprobe "$m" 2>/dev/null || true
done
mkdir -p /etc/modules-load.d
printf "ip_gre\nip6_gre\nvxlan\ntun\n" > /etc/modules-load.d/mcontrol.conf
sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null 2>&1 || true
echo "Kernel modules & IP forwarding ready."
`

func (h *Handler) handleServerSetupRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		Engine string `json:"engine"`
		Source string `json:"source"`
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
	irBase := strings.TrimRight(st.IranMirrorURL, "/")
	bundleGH := defaultBundleGH
	bundleIR := irBase + "/" + bundleFileName

	var cmd string
	switch req.Action {
	case "kernel":
		cmd = kernelModulesCmd
	case "all", "bundle":
		dlURL := bundleGH
		curlFlags := "-fsSL"
		if req.Source == "mirror" {
			dlURL = bundleIR
		} else if req.Source == "master" {
			curlFlags = "-kfsSL"
			if err := h.ensureCachedFile("bundle/"+bundleFileName, bundleGH, bundleIR); err != nil {
				fail(w, http.StatusBadGateway, "failed to cache bundle on Master: "+err.Error())
				return
			}
			dlURL = h.masterDistBaseURL(h.createDistToken()) + "/bundle/" + bundleFileName
		}
		cmd = fmt.Sprintf(`set -e
echo "=== Downloading Static MControl Bundle (Zero-APT / Zero-Unzip) from %s ==="
TMPD=$(mktemp -d)
trap 'rm -rf "$TMPD"' EXIT
curl %s %s -o "$TMPD/bundle.tar.gz"
echo "=== Extracting static binaries to /usr/local/bin/ ==="
tar -xzf "$TMPD/bundle.tar.gz" -C "$TMPD"
cp -af "$TMPD/bin/"* /usr/local/bin/
chmod +x /usr/local/bin/bh /usr/local/bin/backhaul /usr/local/bin/rathole /usr/local/bin/paqet /usr/local/bin/realm /usr/local/bin/iperf3 /usr/local/bin/speedtest /usr/local/bin/jq /usr/local/bin/socat 2>/dev/null || true
%s
echo "=== All Prerequisites & Tunnel Engines Installed Successfully ==="
`, req.Source, curlFlags, sshclient.ShQuote(dlURL), kernelModulesCmd)
	case "engine":
		eng := strings.TrimSpace(req.Engine)
		if eng == "backhaul" {
			eng = "bh"
		}
		validEng := map[string]bool{
			"bh": true, "rathole": true, "paqet": true, "realm": true,
			"iperf3": true, "speedtest": true, "jq": true, "socat": true,
		}
		if !validEng[eng] {
			fail(w, http.StatusBadRequest, "unsupported component: "+eng)
			return
		}
		fallbackCmd := ""
		if eng == "iperf3" || eng == "jq" || eng == "socat" {
			fallbackCmd = fmt.Sprintf(` || (command -v apt-get >/dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq %s) || (command -v dnf >/dev/null 2>&1 && dnf install -y -q %s)`, eng, eng)
		}
		if req.Source == "master" {
			rel := "engines/" + eng
			if _, err := os.Stat(filepath.Join(h.cacheDir(), filepath.FromSlash(rel))); err == nil {
				dlURL := h.masterDistBaseURL(h.createDistToken()) + "/" + rel
				cmd = fmt.Sprintf(`set -e; curl -kfsSL %s -o /usr/local/bin/%s && chmod 755 /usr/local/bin/%s && echo "%s installed from Master Cache."`, sshclient.ShQuote(dlURL), eng, eng, eng)
				if eng == "bh" {
					cmd += ` && ln -sf /usr/local/bin/bh /usr/local/bin/backhaul`
				}
			} else {
				if err := h.ensureCachedFile("bundle/"+bundleFileName, bundleGH, bundleIR); err != nil {
					fail(w, http.StatusBadGateway, "failed to cache bundle on Master: "+err.Error())
					return
				}
				dlURL := h.masterDistBaseURL(h.createDistToken()) + "/bundle/" + bundleFileName
				cmd = fmt.Sprintf(`set -e; TMPD=$(mktemp -d); trap 'rm -rf "$TMPD"' EXIT; curl -kfsSL %s -o "$TMPD/b.tar.gz"; tar -xzf "$TMPD/b.tar.gz" -C "$TMPD"; ( [ -s "$TMPD/bin/%s" ] && install -m 755 "$TMPD/bin/%s" /usr/local/bin/%s && (/usr/local/bin/%s --version >/dev/null 2>&1 || /usr/local/bin/%s version >/dev/null 2>&1 || [ -x /usr/local/bin/%s ]) )%s; [ "%s" = "bh" ] && ln -sf /usr/local/bin/bh /usr/local/bin/backhaul || true; echo "%s installed."`, sshclient.ShQuote(dlURL), eng, eng, eng, eng, eng, eng, fallbackCmd, eng, eng)
			}
		} else {
			dlURL := bundleGH
			if req.Source == "mirror" {
				dlURL = bundleIR
			}
			cmd = fmt.Sprintf(`set -e; TMPD=$(mktemp -d); trap 'rm -rf "$TMPD"' EXIT; curl -fsSL %s -o "$TMPD/b.tar.gz"; tar -xzf "$TMPD/b.tar.gz" -C "$TMPD"; ( [ -s "$TMPD/bin/%s" ] && install -m 755 "$TMPD/bin/%s" /usr/local/bin/%s && (/usr/local/bin/%s --version >/dev/null 2>&1 || /usr/local/bin/%s version >/dev/null 2>&1 || [ -x /usr/local/bin/%s ]) )%s; [ "%s" = "bh" ] && ln -sf /usr/local/bin/bh /usr/local/bin/backhaul || true; echo "%s installed."`, sshclient.ShQuote(dlURL), eng, eng, eng, eng, eng, eng, fallbackCmd, eng, eng)
		}
	default:
		fail(w, http.StatusBadRequest, "unsupported setup action")
		return
	}

	log.Printf("setup action=%s engine=%s source=%s on server %d by %s", req.Action, req.Engine, req.Source, id, r.Header.Get("X-MC-User"))
	res := h.runOnServer(srv, cmd, 180*time.Second)
	errStr := ""
	if res.Err != nil {
		errStr = res.Err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": res.Output, "error": errStr})
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
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, "failed to parse upload")
		return
	}
	target := strings.TrimSpace(r.FormValue("target"))
	allowed := map[string]string{
		"bundle":  "bundle/" + bundleFileName,
		"bh":      "engines/bh",
		"rathole": "engines/rathole",
		"paqet":   "engines/paqet",
		"realm":   "engines/realm",
	}
	rel, ok := allowed[target]
	if !ok {
		fail(w, http.StatusBadRequest, "invalid cache target (bundle, bh, rathole, paqet, realm)")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "file is required")
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
