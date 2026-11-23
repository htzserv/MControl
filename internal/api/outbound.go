package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	outboundSocksPort = 10808
	outboundCfgPath   = "/etc/mcontrol/xray-outbound.json"
	outboundUnit      = "mcontrol-outbound.service"
)

type outboundNode struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Proto   string `json:"proto"`
	Address string `json:"address"`
	Source  string `json:"source"` // "manual" or subscription ID
	Enabled     bool   `json:"enabled"`
	LastDelayMs int    `json:"last_delay_ms"` // 0=untested, -1=timeout/error, >0=ms
}

type outboundSub struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	LastSync  int64  `json:"last_sync"`
	NodeCount int    `json:"node_count"`
}

type outboundConfig struct {
	Enabled       bool           `json:"enabled"`
	Link          string         `json:"link"`           // legacy single-link field
	LBStrategy    string         `json:"lb_strategy"`    // leastPing | leastLoad | roundRobin | random
	ProbeURL      string         `json:"probe_url"`      // default https://www.gstatic.com/generate_204
	ProbeInterval string         `json:"probe_interval"` // default 30s
	UseGitHub     bool           `json:"use_github"`
	UseTelegram   bool           `json:"use_telegram"`
	UseForeign    bool           `json:"use_foreign"`
	Subs          []outboundSub  `json:"subs"`
	Nodes         []outboundNode `json:"nodes"`
}

func shortRandID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Handler) loadOutboundCfg() outboundConfig {
	raw := h.store.GetSetting("master_outbound_json")
	var cfg outboundConfig
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.LBStrategy == "" {
		cfg.LBStrategy = "leastPing"
	}
	if cfg.ProbeURL == "" {
		cfg.ProbeURL = "https://www.gstatic.com/generate_204"
	}
	if cfg.ProbeInterval == "" {
		cfg.ProbeInterval = "30s"
	}
	if cfg.Subs == nil {
		cfg.Subs = []outboundSub{}
	}
	if cfg.Nodes == nil {
		cfg.Nodes = []outboundNode{}
	}
	// Auto-migrate legacy single Link into Nodes list
	if len(cfg.Nodes) == 0 && strings.TrimSpace(cfg.Link) != "" {
		if n, ok := parseNodeSummary(cfg.Link, "manual"); ok {
			cfg.Nodes = append(cfg.Nodes, n)
		}
	}
	return cfg
}

func (h *Handler) saveOutboundCfg(cfg outboundConfig) {
	if b, err := json.Marshal(cfg); err == nil {
		_ = h.store.SetSetting("master_outbound_json", string(b))
	}
}


func (h *Handler) telegramSocksProxy() string {
	cfg := h.loadOutboundCfg()
	if cfg.Enabled && cfg.UseTelegram && isOutboundActive() {
		return fmt.Sprintf("127.0.0.1:%d", outboundSocksPort)
	}
	return ""
}

