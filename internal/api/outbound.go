package api

import (
	"bytes"
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
	ID          string `json:"id"`
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Proto       string `json:"proto"`
	Address     string `json:"address"`
	Source      string `json:"source"` // "manual" or subscription ID
	Enabled     bool   `json:"enabled"`
	LastDelayMs int    `json:"last_delay_ms"` // 0=untested, -1=timeout/error, >0=ms
}

type outboundSub struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	LastSync      int64  `json:"last_sync"`
	NodeCount     int    `json:"node_count"`
	UploadBytes   int64  `json:"upload_bytes,omitempty"`
	DownloadBytes int64  `json:"download_bytes,omitempty"`
	TotalBytes    int64  `json:"total_bytes,omitempty"`
	Expire        int64  `json:"expire,omitempty"`
}

func parseSubUserinfo(hdr string) (up, down, total, expire int64) {
	for _, part := range strings.Split(hdr, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(kv[0]))
		v, _ := strconv.ParseInt(strings.TrimSpace(kv[1]), 10, 64)
		switch k {
		case "upload":
			up = v
		case "download":
			down = v
		case "total":
			total = v
		case "expire":
			expire = v
		}
	}
	return
}

type outboundConfig struct {
	Enabled       bool           `json:"enabled"`
	Mode          string         `json:"mode"`           // "single" (default) | "loadbalance"
	ActiveNodeID  string         `json:"active_node_id"` // selected node ID when Mode == "single"
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

func mapStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}

