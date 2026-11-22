package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	outboundSocksPort = 10808
	outboundCfgPath   = "/etc/mcontrol/xray-outbound.json"
	outboundUnit      = "mcontrol-outbound.service"
)

type outboundConfig struct {
	Enabled     bool   `json:"enabled"`
	Link        string `json:"link"`
	UseGitHub   bool   `json:"use_github"`
	UseTelegram bool   `json:"use_telegram"`
	UseForeign  bool   `json:"use_foreign"`
}

func (h *Handler) loadOutboundCfg() outboundConfig {
	raw := h.store.GetSetting("master_outbound_json")
	var cfg outboundConfig
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg
}

func (h *Handler) saveOutboundCfg(cfg outboundConfig) {
	if b, err := json.Marshal(cfg); err == nil {
		_ = h.store.SetSetting("master_outbound_json", string(b))
	}
}

func isOutboundActive() bool {
	out, err := exec.Command("systemctl", "is-active", outboundUnit).Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func xrayBinPath() string {
	for _, p := range []string{"/usr/local/bin/xray", "/usr/bin/xray"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("xray"); err == nil {
		return p
	}
	return ""
}

func (h *Handler) handleOutboundStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadOutboundCfg()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"config":         cfg,
		"xray_installed": xrayBinPath() != "",
		"running":        isOutboundActive(),
		"socks_addr":     fmt.Sprintf("127.0.0.1:%d", outboundSocksPort),
	})
}

func parseLinkToXrayOutbound(link string) (map[string]any, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, fmt.Errorf("outbound link is empty")
	}
	low := strings.ToLower(link)

	switch {
	case strings.HasPrefix(low, "vless://"):
		u, err := url.Parse(link)
		if err != nil {
			return nil, fmt.Errorf("invalid vless URI: %w", err)
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 443
		}
		q := u.Query()
		sec := q.Get("security")
		if sec == "" {
			sec = "none"
		}
		netType := q.Get("type")
		if netType == "" {
			netType = "tcp"
		}
		stream := map[string]any{"network": netType, "security": sec}
		if sec == "tls" {
			tlsSet := map[string]any{"allowInsecure": q.Get("allowInsecure") == "1"}
			if sni := q.Get("sni"); sni != "" {
				tlsSet["serverName"] = sni
			}
			if fp := q.Get("fp"); fp != "" {
				tlsSet["fingerprint"] = fp
			}
			stream["tlsSettings"] = tlsSet
		} else if sec == "reality" {
			stream["realitySettings"] = map[string]any{
				"serverName":  q.Get("sni"),
				"fingerprint": q.Get("fp"),
				"publicKey":   q.Get("pbk"),
				"shortId":     q.Get("sid"),
				"spiderX":     q.Get("spx"),
			}
		}
		if netType == "ws" {
			ws := map[string]any{"path": q.Get("path")}
			if host := q.Get("host"); host != "" {
				ws["headers"] = map[string]any{"Host": host}
			}
			stream["wsSettings"] = ws
		} else if netType == "grpc" {
			stream["grpcSettings"] = map[string]any{"serviceName": q.Get("serviceName")}
		}
		user := map[string]any{"id": u.User.Username(), "encryption": "none"}
		if fl := q.Get("flow"); fl != "" {
			user["flow"] = fl
		}
		return map[string]any{
			"tag":      "proxy",
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": u.Hostname(),
					"port":    port,
					"users":   []any{user},
				}},
			},
			"streamSettings": stream,
		}, nil

	case strings.HasPrefix(low, "trojan://"):
		u, err := url.Parse(link)
		if err != nil {
			return nil, fmt.Errorf("invalid trojan URI: %w", err)
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 443
		}
		q := u.Query()
		sni := q.Get("sni")
		if sni == "" {
			sni = u.Hostname()
		}
		return map[string]any{
			"tag":      "proxy",
			"protocol": "trojan",
			"settings": map[string]any{
				"servers": []any{map[string]any{
					"address":  u.Hostname(),
					"port":     port,
					"password": u.User.Username(),
				}},
			},
			"streamSettings": map[string]any{
				"network":  "tcp",
				"security": "tls",
				"tlsSettings": map[string]any{
					"serverName":    sni,
					"allowInsecure": q.Get("allowInsecure") == "1",
				},
			},
		}, nil

	case strings.HasPrefix(low, "vmess://"):
		b64 := strings.TrimPrefix(link, "vmess://")
		b64 = strings.TrimPrefix(b64, "VMESS://")
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(b64))
			if err != nil {
				return nil, fmt.Errorf("invalid vmess base64")
			}
		}
		var vm map[string]any
		if err := json.Unmarshal(raw, &vm); err != nil {
			return nil, fmt.Errorf("invalid vmess JSON")
		}
		addr := fmt.Sprint(vm["add"])
		port, _ := strconv.Atoi(fmt.Sprint(vm["port"]))
		if port == 0 {
			port = 443
		}
		uid := fmt.Sprint(vm["id"])
		netType := fmt.Sprint(vm["net"])
		if netType == "" || netType == "<nil>" {
			netType = "tcp"
		}
		sec := "none"
		if fmt.Sprint(vm["tls"]) == "tls" {
			sec = "tls"
		}
		stream := map[string]any{"network": netType, "security": sec}
		if sec == "tls" {
			sni := fmt.Sprint(vm["sni"])
			if sni == "" || sni == "<nil>" {
				sni = fmt.Sprint(vm["host"])
			}
			stream["tlsSettings"] = map[string]any{"serverName": sni, "allowInsecure": true}
		}
		if netType == "ws" {
			stream["wsSettings"] = map[string]any{
				"path":    fmt.Sprint(vm["path"]),
				"headers": map[string]any{"Host": fmt.Sprint(vm["host"])},
			}
		}
		return map[string]any{
			"tag":      "proxy",
			"protocol": "vmess",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": addr,
					"port":    port,
					"users":   []any{map[string]any{"id": uid, "security": "auto"}},
				}},
			},
			"streamSettings": stream,
		}, nil

	case strings.HasPrefix(low, "socks://") || strings.HasPrefix(low, "socks5://"):
		u, err := url.Parse(link)
		if err != nil {
			return nil, fmt.Errorf("invalid socks URI: %w", err)
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 1080
		}
		srv := map[string]any{"address": u.Hostname(), "port": port}
		if u.User != nil {
			p, _ := u.User.Password()
			srv["users"] = []any{map[string]any{"user": u.User.Username(), "pass": p}}
		}
		return map[string]any{
			"tag":      "proxy",
			"protocol": "socks",
			"settings": map[string]any{"servers": []any{srv}},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported URI scheme (use vless://, vmess://, trojan://, or socks5://)")
	}
}