func (h *Handler) githubSocksProxy() string {
	cfg := h.loadOutboundCfg()
	if cfg.Enabled && cfg.UseGitHub && isOutboundActive() {
		return fmt.Sprintf("127.0.0.1:%d", outboundSocksPort)
	}
	return ""
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

var xrayVerRe = regexp.MustCompile(`Xray\s+([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

func xrayVersionString() string {
	bin := xrayBinPath()
	if bin == "" {
		return ""
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "installed"
	}
	if m := xrayVerRe.FindStringSubmatch(string(out)); len(m) == 2 {
		return "v" + m[1]
	}
	firstLine := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	if len(firstLine) > 40 {
		firstLine = firstLine[:40]
	}
	return firstLine
}

func parseNodeSummary(uri, source string) (outboundNode, bool) {
	uri = strings.TrimSpace(uri)
	low := strings.ToLower(uri)
	var proto, name, addr string

	switch {
	case strings.HasPrefix(low, "vless://"):
		proto = "VLESS"
		if u, err := url.Parse(uri); err == nil {
			addr = u.Host
			name = u.Fragment
		}
	case strings.HasPrefix(low, "trojan://"):
		proto = "TROJAN"
		if u, err := url.Parse(uri); err == nil {
			addr = u.Host
			name = u.Fragment
		}
	case strings.HasPrefix(low, "vmess://"):
		proto = "VMESS"
		b64 := strings.TrimSpace(uri[8:])
		if raw, err := decodeFlexibleBase64(b64); err == nil {
			var vm map[string]any
			if json.Unmarshal(raw, &vm) == nil {
				addr = fmt.Sprintf("%v:%v", vm["add"], vm["port"])
				if ps, ok := vm["ps"].(string); ok {
					name = ps
				}
			}
		}
	case strings.HasPrefix(low, "ss://"):
		proto = "SS"
		if u, err := url.Parse(uri); err == nil {
			addr = u.Host
			name = u.Fragment
		}
	case strings.HasPrefix(low, "socks://") || strings.HasPrefix(low, "socks5://"):
		proto = "SOCKS5"
		if u, err := url.Parse(uri); err == nil {
			addr = u.Host
			name = u.Fragment
		}
	default:
		return outboundNode{}, false
	}

	if dec, err := url.QueryUnescape(name); err == nil && dec != "" {
		name = dec
	}
	name = strings.TrimSpace(name)
	if name == "" {
		if addr != "" {
			name = proto + " • " + addr
		} else {
			name = proto + " Node"
		}
	}
	return outboundNode{
		ID:      shortRandID(),
		Name:    name,
		URI:     uri,
		Proto:   proto,
		Address: addr,
		Source:  source,
		Enabled: true,
	}, true
}

func decodeFlexibleBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("base64 decode failed")
}

func parseSubscriptionBody(body, subID string) []outboundNode {
	txt := strings.TrimSpace(body)
	if !strings.Contains(txt, "://") {
		if dec, err := decodeFlexibleBase64(txt); err == nil {
			txt = string(dec)
		}
	}
	var nodes []outboundNode
	seen := map[string]bool{}
	for _, line := range strings.Split(txt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		if n, ok := parseNodeSummary(line, subID); ok {
			seen[line] = true
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func (h *Handler) handleOutboundStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadOutboundCfg()
	installed := xrayBinPath() != ""
	ver := ""
	if installed {
		ver = xrayVersionString()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"config":         cfg,
		"xray_installed": installed,
		"xray_version":   ver,
		"running":        isOutboundActive(),
		"socks_addr":     fmt.Sprintf("127.0.0.1:%d", outboundSocksPort),
	})
}

func (h *Handler) handleOutboundInstallCore(w http.ResponseWriter, r *http.Request) {
	bin, err := installOrUpdateXray()
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	ver := xrayVersionString()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"xray_installed": bin != "",
		"xray_version":   ver,
		"message":        "Xray Core installed: " + ver,
	})
}

func (h *Handler) handleOutboundFetchSub(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	req.Name = strings.TrimSpace(req.Name)
	if req.URL == "" || (!strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://")) {
		fail(w, http.StatusBadRequest, "valid http(s) subscription URL is required")
		return
	}
	if req.Name == "" {
		if u, err := url.Parse(req.URL); err == nil && u.Hostname() != "" {
			req.Name = u.Hostname()
		} else {
			req.Name = "Subscription"
		}
	}

	// Fetch subscription (direct first; fallback via curl --socks5-hostname if outbound is active)
	var bodyStr string
	client := &http.Client{Timeout: 12 * time.Second}
	httpReq, _ := http.NewRequest("GET", req.URL, nil)
	httpReq.Header.Set("User-Agent", "MControl/1.2.0 v2rayN/6.0")
	resp, err := client.Do(httpReq)
	if err == nil && resp.StatusCode == http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		bodyStr = string(raw)
	} else {
		if resp != nil {
			resp.Body.Close()
		}
		if isOutboundActive() {
			out, cerr := exec.Command("curl", "-fsSL", "--max-time", "14", "--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", outboundSocksPort), "-A", "MControl/1.2.0", req.URL).Output()
			if cerr == nil {
				bodyStr = string(out)
			}
		}
	}
	if strings.TrimSpace(bodyStr) == "" {
		fail(w, http.StatusBadGateway, "failed to download subscription URL (check connectivity or enable Outbound first)")
		return
	}

	cfg := h.loadOutboundCfg()
	subID := req.ID
	if subID == "" {
		subID = "sub-" + shortRandID()
	}

	parsed := parseSubscriptionBody(bodyStr, subID)
	if len(parsed) == 0 {
		fail(w, http.StatusBadRequest, "no supported VLESS/VMess/Trojan/SS/SOCKS5 nodes found in subscription")
		return
	}

	// Update or add sub entry
	foundSub := false
	for i := range cfg.Subs {
		if cfg.Subs[i].ID == subID {
			cfg.Subs[i].Name = req.Name
			cfg.Subs[i].URL = req.URL
			cfg.Subs[i].LastSync = time.Now().Unix()
			cfg.Subs[i].NodeCount = len(parsed)
			foundSub = true
			break
		}
	}
	if !foundSub {
		cfg.Subs = append(cfg.Subs, outboundSub{
			ID:        subID,
			Name:      req.Name,
			URL:       req.URL,
			LastSync:  time.Now().Unix(),
			NodeCount: len(parsed),
		})
	}

	// Replace old nodes from this sub and append fresh ones
	kept := make([]outboundNode, 0, len(cfg.Nodes)+len(parsed))
	for _, n := range cfg.Nodes {
		if n.Source != subID {
			kept = append(kept, n)
		}
	}
	kept = append(kept, parsed...)
	cfg.Nodes = kept
	h.saveOutboundCfg(cfg)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"added":   len(parsed),
		"config":  cfg,
		"message": fmt.Sprintf("Synced %d nodes from %s", len(parsed), req.Name),
	})
}

func parseLinkToXrayOutbound(link, tag string) (map[string]any, error) {
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
		} else if netType == "xhttp" || netType == "splithttp" {
			xh := map[string]any{"path": q.Get("path")}
			if host := q.Get("host"); host != "" {
				xh["host"] = host
			}
			stream["xhttpSettings"] = xh
		}
		user := map[string]any{"id": u.User.Username(), "encryption": "none"}
		if fl := q.Get("flow"); fl != "" {
			user["flow"] = fl
		}
		return map[string]any{
			"tag":      tag,
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
		netType := q.Get("type")
		if netType == "" {
			netType = "tcp"
		}
		stream := map[string]any{
			"network":  netType,
			"security": "tls",
			"tlsSettings": map[string]any{
				"serverName":    sni,
				"allowInsecure": q.Get("allowInsecure") == "1",
			},
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
		return map[string]any{
			"tag":      tag,
			"protocol": "trojan",
			"settings": map[string]any{
				"servers": []any{map[string]any{
					"address":  u.Hostname(),
					"port":     port,
					"password": u.User.Username(),
				}},
			},
			"streamSettings": stream,
		}, nil

	case strings.HasPrefix(low, "vmess://"):
		b64 := strings.TrimSpace(link[8:])
		raw, err := decodeFlexibleBase64(b64)
		if err != nil {
			return nil, fmt.Errorf("invalid vmess base64")
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
			"tag":      tag,
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

	case strings.HasPrefix(low, "ss://"):
		u, err := url.Parse(link)
		if err != nil {
			return nil, fmt.Errorf("invalid ss URI: %w", err)
		}
		var method, password, host string
		var port int
		if u.User != nil && u.Hostname() != "" {
			host = u.Hostname()
			port, _ = strconv.Atoi(u.Port())
			if p, ok := u.User.Password(); ok {
				method = u.User.Username()
				password = p
			} else if dec, err := decodeFlexibleBase64(u.User.Username()); err == nil {
				parts := strings.SplitN(string(dec), ":", 2)
				if len(parts) == 2 {
					method, password = parts[0], parts[1]
				}
			}
		} else {
			// Legacy full base64 ss://base64(method:pass@host:port)
			rawPart := strings.SplitN(link[5:], "#", 2)[0]
			if dec, err := decodeFlexibleBase64(rawPart); err == nil {
				if u2, err := url.Parse("ss://" + string(dec)); err == nil && u2.User != nil {
					host = u2.Hostname()
					port, _ = strconv.Atoi(u2.Port())
					method = u2.User.Username()
					password, _ = u2.User.Password()
				}
			}
		}
		if host == "" || port == 0 || method == "" {
			return nil, fmt.Errorf("incomplete shadowsocks URI")
		}
		return map[string]any{
			"tag":      tag,
			"protocol": "shadowsocks",
			"settings": map[string]any{
				"servers": []any{map[string]any{
					"address":  host,
					"port":     port,
					"method":   method,
					"password": password,
				}},
			},
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
			"tag":      tag,
			"protocol": "socks",
			"settings": map[string]any{"servers": []any{srv}},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported URI scheme (use vless://, vmess://, trojan://, ss://, or socks5://)")
	}
}

func installOrUpdateXray() (string, error) {
	proxyFlag := ""
	if isOutboundActive() {
		proxyFlag = fmt.Sprintf("--socks5-hostname 127.0.0.1:%d", outboundSocksPort)
	}
	script := fmt.Sprintf(`set -e
TMPD=$(mktemp -d)
trap 'rm -rf "$TMPD"' EXIT
ARCH=$(uname -m)
ASSET="Xray-linux-64.zip"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then ASSET="Xray-linux-arm64-v8a.zip"; fi
URL="https://github.com/XTLS/Xray-core/releases/latest/download/${ASSET}"
curl -fsSL %s "$URL" -o "$TMPD/xray.zip" || curl -fsSL "$URL" -o "$TMPD/xray.zip"
python3 -c "import zipfile; zipfile.ZipFile('$TMPD/xray.zip').extract('xray', '$TMPD')"
install -m 755 "$TMPD/xray" /usr/local/bin/xray`, proxyFlag)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to install xray: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return "/usr/local/bin/xray", nil
}

func ensureXrayInstalled() (string, error) {
	if p := xrayBinPath(); p != "" {
		return p, nil
	}
	return installOrUpdateXray()
}

func (h *Handler) handleOutboundSave(w http.ResponseWriter, r *http.Request) {
	var req outboundConfig
	if !readJSON(w, r, &req) {
		return
	}
	if req.LBStrategy == "" {
		req.LBStrategy = "leastPing"
	}
	if req.ProbeURL == "" {
		req.ProbeURL = "https://www.gstatic.com/generate_204"
	}
	if req.ProbeInterval == "" {
		req.ProbeInterval = "30s"
	}
	if req.Subs == nil {
		req.Subs = []outboundSub{}
	}
	if req.Nodes == nil {
		req.Nodes = []outboundNode{}
	}
	// Legacy single link fallback
	req.Link = strings.TrimSpace(req.Link)
	if len(req.Nodes) == 0 && req.Link != "" {
		if n, ok := parseNodeSummary(req.Link, "manual"); ok {
			req.Nodes = append(req.Nodes, n)
		}
	}
	h.saveOutboundCfg(req)

	if !req.Enabled {
		_ = exec.Command("systemctl", "disable", "--now", outboundUnit).Run()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": false, "message": "Master Outbound disabled & saved."})
		return
	}

	// Build Xray outbounds array from all enabled nodes
	var outbounds []any
	var firstErr error
	for _, n := range req.Nodes {
		if !n.Enabled || strings.TrimSpace(n.URI) == "" {
			continue
		}
		tag := fmt.Sprintf("proxy-%d", len(outbounds))
		obj, err := parseLinkToXrayOutbound(n.URI, tag)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		outbounds = append(outbounds, obj)
	}
	if len(outbounds) == 0 {
		msg := "no enabled valid outbound nodes found"
		if firstErr != nil {
			msg = firstErr.Error()
		}
		fail(w, http.StatusBadRequest, msg)
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
		"outbounds": outbounds,
	}

	// Enable Xray Observatory + Load Balancer + Auto-Failover whenever 2+ nodes are active
	if len(outbounds) > 1 {
		strat := req.LBStrategy
		switch strat {
		case "leastPing", "leastLoad", "roundRobin", "random":
		default:
			strat = "leastPing"
		}
		if strat == "leastLoad" {
			fullCfg["burstObservatory"] = map[string]any{
				"subjectSelector": []string{"proxy-"},
				"pingConfig": map[string]any{
					"destination":  req.ProbeURL,
					"interval":     req.ProbeInterval,
					"sampling":     3,
					"timeout":      "5s",
				},
			}
		} else {
			fullCfg["observatory"] = map[string]any{
				"subjectSelector":   []string{"proxy-"},
				"probeUrl":          req.ProbeURL,
				"probeInterval":     req.ProbeInterval,
				"enableConcurrency": true,
			}
		}
		fullCfg["routing"] = map[string]any{
			"balancers": []any{
				map[string]any{
					"tag":         "mc-balancer",
					"selector":    []string{"proxy-"},
					"strategy":    map[string]any{"type": strat},
					"fallbackTag": "proxy-0",
				},
			},
			"rules": []any{
				map[string]any{
					"type":        "field",
					"inboundTag":  []string{"mc-local-socks"},
					"balancerTag": "mc-balancer",
				},
			},
		}
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
	time.Sleep(850 * time.Millisecond)
	active := isOutboundActive()
	modeDesc := "Single Node"
	if len(outbounds) > 1 {
		modeDesc = fmt.Sprintf("%d Nodes (%s + Auto-Failover)", len(outbounds), req.LBStrategy)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"running":      active,
		"xray_version": xrayVersionString(),
		"active_nodes": len(outbounds),
		"socks_addr":   fmt.Sprintf("127.0.0.1:%d", outboundSocksPort),
		"message":      fmt.Sprintf("Master Outbound active on 127.0.0.1:%d • %s", outboundSocksPort, modeDesc),
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

func freeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func (h *Handler) handleOutboundRealDelay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID string `json:"node_id"` // optional: empty = test all nodes
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)

	bin := xrayBinPath()
	if bin == "" {
		fail(w, http.StatusBadRequest, "Xray Core is not installed. Click Install Xray Core first.")
		return
	}

	cfg := h.loadOutboundCfg()
	if len(cfg.Nodes) == 0 {
		fail(w, http.StatusBadRequest, "no outbound nodes to test")
		return
	}

	type testItem struct {
		idx  int
		id   string
		port int
	}
	var items []testItem
	var inbounds []any
	var outbounds []any
	var rules []any

	for i, n := range cfg.Nodes {
		if req.NodeID != "" && n.ID != req.NodeID {
			continue
		}
		if strings.TrimSpace(n.URI) == "" {
			cfg.Nodes[i].LastDelayMs = -1
			continue
		}
		inTag := fmt.Sprintf("tin-%d", i)
		outTag := fmt.Sprintf("tout-%d", i)
		outObj, err := parseLinkToXrayOutbound(n.URI, outTag)
		if err != nil {
			cfg.Nodes[i].LastDelayMs = -1
			continue
		}
		p, err := freeLocalPort()
		if err != nil {
			cfg.Nodes[i].LastDelayMs = -1
			continue
		}
		inbounds = append(inbounds, map[string]any{
			"tag":      inTag,
			"listen":   "127.0.0.1",
			"port":     p,
			"protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": false},
		})
		outbounds = append(outbounds, outObj)
		rules = append(rules, map[string]any{
			"type":        "field",
			"inboundTag":  []string{inTag},
			"outboundTag": outTag,
		})
		items = append(items, testItem{idx: i, id: n.ID, port: p})
	}

	if len(items) == 0 {
		h.saveOutboundCfg(cfg)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": cfg, "ok_count": 0, "failed_count": 0})
		return
	}

	tmpCfg := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   map[string]any{"rules": rules},
	}
	tmpFile, err := os.CreateTemp("", "mc-xray-delay-*.json")
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := tmpCfgPath(tmpFile)
	b, _ := json.Marshal(tmpCfg)
	_, _ = tmpFile.Write(b)
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command(bin, "run", "-c", tmpPath)
	if err := cmd.Start(); err != nil {
		fail(w, http.StatusInternalServerError, "failed to start xray tester: "+err.Error())
		return
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	time.Sleep(700 * time.Millisecond)

	probeURL := cfg.ProbeURL
	if probeURL == "" {
		probeURL = "https://www.gstatic.com/generate_204"
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10)
	okCount, failCount := 0, 0
	bestMs := 0

	for _, it := range items {
		wg.Add(1)
		go func(item testItem) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			proxy := fmt.Sprintf("127.0.0.1:%d", item.port)
			out, err := exec.Command("curl", "-sS", "--max-time", "6", "--socks5-hostname", proxy, "-o", "/dev/null", "-w", "%{http_code} %{time_total}", probeURL).Output()
			delayMs := -1
			if err == nil {
				parts := strings.Fields(strings.TrimSpace(string(out)))
				if len(parts) == 2 {
					code, _ := strconv.Atoi(parts[0])
					sec, _ := strconv.ParseFloat(parts[1], 64)
					if code >= 200 && code < 400 && sec > 0 {
						delayMs = int(sec * 1000)
						if delayMs < 1 {
							delayMs = 1
						}
					}
				}
			}
			mu.Lock()
			cfg.Nodes[item.idx].LastDelayMs = delayMs
			if delayMs > 0 {
				okCount++
				if bestMs == 0 || delayMs < bestMs {
					bestMs = delayMs
				}
			} else {
				failCount++
			}
			mu.Unlock()
		}(it)
	}
	wg.Wait()

	h.saveOutboundCfg(cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"config":       cfg,
		"ok_count":     okCount,
		"failed_count": failCount,
		"best_ms":      bestMs,
	})
}

func tmpCfgPath(f *os.File) string {
	return f.Name()
}
