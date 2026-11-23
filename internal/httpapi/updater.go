package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/version"
)

func releaseBaseForChannel(ch string) (string, string) {
	if strings.EqualFold(strings.TrimSpace(ch), "beta") {
		return "beta", "https://github.com/htzserv/MControl/releases/download/v2-beta"
	}
	return "stable", "https://github.com/htzserv/MControl/releases/download/v2-latest"
}

func fetchWithMirror(client *http.Client, rawURL string) (*http.Response, error) {
	resp, err := client.Get(rawURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	return client.Get("https://gh-proxy.com/" + rawURL)
}

func (s *Server) handleAdminCheckUpdate(w http.ResponseWriter, r *http.Request) {
	ch, base := releaseBaseForChannel(r.URL.Query().Get("channel"))
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := fetchWithMirror(client, base+"/VERSION")
	if err != nil {
		fail(w, http.StatusBadGateway, "failed to reach GitHub: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail(w, http.StatusBadGateway, fmt.Sprintf("GitHub (%s) returned HTTP %d", ch, resp.StatusCode))
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	latest := strings.TrimSpace(string(raw))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"channel":          ch,
		"current":          version.Current,
		"latest":           latest,
		"update_available": latest != "" && latest != version.Current,
	})
}

func (s *Server) handleAdminApplyUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	_ = readJSON(w, r, &req)
	if req.Channel == "" {
		req.Channel = r.URL.Query().Get("channel")
	}
	ch, base := releaseBaseForChannel(req.Channel)

	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		fail(w, http.StatusBadRequest, "unsupported architecture: "+arch)
		return
	}
	binURL := fmt.Sprintf("%s/mcontrol-linux-%s", base, arch)
	shaURL := binURL + ".sha256"

	client := &http.Client{Timeout: 60 * time.Second}

	shaResp, err := fetchWithMirror(client, shaURL)
	if err != nil || shaResp.StatusCode != http.StatusOK {
		fail(w, http.StatusBadGateway, "failed to fetch SHA256 checksum for "+ch)
		return
	}
	shaRaw, _ := io.ReadAll(io.LimitReader(shaResp.Body, 256))
	_ = shaResp.Body.Close()
	expectedSha := strings.Fields(strings.TrimSpace(string(shaRaw)))
	if len(expectedSha) == 0 {
		fail(w, http.StatusBadGateway, "invalid sha256 file")
		return
	}

	binResp, err := fetchWithMirror(client, binURL)
	if err != nil || binResp.StatusCode != http.StatusOK {
		fail(w, http.StatusBadGateway, "failed to download release binary for "+ch)
		return
	}
	defer binResp.Body.Close()

	exePath, err := os.Executable()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := exePath + ".new"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		fail(w, http.StatusInternalServerError, "cannot write temp binary: "+err.Error())
		return
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hasher), binResp.Body); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		fail(w, http.StatusBadGateway, "download interrupted: "+err.Error())
		return
	}
	_ = out.Close()

	gotSha := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSha, expectedSha[0]) {
		_ = os.Remove(tmpPath)
		fail(w, http.StatusBadGateway, "checksum mismatch — update aborted")
		return
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Remove(tmpPath)
		fail(w, http.StatusInternalServerError, "failed to replace binary: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"channel": ch,
		"message": fmt.Sprintf("Update (%s) verified & applied! Restarting panel...", strings.ToUpper(ch)),
	})

	go func() {
		time.Sleep(800 * time.Millisecond)
		_ = exec.Command("systemctl", "restart", "mcontrol").Run()
		os.Exit(0)
	}()
}

// ── SwiftProxy (Quick Authenticated SOCKS5/HTTP Proxy Deploy) ────────────────

func (s *Server) handleTenantDeploySwiftProxy(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req struct {
		ServerID int64  `json:"server_id"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		req.Port = 10808
	}
	if strings.TrimSpace(req.Username) == "" {
		req.Username = "mcontrol"
	}
	if strings.TrimSpace(req.Password) == "" {
		fail(w, http.StatusBadRequest, "proxy password is required")
		return
	}
	srv, err := s.engine.GetServerWithSecret(r.Context(), claims.UserID, req.ServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}

	cmd := fmt.Sprintf(`if [ ! -x /usr/local/bin/gost ]; then
  ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  (curl -fsSL --connect-timeout 8 "https://github.com/go-gost/gost/releases/download/v3.0.0-rc10/gost_3.0.0-rc10_linux_${ARCH}.tar.gz" -o /tmp/gost.tar.gz || curl -fsSL --connect-timeout 10 "https://gh-proxy.com/https://github.com/go-gost/gost/releases/download/v3.0.0-rc10/gost_3.0.0-rc10_linux_${ARCH}.tar.gz" -o /tmp/gost.tar.gz) && tar -xzf /tmp/gost.tar.gz -C /usr/local/bin/ gost && chmod +x /usr/local/bin/gost && rm -f /tmp/gost.tar.gz
fi
cat <<'EOF' > /etc/systemd/system/mcontrol-swiftproxy.service
[Unit]
Description=MControl SwiftProxy (SOCKS5+HTTP)
After=network-online.target

[Service]
ExecStart=/usr/local/bin/gost -L %s:%s@:%d
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now mcontrol-swiftproxy.service
systemctl is-active mcontrol-swiftproxy.service
echo "SWIFTPROXY_OK"`, req.Username, req.Password, req.Port)

	res := sshclient.Run(toSSHTarget(srv), cmd, 10*time.Second, 60*time.Second)
	ok := res.Err == nil && strings.Contains(res.Output, "SWIFTPROXY_OK")
	proxyURL := fmt.Sprintf("socks5://%s:%s@%s:%d", req.Username, req.Password, srv.Host, req.Port)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        ok,
		"proxy_url": proxyURL,
		"output":    strings.TrimSpace(res.Output),
	})
}