func ensureXrayInstalled() (string, error) {
	if p := xrayBinPath(); p != "" {
		return p, nil
	}
	cmd := exec.Command("bash", "-c", `set -e
TMPD=$(mktemp -d)
trap 'rm -rf "$TMPD"' EXIT
curl -fsSL "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-64.zip" -o "$TMPD/xray.zip"
python3 -c "import zipfile; zipfile.ZipFile('$TMPD/xray.zip').extract('xray', '$TMPD')"
install -m 755 "$TMPD/xray" /usr/local/bin/xray`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to install xray: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return "/usr/local/bin/xray", nil
}

func (h *Handler) handleOutboundSave(w http.ResponseWriter, r *http.Request) {
	var req outboundConfig
	if !readJSON(w, r, &req) {
		return
	}
	req.Link = strings.TrimSpace(req.Link)
	h.saveOutboundCfg(req)

	if !req.Enabled || req.Link == "" {
		_ = exec.Command("systemctl", "disable", "--now", outboundUnit).Run()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": false, "message": "Master Outbound disabled."})
		return
	}

	outObj, err := parseLinkToXrayOutbound(req.Link)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	bin, err := ensureXrayInstalled()
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}

	fullCfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"tag":      "mc-local-socks",
				"listen":   "127.0.0.1",
				"port":     outboundSocksPort,
				"protocol": "socks",
				"settings": map[string]any{"auth": "noauth", "udp": true},
			},
		},
		"outbounds": []any{outObj},
	}

	_ = os.MkdirAll("/etc/mcontrol", 0750)
	b, _ := json.MarshalIndent(fullCfg, "", "  ")
	if err := os.WriteFile(outboundCfgPath, b, 0600); err != nil {
		fail(w, http.StatusInternalServerError, "write config failed: "+err.Error())
		return
	}

	unitTxt := fmt.Sprintf(`[Unit]
Description=MControl Master Isolated Outbound (Xray SOCKS5 127.0.0.1:%d)
After=network-online.target

[Service]
Type=simple
ExecStart=%s run -c %s
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, outboundSocksPort, bin, outboundCfgPath)

	if err := os.WriteFile("/etc/systemd/system/"+outboundUnit, []byte(unitTxt), 0644); err != nil {
		fail(w, http.StatusInternalServerError, "write service failed: "+err.Error())
		return
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	_ = exec.Command("systemctl", "enable", outboundUnit).Run()
	if out, err := exec.Command("systemctl", "restart", outboundUnit).CombinedOutput(); err != nil {
		fail(w, http.StatusInternalServerError, "start outbound failed: "+strings.TrimSpace(string(out)))
		return
	}
	time.Sleep(800 * time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"running":    isOutboundActive(),
		"socks_addr": fmt.Sprintf("127.0.0.1:%d", outboundSocksPort),
		"message":    fmt.Sprintf("Master Outbound active on 127.0.0.1:%d", outboundSocksPort),
	})
}

func (h *Handler) handleOutboundStop(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadOutboundCfg()
	cfg.Enabled = false
	h.saveOutboundCfg(cfg)
	_ = exec.Command("systemctl", "disable", "--now", outboundUnit).Run()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": false})
}

func (h *Handler) handleOutboundTest(w http.ResponseWriter, r *http.Request) {
	if !isOutboundActive() {
		fail(w, http.StatusBadRequest, "Master Outbound service is not running. Save & Start it first.")
		return
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf(`
t_gh=$(curl -sS --max-time 8 --socks5-hostname 127.0.0.1:%d -o /dev/null -w "HTTP %%{http_code} | %%{time_total}s" https://api.github.com/zen 2>&1 || echo "FAILED")
t_tg=$(curl -sS --max-time 8 --socks5-hostname 127.0.0.1:%d -o /dev/null -w "HTTP %%{http_code} | %%{time_total}s" https://api.telegram.org 2>&1 || echo "FAILED")
echo "GitHub API  : $t_gh"
echo "Telegram API: $t_tg"
`, outboundSocksPort, outboundSocksPort))
	out, _ := cmd.CombinedOutput()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": strings.TrimSpace(string(out))})
}