func (h *Handler) loadOutboundCfg() outboundConfig {
	raw := h.store.GetSetting("master_outbound_json")
	var cfg outboundConfig
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.Mode != "single" && cfg.Mode != "loadbalance" {
		cfg.Mode = "single"
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
	if len(cfg.Nodes) == 0 && strings.TrimSpace(cfg.Link) != "" {
		if n, ok := parseNodeSummary(cfg.Link, "manual"); ok {
			cfg.Nodes = append(cfg.Nodes, n)
			if cfg.ActiveNodeID == "" {
				cfg.ActiveNodeID = n.ID
			}
		}
	}
	if len(cfg.Nodes) > 0 {
		found := false
		for _, n := range cfg.Nodes {
			if n.ID == cfg.ActiveNodeID {
				found = true
				break
			}
		}
		if !found {
			for _, n := range cfg.Nodes {
				if n.Enabled {
					cfg.ActiveNodeID = n.ID
					found = true
					break
				}
			}
			if !found {
				cfg.ActiveNodeID = cfg.Nodes[0].ID
			}
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
	if err != nil || strings.TrimSpace(string(out)) != "active" {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", outboundSocksPort), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
				add := mapStr(vm, "add")
				port := mapStr(vm, "port")
				if add != "" {
					addr = add + ":" + port
				}
				name = mapStr(vm, "ps")
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

func (h *Handler) handleOutboundLogs(w http.ResponseWriter, r *http.Request) {
	out, _ := exec.Command("journalctl", "-u", outboundUnit, "-n", "40", "--no-pager").CombinedOutput()
	txt := strings.TrimSpace(string(out))
	if txt == "" {
		txt = "No logs recorded yet for " + outboundUnit
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": txt})
}

func (h *Handler) handleOutboundInstallCore(w http.ResponseWriter, r *http.Request) {
	bin, err := installOrUpdateXray(h.store.GetSettings().PanelMirrorURL)
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

	var bodyStr, subUserInfo string
	client := &http.Client{Timeout: 14 * time.Second}
	httpReq, _ := http.NewRequest("GET", req.URL, nil)
	httpReq.Header.Set("User-Agent", "MControl/1.6.3 v2rayN/6.0")
	resp, err := client.Do(httpReq)
	if err == nil && resp.StatusCode == http.StatusOK {
		subUserInfo = resp.Header.Get("Subscription-Userinfo")
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		bodyStr = string(raw)
	} else {
		if resp != nil {
			resp.Body.Close()
		}
		if isOutboundActive() {
			hdrFile, _ := os.CreateTemp("", "mc-sub-hdr-*")
			hdrPath := ""
			if hdrFile != nil {
				hdrPath = hdrFile.Name()
				_ = hdrFile.Close()
				defer os.Remove(hdrPath)
			}
			args := []string{"-fsSL", "--max-time", "14", "--socks5-hostname", fmt.Sprintf("127.0.0.1:%d", outboundSocksPort), "-A", "MControl/1.6.3 v2rayN/6.0"}
			if hdrPath != "" {
				args = append(args, "-D", hdrPath)
			}
			args = append(args, req.URL)
			out, cerr := exec.Command("curl", args...).Output()
			if cerr == nil {
				bodyStr = string(out)
				if hdrPath != "" {
					if hb, e := os.ReadFile(hdrPath); e == nil {
						for _, ln := range strings.Split(string(hb), "\n") {
							if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ln)), "subscription-userinfo:") {
								parts := strings.SplitN(ln, ":", 2)
								if len(parts) == 2 {
									subUserInfo = strings.TrimSpace(parts[1])
								}
							}
						}
					}
				}
			}
		}
	}
	upB, downB, totB, expTs := parseSubUserinfo(subUserInfo)
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

	foundSub := false
	for i := range cfg.Subs {
		if cfg.Subs[i].ID == subID {
			cfg.Subs[i].Name = req.Name
			cfg.Subs[i].URL = req.URL
			cfg.Subs[i].LastSync = time.Now().Unix()
			cfg.Subs[i].NodeCount = len(parsed)
			cfg.Subs[i].UploadBytes = upB
			cfg.Subs[i].DownloadBytes = downB
			cfg.Subs[i].TotalBytes = totB
			cfg.Subs[i].Expire = expTs
			foundSub = true
			break
		}
	}
	if !foundSub {
		cfg.Subs = append(cfg.Subs, outboundSub{
			ID:            subID,
			Name:          req.Name,
			URL:           req.URL,
			LastSync:      time.Now().Unix(),
			NodeCount:     len(parsed),
			UploadBytes:   upB,
			DownloadBytes: downB,
			TotalBytes:    totB,
			Expire:        expTs,
		})
	}

	kept := make([]outboundNode, 0, len(cfg.Nodes)+len(parsed))
	for _, n := range cfg.Nodes {
		if n.Source != subID {
			kept = append(kept, n)
		}
	}
	kept = append(kept, parsed...)
	cfg.Nodes = kept
	if cfg.ActiveNodeID == "" && len(cfg.Nodes) > 0 {
		cfg.ActiveNodeID = cfg.Nodes[0].ID
	}
	h.saveOutboundCfg(cfg)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"sub_id":  subID,
		"added":   len(parsed),
		"config":  cfg,
		"message": fmt.Sprintf("Synced %d nodes from %s", len(parsed), req.Name),
	})
}

func buildStreamSettings(netType, sec, sni, fp, alpn, pbk, sid, spx, host, path, serviceName, headerType string, allowInsecure bool) map[string]any {
	if netType == "" || netType == "raw" {
		netType = "tcp"
	}
	if sec == "" {
		sec = "none"
	}
	stream := map[string]any{"network": netType, "security": sec}

	if sec == "tls" {
		tlsSet := map[string]any{"allowInsecure": allowInsecure}
		if sni != "" {
			tlsSet["serverName"] = sni
		}
		if fp != "" {
			tlsSet["fingerprint"] = fp
		}
		if alpn != "" {
			var list []string
			for _, a := range strings.Split(alpn, ",") {
				if t := strings.TrimSpace(a); t != "" {
					list = append(list, t)
				}
			}
			if len(list) > 0 {
				tlsSet["alpn"] = list
			}
		}
		stream["tlsSettings"] = tlsSet
	} else if sec == "reality" {
		if fp == "" {
			fp = "chrome"
		}
		if spx == "" {
			spx = "/"
		}
		stream["realitySettings"] = map[string]any{
			"serverName":  sni,
			"fingerprint": fp,
			"publicKey":   pbk,
			"shortId":     sid,
			"spiderX":     spx,
		}
	}

	switch netType {
	case "ws":
		ws := map[string]any{"path": "/"}
		if path != "" {
			ws["path"] = path
		}
		if host != "" {
			ws["headers"] = map[string]any{"Host": host}
		}
		stream["wsSettings"] = ws
	case "grpc":
		gs := map[string]any{"serviceName": serviceName}
		if serviceName == "" && path != "" {
			gs["serviceName"] = strings.TrimPrefix(path, "/")
		}
		stream["grpcSettings"] = gs
	case "xhttp", "splithttp":
		stream["network"] = "xhttp"
		xh := map[string]any{"path": "/"}
		if path != "" {
			xh["path"] = path
		}
		if host != "" {
			xh["host"] = host
		}
		stream["xhttpSettings"] = xh
	case "httpupgrade":
		hu := map[string]any{"path": "/"}
		if path != "" {
			hu["path"] = path
		}
		if host != "" {
			hu["host"] = host
		}
		stream["httpupgradeSettings"] = hu
	case "tcp":
		if strings.EqualFold(headerType, "http") {
			reqHdr := map[string]any{}
			if host != "" {
				reqHdr["Host"] = strings.Split(host, ",")
			}
			reqPath := []string{"/"}
			if path != "" {
				reqPath = strings.Split(path, ",")
			}
			stream["tcpSettings"] = map[string]any{
				"header": map[string]any{
					"type": "http",
					"request": map[string]any{
						"path":    reqPath,
						"headers": reqHdr,
					},
				},
			}
		}
	}
	return stream
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
		if u.Hostname() == "" || u.User == nil || u.User.Username() == "" {
			return nil, fmt.Errorf("incomplete vless URI (missing host or UUID)")
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
		sni := q.Get("sni")
		if sni == "" && sec == "tls" {
			sni = q.Get("host")
			if sni == "" {
				sni = u.Hostname()
			}
		}
		allowIns := q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" || strings.EqualFold(q.Get("allowInsecure"), "true")
		stream := buildStreamSettings(
			netType, sec, sni, q.Get("fp"), q.Get("alpn"),
			q.Get("pbk"), q.Get("sid"), q.Get("spx"),
			q.Get("host"), q.Get("path"), q.Get("serviceName"), q.Get("headerType"),
			allowIns,
		)
		enc := q.Get("encryption")
		if enc == "" {
			enc = "none"
		}
		user := map[string]any{"id": u.User.Username(), "encryption": enc}
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
		if u.Hostname() == "" || u.User == nil || u.User.Username() == "" {
			return nil, fmt.Errorf("incomplete trojan URI (missing host or password)")
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 443
		}
		q := u.Query()
		sec := q.Get("security")
		if sec == "" {
			sec = "tls"
		}
		sni := q.Get("sni")
		if sni == "" {
			sni = q.Get("peer")
		}
		if sni == "" {
			sni = u.Hostname()
		}
		netType := q.Get("type")
		if netType == "" {
			netType = "tcp"
		}
		allowIns := q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" || strings.EqualFold(q.Get("allowInsecure"), "true")
		stream := buildStreamSettings(
			netType, sec, sni, q.Get("fp"), q.Get("alpn"),
			q.Get("pbk"), q.Get("sid"), q.Get("spx"),
			q.Get("host"), q.Get("path"), q.Get("serviceName"), q.Get("headerType"),
			allowIns,
		)
		pass := u.User.Username()
		if dec, err := url.QueryUnescape(pass); err == nil && dec != "" {
			pass = dec
		}
		return map[string]any{
			"tag":      tag,
			"protocol": "trojan",
			"settings": map[string]any{
				"servers": []any{map[string]any{
					"address":  u.Hostname(),
					"port":     port,
					"password": pass,
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
		addr := mapStr(vm, "add")
		uid := mapStr(vm, "id")
		if addr == "" || uid == "" {
			return nil, fmt.Errorf("incomplete vmess JSON (missing add or id)")
		}
		port, _ := strconv.Atoi(mapStr(vm, "port"))
		if port == 0 {
			port = 443
		}
		netType := mapStr(vm, "net")
		if netType == "" {
			netType = "tcp"
		}
		sec := "none"
		if strings.EqualFold(mapStr(vm, "tls"), "tls") {
			sec = "tls"
		}
		sni := mapStr(vm, "sni")
		host := mapStr(vm, "host")
		if sni == "" {
			if host != "" {
				sni = host
			} else {
				sni = addr
			}
		}
		scy := mapStr(vm, "scy")
		if scy == "" {
			scy = "auto"
		}
		stream := buildStreamSettings(
			netType, sec, sni, mapStr(vm, "fp"), mapStr(vm, "alpn"),
			"", "", "", host, mapStr(vm, "path"), mapStr(vm, "path"), mapStr(vm, "type"),
			true,
		)
		return map[string]any{
			"tag":      tag,
			"protocol": "vmess",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": addr,
					"port":    port,
					"users":   []any{map[string]any{"id": uid, "security": scy}},
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
		if u.Hostname() == "" {
			return nil, fmt.Errorf("incomplete socks5 URI")
		}
		port, _ := strconv.Atoi(u.Port())
		if port == 0 {
			port = 1080
		}
		srv := map[string]any{"address": u.Hostname(), "port": port}
		if u.User != nil && u.User.Username() != "" {
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

func installOrUpdateXray(mirrorURL string) (string, error) {
	mirrorBase := strings.TrimRight(strings.TrimSpace(mirrorURL), "/")
	if mirrorBase == "" {
		mirrorBase = "https://c107328.parspack.net/c107328/MControl"
	}
	proxyFlag := ""
	if isOutboundActive() {
		proxyFlag = fmt.Sprintf("--socks5-hostname 127.0.0.1:%d", outboundSocksPort)
	}
	script := fmt.Sprintf(`set -e
TMPD=$(mktemp -d)
trap 'rm -rf "$TMPD"' EXIT
ARCH=$(uname -m)
GOARCH="amd64"
ASSET="Xray-linux-64.zip"
if [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
  GOARCH="arm64"
  ASSET="Xray-linux-arm64-v8a.zip"
fi
MIRROR="%s"
BUNDLE_CACHE="/var/lib/mcontrol/cache/bundle/mcontrol-bundle-linux-amd64.tar.gz"

# 1) Fast offline install from local cached bundle (amd64) if xray is inside it
if [ "$GOARCH" = "amd64" ] && [ -f "$BUNDLE_CACHE" ]; then
  if tar -xzf "$BUNDLE_CACHE" -C "$TMPD" bin/xray 2>/dev/null && [ -s "$TMPD/bin/xray" ]; then
    chmod 755 "$TMPD/bin/xray"
    if "$TMPD/bin/xray" version >/dev/null 2>&1; then
      install -m 755 "$TMPD/bin/xray" /usr/local/bin/xray
      exit 0
    fi
  fi
fi

# 2) Direct fast download from Iran Mirror (ParsPack)
if curl -fsSL --connect-timeout 8 --max-time 60 "${MIRROR}/xray-linux-${GOARCH}" -o "$TMPD/xray" 2>/dev/null && [ -s "$TMPD/xray" ]; then
  chmod 755 "$TMPD/xray"
  if "$TMPD/xray" version >/dev/null 2>&1; then
    install -m 755 "$TMPD/xray" /usr/local/bin/xray
    exit 0
  fi
fi

# 3) Fallback to GitHub MControl bundle-latest
GH_BIN="https://github.com/htzserv/MControl/releases/download/bundle-latest/xray-linux-${GOARCH}"
if curl -fsSL --connect-timeout 10 --max-time 60 %s "$GH_BIN" -o "$TMPD/xray" 2>/dev/null || curl -fsSL --connect-timeout 10 --max-time 60 "$GH_BIN" -o "$TMPD/xray" 2>/dev/null; then
  chmod 755 "$TMPD/xray"
  if "$TMPD/xray" version >/dev/null 2>&1; then
    install -m 755 "$TMPD/xray" /usr/local/bin/xray
    exit 0
  fi
fi

# 4) Final fallback to official XTLS/Xray-core GitHub release zip
URL="https://github.com/XTLS/Xray-core/releases/latest/download/${ASSET}"
curl -fsSL --connect-timeout 10 --max-time 60 %s "$URL" -o "$TMPD/xray.zip" || curl -fsSL --connect-timeout 10 --max-time 60 "$URL" -o "$TMPD/xray.zip"
if command -v unzip >/dev/null 2>&1; then
  unzip -qo "$TMPD/xray.zip" xray -d "$TMPD"
else
  python3 -c "import zipfile; zipfile.ZipFile('$TMPD/xray.zip').extract('xray', '$TMPD')"
fi
install -m 755 "$TMPD/xray" /usr/local/bin/xray`, mirrorBase, proxyFlag, proxyFlag)
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to install xray (mirror & github): %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return "/usr/local/bin/xray", nil
}

func ensureXrayInstalled(mirrorURL string) (string, error) {
	if p := xrayBinPath(); p != "" {
		return p, nil
	}
	return installOrUpdateXray(mirrorURL)
}

func (h *Handler) handleOutboundSave(w http.ResponseWriter, r *http.Request) {
	var req outboundConfig
	if !readJSON(w, r, &req) {
		return
	}
	if req.Mode != "single" && req.Mode != "loadbalance" {
		req.Mode = "single"
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
	req.Link = strings.TrimSpace(req.Link)
	if len(req.Nodes) == 0 && req.Link != "" {
		if n, ok := parseNodeSummary(req.Link, "manual"); ok {
			req.Nodes = append(req.Nodes, n)
			req.ActiveNodeID = n.ID
		}
	}
	h.saveOutboundCfg(req)

	if !req.Enabled {
		_ = exec.Command("systemctl", "disable", "--now", outboundUnit).Run()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "running": false, "message": "Master Outbound disabled & saved."})
		return
	}

	var outbounds []any
	var activeSingleName string
	var firstErr error

	if req.Mode == "single" {
		var chosen *outboundNode
		for i := range req.Nodes {
			if req.Nodes[i].ID == req.ActiveNodeID && strings.TrimSpace(req.Nodes[i].URI) != "" {
				chosen = &req.Nodes[i]
				break
			}
		}
		if chosen == nil {
			for i := range req.Nodes {
				if req.Nodes[i].Enabled && strings.TrimSpace(req.Nodes[i].URI) != "" {
					chosen = &req.Nodes[i]
					req.ActiveNodeID = chosen.ID
					h.saveOutboundCfg(req)
					break
				}
			}
		}
		if chosen == nil {
			fail(w, http.StatusBadRequest, "select or add an active single config first")
			return
		}
		obj, err := parseLinkToXrayOutbound(chosen.URI, "proxy-0")
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid active config: "+err.Error())
			return
		}
		outbounds = append(outbounds, obj)
		activeSingleName = chosen.Name
	} else {
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
	}

	bin, err := ensureXrayInstalled(h.store.GetSettings().PanelMirrorURL)
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

	if req.Mode == "loadbalance" && len(outbounds) > 1 {
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
					"destination": req.ProbeURL,
					"interval":    req.ProbeInterval,
					"sampling":    3,
					"timeout":     "5s",
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
	} else {
		fullCfg["routing"] = map[string]any{
			"rules": []any{
				map[string]any{
					"type":        "field",
					"inboundTag":  []string{"mc-local-socks"},
					"outboundTag": "proxy-0",
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

	if testOut, testErr := exec.Command(bin, "run", "-test", "-c", outboundCfgPath).CombinedOutput(); testErr != nil {
		fail(w, http.StatusBadRequest, "Xray config validation failed: "+strings.TrimSpace(string(testOut)))
		return
	}

	unitTxt := fmt.Sprintf(`[Unit]
Description=MControl Master Isolated Outbound (Xray SOCKS5 127.0.0.1:%d)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

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

	active := false
	for i := 0; i < 5; i++ {
		time.Sleep(500 * time.Millisecond)
		if isOutboundActive() {
			active = true
			break
		}
	}
	if !active {
		jlog, _ := exec.Command("journalctl", "-u", outboundUnit, "-n", "12", "--no-pager").CombinedOutput()
		fail(w, http.StatusInternalServerError, "Xray started but SOCKS5 127.0.0.1:10808 did not come up:\n"+strings.TrimSpace(string(jlog)))
		return
	}

	modeDesc := "Single Config"
	if activeSingleName != "" {
		modeDesc = "Single Config (" + activeSingleName + ")"
	}
	if req.Mode == "loadbalance" && len(outbounds) > 1 {
		modeDesc = fmt.Sprintf("Load Balance • %d Nodes (%s)", len(outbounds), req.LBStrategy)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"running":      true,
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
		fail(w, http.StatusBadRequest, "Master Outbound service is not running on 127.0.0.1:10808. Save & Start it first.")
		return
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf(`
t_ip=$(curl -sS --max-time 8 --socks5-hostname 127.0.0.1:%d https://api.ipify.org 2>&1 || echo "FAILED")
t_gh=$(curl -sS --max-time 8 --socks5-hostname 127.0.0.1:%d -o /dev/null -w "HTTP %%{http_code} | %%{time_total}s" https://api.github.com/zen 2>&1 || echo "FAILED")
t_tg=$(curl -sS --max-time 8 --socks5-hostname 127.0.0.1:%d -o /dev/null -w "HTTP %%{http_code} | %%{time_total}s" https://api.telegram.org 2>&1 || echo "FAILED")
echo "Exit IP     : $t_ip"
echo "GitHub API  : $t_gh"
echo "Telegram API: $t_tg"
`, outboundSocksPort, outboundSocksPort, outboundSocksPort))
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
		NodeID string `json:"node_id"` // optional: test single node
		SubID  string `json:"sub_id"`  // optional: test all nodes in a subscription (or "manual")
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
		if req.SubID != "" && n.Source != req.SubID {
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
		"log":       map[string]any{"loglevel": "error"},
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

	var stderrBuf bytes.Buffer
	cmd := exec.Command(bin, "run", "-c", tmpPath)
	cmd.Stderr = &stderrBuf
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

	ready := false
	for i := 0; i < 8; i++ {
		time.Sleep(250 * time.Millisecond)
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", items[0].port), 200*time.Millisecond); err == nil {
			_ = c.Close()
			ready = true
			break
		}
	}
	if !ready {
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = "Xray tester exited before opening SOCKS ports (check node URI parameters)"
		}
		fail(w, http.StatusBadRequest, "Xray delay tester failed: "+msg)
		return
	}

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
