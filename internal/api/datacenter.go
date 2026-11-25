package api

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
)

type dcProviderCfg struct {
	Enabled     bool   `json:"enabled"`
	Token       string `json:"token,omitempty"`        // Hetzner / DigitalOcean / Linode / YottaSrc API Key
	AppKey      string `json:"app_key,omitempty"`      // OVH App Key
	AppSecret   string `json:"app_secret,omitempty"`   // OVH App Secret
	ConsumerKey string `json:"consumer_key,omitempty"` // OVH Consumer Key
	Endpoint    string `json:"endpoint,omitempty"`     // OVH region (ovh-eu|ovh-ca|ovh-us) or YottaSrc Base URL
	ExtraHeader string `json:"extra_header,omitempty"` // YottaSrc optional custom auth header name (default Authorization)
}

type dcConfig struct {
	UseOutbound  bool          `json:"use_outbound"`
	Hetzner      dcProviderCfg `json:"hetzner"`
	DigitalOcean dcProviderCfg `json:"digitalocean"`
	Linode       dcProviderCfg `json:"linode"`
	OVH          dcProviderCfg `json:"ovh"`
	YottaSrc     dcProviderCfg `json:"yottasrc"`
}

type dcServer struct {
	Provider   string `json:"provider"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	IPv4       string `json:"ipv4"`
	IPv6       string `json:"ipv6"`
	Region     string `json:"region"`
	Plan       string `json:"plan"`
	Image      string `json:"image"`
	CanRebuild bool   `json:"can_rebuild"`
	CanFW      bool   `json:"can_fw"`
	CanDelete  bool   `json:"can_delete"`
}

type dcOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type dcOptionsResp struct {
	Regions []dcOption `json:"regions"`
	Plans   []dcOption `json:"plans"`
	Images  []dcOption `json:"images"`
}

var peerV4FirewallPorts = []int{84, 847, 7483, 24819, 2054}

func (h *Handler) loadDCConfig() dcConfig {
	var cfg dcConfig
	enc := h.store.GetSetting("datacenter_providers_enc")
	if enc != "" {
		if raw, err := h.store.Decrypt(enc); err == nil && raw != "" {
			_ = json.Unmarshal([]byte(raw), &cfg)
		}
	}
	if cfg.OVH.Endpoint == "" {
		cfg.OVH.Endpoint = "ovh-eu"
	}
	return cfg
}

func (h *Handler) saveDCConfig(cfg dcConfig) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	enc, err := h.store.Encrypt(string(b))
	if err != nil {
		return err
	}
	return h.store.SetSetting("datacenter_providers_enc", enc)
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

func keepOrUpdateSecret(incoming, existing string) string {
	t := strings.TrimSpace(incoming)
	if t == "" || strings.Contains(t, "••••") {
		return existing
	}
	return t
}

func (h *Handler) dcHTTPClient(useOutbound bool, timeout time.Duration) *http.Client {
	tr := &http.Transport{}
	if useOutbound && isOutboundActive() {
		px := fmt.Sprintf("127.0.0.1:%d", outboundSocksPort)
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return sshclient.DialViaSOCKS5(px, addr, timeout)
		}
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

func dcDoJSON(client *http.Client, method, u string, headers map[string]string, payload any) ([]byte, int, error) {
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 260 {
			msg = msg[:260]
		}
		return raw, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return raw, resp.StatusCode, nil
}

// ── OVHcloud Signed Request Helper ──
func ovhBaseURL(region string) string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "ovh-ca":
		return "https://ca.api.ovh.com/1.0"
	case "ovh-us":
		return "https://api.us.ovhcloud.com/1.0"
	default:
		return "https://eu.api.ovh.com/1.0"
	}
}

func ovhDoJSON(client *http.Client, p dcProviderCfg, method, path string, payload any) ([]byte, error) {
	base := ovhBaseURL(p.Endpoint)
	fullURL := base + path
	bodyStr := ""
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyStr = string(b)
	}
	// Fetch OVH server time to avoid clock skew
	ts := time.Now().Unix()
	if tr, err := client.Get(base + "/auth/time"); err == nil {
		tb, _ := io.ReadAll(io.LimitReader(tr.Body, 64))
		tr.Body.Close()
		if parsed, e := strconv.ParseInt(strings.TrimSpace(string(tb)), 10, 64); e == nil && parsed > 0 {
			ts = parsed
		}
	}
	tsStr := strconv.FormatInt(ts, 10)
	toSign := fmt.Sprintf("%s+%s+%s+%s+%s+%s", p.AppSecret, p.ConsumerKey, method, fullURL, bodyStr, tsStr)
	sum := sha1.Sum([]byte(toSign))
	sig := "$1$" + hex.EncodeToString(sum[:])

	var rdr io.Reader
	if bodyStr != "" {
		rdr = strings.NewReader(bodyStr)
	}
	req, err := http.NewRequest(method, fullURL, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Ovh-Application", p.AppKey)
	req.Header.Set("X-Ovh-Consumer", p.ConsumerKey)
	req.Header.Set("X-Ovh-Timestamp", tsStr)
	req.Header.Set("X-Ovh-Signature", sig)
	req.Header.Set("Accept", "application/json")
	if bodyStr != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 260 {
			msg = msg[:260]
		}
		return nil, fmt.Errorf("OVH HTTP %d: %s", resp.StatusCode, msg)
	}
	return raw, nil
}

// ── Provider: Hetzner Cloud ──
func hetznerListServers(c *http.Client, tok string) ([]dcServer, error) {
	raw, _, err := dcDoJSON(c, "GET", "https://api.hetzner.cloud/v1/servers?per_page=50", map[string]string{"Authorization": "Bearer " + tok}, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Servers []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			PublicNet  struct {
				IPv4 struct{ IP string `json:"ip"` } `json:"ipv4"`
				IPv6 struct{ IP string `json:"ip"` } `json:"ipv6"`
			} `json:"public_net"`
			Datacenter struct {
				Name     string `json:"name"`
				Location struct{ Name string `json:"name"` } `json:"location"`
			} `json:"datacenter"`
			ServerType struct{ Name string `json:"name"` } `json:"server_type"`
			Image      *struct{ Name string `json:"name"` } `json:"image"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]dcServer, 0, len(resp.Servers))
	for _, s := range resp.Servers {
		img := ""
		if s.Image != nil {
			img = s.Image.Name
		}
		reg := s.Datacenter.Location.Name
		if reg == "" {
			reg = s.Datacenter.Name
		}
		out = append(out, dcServer{
			Provider: "hetzner", ID: strconv.FormatInt(s.ID, 10), Name: s.Name, Status: s.Status,
			IPv4: s.PublicNet.IPv4.IP, IPv6: s.PublicNet.IPv6.IP, Region: reg, Plan: s.ServerType.Name, Image: img,
			CanRebuild: true, CanFW: true, CanDelete: true,
		})
	}
	return out, nil
}

// ── Provider: DigitalOcean ──
func doListServers(c *http.Client, tok string) ([]dcServer, error) {
	raw, _, err := dcDoJSON(c, "GET", "https://api.digitalocean.com/v2/droplets?per_page=50", map[string]string{"Authorization": "Bearer " + tok}, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Droplets []struct {
			ID       int64  `json:"id"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			SizeSlug string `json:"size_slug"`
			Region   struct{ Slug string `json:"slug"` } `json:"region"`
			Image    struct{ Slug, Name string } `json:"image"`
			Networks struct {
				V4 []struct{ IPAddress, Type string } `json:"v4"`
				V6 []struct{ IPAddress, Type string } `json:"v6"`
			} `json:"networks"`
		} `json:"droplets"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]dcServer, 0, len(resp.Droplets))
	for _, d := range resp.Droplets {
		ip4, ip6 := "", ""
		for _, v := range d.Networks.V4 {
			if v.Type == "public" {
				ip4 = v.IPAddress
				break
			}
		}
		for _, v := range d.Networks.V6 {
			if v.Type == "public" {
				ip6 = v.IPAddress
				break
			}
		}
		img := d.Image.Slug
		if img == "" {
			img = d.Image.Name
		}
		out = append(out, dcServer{
			Provider: "digitalocean", ID: strconv.FormatInt(d.ID, 10), Name: d.Name, Status: d.Status,
			IPv4: ip4, IPv6: ip6, Region: d.Region.Slug, Plan: d.SizeSlug, Image: img,
			CanRebuild: true, CanFW: true, CanDelete: true,
		})
	}
	return out, nil
}

// ── Provider: Linode (Akamai) ──
func linodeListServers(c *http.Client, tok string) ([]dcServer, error) {
	raw, _, err := dcDoJSON(c, "GET", "https://api.linode.com/v4/linode/instances?page_size=100", map[string]string{"Authorization": "Bearer " + tok}, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data []struct {
			ID     int64    `json:"id"`
			Label  string   `json:"label"`
			Status string   `json:"status"`
			IPv4   []string `json:"ipv4"`
			IPv6   string   `json:"ipv6"`
			Region string   `json:"region"`
			Type   string   `json:"type"`
			Image  string   `json:"image"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	out := make([]dcServer, 0, len(resp.Data))
	for _, l := range resp.Data {
		ip4 := ""
		if len(l.IPv4) > 0 {
			ip4 = l.IPv4[0]
		}
		out = append(out, dcServer{
			Provider: "linode", ID: strconv.FormatInt(l.ID, 10), Name: l.Label, Status: l.Status,
			IPv4: ip4, IPv6: l.IPv6, Region: l.Region, Plan: l.Type, Image: l.Image,
			CanRebuild: true, CanFW: true, CanDelete: true,
		})
	}
	return out, nil
}

// ── Provider: OVHcloud (VPS) ──
func ovhListServers(c *http.Client, p dcProviderCfg) ([]dcServer, error) {
	raw, err := ovhDoJSON(c, p, "GET", "/vps", nil)
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, err
	}
	out := make([]dcServer, 0, len(names))
	for _, svc := range names {
		item := dcServer{
			Provider: "ovh", ID: svc, Name: svc, Status: "running",
			CanRebuild: true, CanFW: false, CanDelete: false,
		}
		if dRaw, e := ovhDoJSON(c, p, "GET", "/vps/"+svc, nil); e == nil {
			var det struct {
				DisplayName string `json:"displayName"`
				State       string `json:"state"`
				Zone        string `json:"zone"`
				Model       struct{ Name string `json:"name"` } `json:"model"`
			}
			if json.Unmarshal(dRaw, &det) == nil {
				if det.DisplayName != "" {
					item.Name = det.DisplayName
				}
				if det.State != "" {
					item.Status = det.State
				}
				item.Region = det.Zone
				item.Plan = det.Model.Name
			}
		}
		if ipRaw, e := ovhDoJSON(c, p, "GET", "/vps/"+svc+"/ips", nil); e == nil {
			var ips []string
			if json.Unmarshal(ipRaw, &ips) == nil {
				for _, ip := range ips {
					if strings.Contains(ip, ":") && item.IPv6 == "" {
						item.IPv6 = ip
					} else if !strings.Contains(ip, ":") && item.IPv4 == "" {
						item.IPv4 = ip
					}
				}
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// ── Provider: YottaSrc (Flexible REST Connector) ──
func yottaHeaders(p dcProviderCfg) map[string]string {
	hdr := strings.TrimSpace(p.ExtraHeader)
	if hdr == "" {
		return map[string]string{"Authorization": "Bearer " + p.Token, "X-API-Key": p.Token}
	}
	return map[string]string{hdr: p.Token}
}

func yottaListServers(c *http.Client, p dcProviderCfg) ([]dcServer, error) {
	base := strings.TrimRight(strings.TrimSpace(p.Endpoint), "/")
	if base == "" {
		return nil, fmt.Errorf("YottaSrc API Base URL is empty")
	}
	raw, _, err := dcDoJSON(c, "GET", base+"/servers", yottaHeaders(p), nil)
	if err != nil {
		return nil, err
	}
	// Accept either {"servers":[...]}, {"data":[...]}, or top-level [...]
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		var wrap map[string]json.RawMessage
		if err2 := json.Unmarshal(raw, &wrap); err2 == nil {
			for _, k := range []string{"servers", "data", "instances", "vps"} {
				if sub, ok := wrap[k]; ok {
					_ = json.Unmarshal(sub, &arr)
					break
				}
			}
		}
	}
	out := make([]dcServer, 0, len(arr))
	for _, m := range arr {
		id := mapStr(m, "id")
		if id == "" {
			id = mapStr(m, "vpsid")
		}
		name := mapStr(m, "name")
		if name == "" {
			name = mapStr(m, "hostname")
		}
		ip4 := mapStr(m, "ipv4")
		if ip4 == "" {
			ip4 = mapStr(m, "ip")
		}
		st := mapStr(m, "status")
		if st == "" {
			st = "active"
		}
		out = append(out, dcServer{
			Provider: "yottasrc", ID: id, Name: name, Status: st,
			IPv4: ip4, IPv6: mapStr(m, "ipv6"), Region: mapStr(m, "region"),
			Plan: mapStr(m, "plan"), Image: mapStr(m, "os"),
			CanRebuild: true, CanFW: false, CanDelete: true,
		})
	}
	return out, nil
}

// ══════════════════════ HTTP HANDLERS ══════════════════════

func (h *Handler) handleDCStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadDCConfig()
	masked := map[string]any{
		"use_outbound": cfg.UseOutbound,
		"hetzner": map[string]any{
			"enabled": cfg.Hetzner.Enabled, "has_token": cfg.Hetzner.Token != "", "token_masked": maskSecret(cfg.Hetzner.Token),
		},
		"digitalocean": map[string]any{
			"enabled": cfg.DigitalOcean.Enabled, "has_token": cfg.DigitalOcean.Token != "", "token_masked": maskSecret(cfg.DigitalOcean.Token),
		},
		"linode": map[string]any{
			"enabled": cfg.Linode.Enabled, "has_token": cfg.Linode.Token != "", "token_masked": maskSecret(cfg.Linode.Token),
		},
		"ovh": map[string]any{
			"enabled": cfg.OVH.Enabled, "endpoint": cfg.OVH.Endpoint,
			"has_token": cfg.OVH.AppKey != "" && cfg.OVH.AppSecret != "" && cfg.OVH.ConsumerKey != "",
			"app_key": cfg.OVH.AppKey, "app_secret_masked": maskSecret(cfg.OVH.AppSecret), "consumer_key_masked": maskSecret(cfg.OVH.ConsumerKey),
		},
		"yottasrc": map[string]any{
			"enabled": cfg.YottaSrc.Enabled, "endpoint": cfg.YottaSrc.Endpoint, "extra_header": cfg.YottaSrc.ExtraHeader,
			"has_token": cfg.YottaSrc.Token != "" && cfg.YottaSrc.Endpoint != "", "token_masked": maskSecret(cfg.YottaSrc.Token),
		},
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": masked})
}

func (h *Handler) handleDCSaveConfig(w http.ResponseWriter, r *http.Request) {
	var req dcConfig
	if !readJSON(w, r, &req) {
		return
	}
	cur := h.loadDCConfig()
	cur.UseOutbound = req.UseOutbound

	cur.Hetzner.Enabled = req.Hetzner.Enabled
	cur.Hetzner.Token = keepOrUpdateSecret(req.Hetzner.Token, cur.Hetzner.Token)

	cur.DigitalOcean.Enabled = req.DigitalOcean.Enabled
	cur.DigitalOcean.Token = keepOrUpdateSecret(req.DigitalOcean.Token, cur.DigitalOcean.Token)

	cur.Linode.Enabled = req.Linode.Enabled
	cur.Linode.Token = keepOrUpdateSecret(req.Linode.Token, cur.Linode.Token)

	cur.OVH.Enabled = req.OVH.Enabled
	if strings.TrimSpace(req.OVH.Endpoint) != "" {
		cur.OVH.Endpoint = strings.TrimSpace(req.OVH.Endpoint)
	}
	cur.OVH.AppKey = keepOrUpdateSecret(req.OVH.AppKey, cur.OVH.AppKey)
	cur.OVH.AppSecret = keepOrUpdateSecret(req.OVH.AppSecret, cur.OVH.AppSecret)
	cur.OVH.ConsumerKey = keepOrUpdateSecret(req.OVH.ConsumerKey, cur.OVH.ConsumerKey)

	cur.YottaSrc.Enabled = req.YottaSrc.Enabled
	cur.YottaSrc.Endpoint = strings.TrimRight(strings.TrimSpace(req.YottaSrc.Endpoint), "/")
	cur.YottaSrc.ExtraHeader = strings.TrimSpace(req.YottaSrc.ExtraHeader)
	cur.YottaSrc.Token = keepOrUpdateSecret(req.YottaSrc.Token, cur.YottaSrc.Token)

	if err := h.saveDCConfig(cur); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.logAudit(r.Header.Get("X-MC-User"), "POST", r.URL.Path, "Updated Datacenter API provider settings")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Datacenter API credentials encrypted & saved!"})
}

func (h *Handler) handleDCListServers(w http.ResponseWriter, r *http.Request) {
	cfg := h.loadDCConfig()
	c := h.dcHTTPClient(cfg.UseOutbound, 16*time.Second)

	var mu sync.Mutex
	var wg sync.WaitGroup
	var all []dcServer
	errs := map[string]string{}

	runProv := func(name string, fn func() ([]dcServer, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := fn()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[name] = err.Error()
			} else {
				all = append(all, list...)
			}
		}()
	}

	if cfg.Hetzner.Enabled && cfg.Hetzner.Token != "" {
		runProv("hetzner", func() ([]dcServer, error) { return hetznerListServers(c, cfg.Hetzner.Token) })
	}
	if cfg.DigitalOcean.Enabled && cfg.DigitalOcean.Token != "" {
		runProv("digitalocean", func() ([]dcServer, error) { return doListServers(c, cfg.DigitalOcean.Token) })
	}
	if cfg.Linode.Enabled && cfg.Linode.Token != "" {
		runProv("linode", func() ([]dcServer, error) { return linodeListServers(c, cfg.Linode.Token) })
	}
	if cfg.OVH.Enabled && cfg.OVH.AppKey != "" && cfg.OVH.AppSecret != "" && cfg.OVH.ConsumerKey != "" {
		runProv("ovh", func() ([]dcServer, error) { return ovhListServers(c, cfg.OVH) })
	}
	if cfg.YottaSrc.Enabled && cfg.YottaSrc.Endpoint != "" && cfg.YottaSrc.Token != "" {
		runProv("yottasrc", func() ([]dcServer, error) { return yottaListServers(c, cfg.YottaSrc) })
	}
	wg.Wait()
	if all == nil {
		all = []dcServer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": all, "errors": errs})
}

func (h *Handler) handleDCOptions(w http.ResponseWriter, r *http.Request) {
	prov := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	srvID := strings.TrimSpace(r.URL.Query().Get("server_id"))
	cfg := h.loadDCConfig()
	c := h.dcHTTPClient(cfg.UseOutbound, 15*time.Second)

	res := dcOptionsResp{
		Regions: []dcOption{},
		Plans:   []dcOption{},
		Images:  []dcOption{},
	}

	switch prov {
	case "hetzner":
		if cfg.Hetzner.Token == "" {
			fail(w, http.StatusBadRequest, "Hetzner API token not configured")
			return
		}
		hdr := map[string]string{"Authorization": "Bearer " + cfg.Hetzner.Token}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.hetzner.cloud/v1/locations", hdr, nil); err == nil {
			var o struct {
				Locations []struct{ Name, Description, Country string } `json:"locations"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, l := range o.Locations {
					res.Regions = append(res.Regions, dcOption{ID: l.Name, Label: fmt.Sprintf("%s — %s (%s)", l.Name, l.Description, l.Country)})
				}
			}
		}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.hetzner.cloud/v1/server_types?per_page=50", hdr, nil); err == nil {
			var o struct {
				ServerTypes []struct {
					Name   string  `json:"name"`
					Cores  int     `json:"cores"`
					Memory float64 `json:"memory"`
					Disk   int     `json:"disk"`
				} `json:"server_types"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, t := range o.ServerTypes {
					res.Plans = append(res.Plans, dcOption{ID: t.Name, Label: fmt.Sprintf("%s (%d vCPU · %.0fGB RAM · %dGB)", t.Name, t.Cores, t.Memory, t.Disk)})
				}
			}
		}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.hetzner.cloud/v1/images?type=system&per_page=50", hdr, nil); err == nil {
			var o struct {
				Images []struct{ Name, Description, Architecture string } `json:"images"`
			}
			if json.Unmarshal(raw, &o) == nil {
				seen := map[string]bool{}
				for _, im := range o.Images {
					if im.Architecture != "x86" || seen[im.Name] {
						continue
					}
					seen[im.Name] = true
					res.Images = append(res.Images, dcOption{ID: im.Name, Label: im.Description + " (" + im.Name + ")"})
				}
			}
		}

	case "digitalocean":
		if cfg.DigitalOcean.Token == "" {
			fail(w, http.StatusBadRequest, "DigitalOcean API token not configured")
			return
		}
		hdr := map[string]string{"Authorization": "Bearer " + cfg.DigitalOcean.Token}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.digitalocean.com/v2/regions?per_page=50", hdr, nil); err == nil {
			var o struct {
				Regions []struct {
					Slug      string `json:"slug"`
					Name      string `json:"name"`
					Available bool   `json:"available"`
				} `json:"regions"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, rg := range o.Regions {
					if rg.Available {
						res.Regions = append(res.Regions, dcOption{ID: rg.Slug, Label: rg.Name + " (" + rg.Slug + ")"})
					}
				}
			}
		}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.digitalocean.com/v2/sizes?per_page=40", hdr, nil); err == nil {
			var o struct {
				Sizes []struct {
					Slug         string  `json:"slug"`
					Memory       int     `json:"memory"`
					Vcpus        int     `json:"vcpus"`
					Disk         int     `json:"disk"`
					PriceMonthly float64 `json:"price_monthly"`
					Available    bool    `json:"available"`
				} `json:"sizes"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, sz := range o.Sizes {
					if sz.Available {
						res.Plans = append(res.Plans, dcOption{ID: sz.Slug, Label: fmt.Sprintf("%s (%d vCPU · %dMB · %dGB · $%.0f/mo)", sz.Slug, sz.Vcpus, sz.Memory, sz.Disk, sz.PriceMonthly)})
					}
				}
			}
		}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.digitalocean.com/v2/images?type=distribution&per_page=50", hdr, nil); err == nil {
			var o struct {
				Images []struct{ Slug, Distribution, Name string } `json:"images"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, im := range o.Images {
					if im.Slug != "" {
						res.Images = append(res.Images, dcOption{ID: im.Slug, Label: im.Distribution + " " + im.Name})
					}
				}
			}
		}

	case "linode":
		if cfg.Linode.Token == "" {
			fail(w, http.StatusBadRequest, "Linode API token not configured")
			return
		}
		hdr := map[string]string{"Authorization": "Bearer " + cfg.Linode.Token}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.linode.com/v4/regions", hdr, nil); err == nil {
			var o struct {
				Data []struct{ ID, Label, Country string } `json:"data"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, rg := range o.Data {
					res.Regions = append(res.Regions, dcOption{ID: rg.ID, Label: fmt.Sprintf("%s (%s)", rg.Label, rg.ID)})
				}
			}
		}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.linode.com/v4/linode/types", hdr, nil); err == nil {
			var o struct {
				Data []struct {
					ID     string `json:"id"`
					Label  string `json:"label"`
					Memory int    `json:"memory"`
					Vcpus  int    `json:"vcpus"`
				} `json:"data"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, t := range o.Data {
					res.Plans = append(res.Plans, dcOption{ID: t.ID, Label: fmt.Sprintf("%s (%d vCPU · %dMB)", t.Label, t.Vcpus, t.Memory)})
				}
			}
		}
		if raw, _, err := dcDoJSON(c, "GET", "https://api.linode.com/v4/images", hdr, nil); err == nil {
			var o struct {
				Data []struct {
					ID       string `json:"id"`
					Label    string `json:"label"`
					IsPublic bool   `json:"is_public"`
				} `json:"data"`
			}
			if json.Unmarshal(raw, &o) == nil {
				for _, im := range o.Data {
					if im.IsPublic && strings.HasPrefix(im.ID, "linode/") {
						res.Images = append(res.Images, dcOption{ID: im.ID, Label: im.Label})
					}
				}
			}
		}

	case "ovh":
		if srvID != "" && cfg.OVH.AppKey != "" {
			if raw, err := ovhDoJSON(c, cfg.OVH, "GET", "/vps/"+srvID+"/images/available", nil); err == nil {
				var ids []string
				if json.Unmarshal(raw, &ids) == nil {
					for _, imID := range ids {
						lbl := imID
						if dRaw, e := ovhDoJSON(c, cfg.OVH, "GET", "/vps/"+srvID+"/images/available/"+imID, nil); e == nil {
							var d struct{ Name string `json:"name"` }
							if json.Unmarshal(dRaw, &d) == nil && d.Name != "" {
								lbl = d.Name
							}
						}
						res.Images = append(res.Images, dcOption{ID: imID, Label: lbl})
					}
				}
			}
		}

	case "yottasrc":
		res.Images = []dcOption{
			{ID: "ubuntu-24.04", Label: "Ubuntu 24.04 LTS"},
			{ID: "ubuntu-22.04", Label: "Ubuntu 22.04 LTS"},
			{ID: "debian-12", Label: "Debian 12"},
		}
	}

	// Sensible fallback OS images if list is empty
	if len(res.Images) == 0 {
		res.Images = []dcOption{
			{ID: "ubuntu-24.04", Label: "Ubuntu 24.04 LTS"},
			{ID: "ubuntu-22.04", Label: "Ubuntu 22.04 LTS"},
			{ID: "debian-12", Label: "Debian 12"},
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "options": res})
}

func (h *Handler) handleDCCreateServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider     string `json:"provider"`
		Name         string `json:"name"`
		Region       string `json:"region"`
		Plan         string `json:"plan"`
		Image        string `json:"image"`
		RootPassword string `json:"root_password"`
		AutoImport   bool   `json:"auto_import"`
		AutoPair     bool   `json:"auto_pair"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(w, http.StatusBadRequest, "server name is required")
		return
	}
	cfg := h.loadDCConfig()
	c := h.dcHTTPClient(cfg.UseOutbound, 25*time.Second)

	var createdIP, rootPass, createdID string

	switch req.Provider {
	case "hetzner":
		if cfg.Hetzner.Token == "" {
			fail(w, http.StatusBadRequest, "Hetzner API token not configured")
			return
		}
		body := map[string]any{
			"name":               req.Name,
			"server_type":        req.Plan,
			"image":              req.Image,
			"location":           req.Region,
			"start_after_create": true,
		}
		raw, _, err := dcDoJSON(c, "POST", "https://api.hetzner.cloud/v1/servers", map[string]string{"Authorization": "Bearer " + cfg.Hetzner.Token}, body)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		var resp struct {
			RootPassword string `json:"root_password"`
			Server       struct {
				ID        int64 `json:"id"`
				PublicNet struct{ IPv4 struct{ IP string `json:"ip"` } `json:"ipv4"` } `json:"public_net"`
			} `json:"server"`
		}
		_ = json.Unmarshal(raw, &resp)
		createdID = strconv.FormatInt(resp.Server.ID, 10)
		createdIP = resp.Server.PublicNet.IPv4.IP
		rootPass = resp.RootPassword

	case "digitalocean":
		if cfg.DigitalOcean.Token == "" {
			fail(w, http.StatusBadRequest, "DigitalOcean API token not configured")
			return
		}
		body := map[string]any{
			"name":   req.Name,
			"region": req.Region,
			"size":   req.Plan,
			"image":  req.Image,
			"ipv6":   true,
		}
		raw, _, err := dcDoJSON(c, "POST", "https://api.digitalocean.com/v2/droplets", map[string]string{"Authorization": "Bearer " + cfg.DigitalOcean.Token}, body)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		var resp struct {
			Droplet struct{ ID int64 `json:"id"` } `json:"droplet"`
		}
		_ = json.Unmarshal(raw, &resp)
		createdID = strconv.FormatInt(resp.Droplet.ID, 10)

	case "linode":
		if cfg.Linode.Token == "" {
			fail(w, http.StatusBadRequest, "Linode API token not configured")
			return
		}
		if len(req.RootPassword) < 8 {
			req.RootPassword = "Mc!" + shortRandID() + "9A#"
		}
		body := map[string]any{
			"label":     req.Name,
			"region":    req.Region,
			"type":      req.Plan,
			"image":     req.Image,
			"root_pass": req.RootPassword,
			"booted":    true,
		}
		raw, _, err := dcDoJSON(c, "POST", "https://api.linode.com/v4/linode/instances", map[string]string{"Authorization": "Bearer " + cfg.Linode.Token}, body)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		var resp struct {
			ID   int64    `json:"id"`
			IPv4 []string `json:"ipv4"`
		}
		_ = json.Unmarshal(raw, &resp)
		createdID = strconv.FormatInt(resp.ID, 10)
		if len(resp.IPv4) > 0 {
			createdIP = resp.IPv4[0]
		}
		rootPass = req.RootPassword

	case "yottasrc":
		if cfg.YottaSrc.Endpoint == "" || cfg.YottaSrc.Token == "" {
			fail(w, http.StatusBadRequest, "YottaSrc API endpoint & key not configured")
			return
		}
		body := map[string]any{
			"name": req.Name, "region": req.Region, "plan": req.Plan, "os": req.Image, "password": req.RootPassword,
		}
		_, _, err := dcDoJSON(c, "POST", cfg.YottaSrc.Endpoint+"/servers", yottaHeaders(cfg.YottaSrc), body)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		rootPass = req.RootPassword

	default:
		fail(w, http.StatusBadRequest, "create server is supported for hetzner, digitalocean, linode, and yottasrc (OVH VPS orders are managed in OVH dashboard)")
		return
	}

	importedID := int64(0)
	if (req.AutoImport || req.AutoPair) && createdIP != "" {
		srv := &db.Server{
			Name: req.Name, Role: "foreign", Host: createdIP, Port: 22, Username: "root",
			Password: rootPass, AuthType: "password", Datacenter: strings.ToUpper(req.Provider),
		}
		if id, err := h.store.AddServer(srv); err == nil {
			importedID = id
			if req.AutoPair && rootPass != "" {
				go func(sid int64, ip, pass, prov, cid string) {
					// Wait for cloud VM to finish booting SSH
					time.Sleep(25 * time.Second)
					fullSrv, e := h.store.GetServerWithSecret(sid)
					if e != nil {
						return
					}
					peerCode := "MC-" + shortRandID() + shortRandID()
					stCfg := h.store.GetSettings()
					target := h.targetOfSrv(fullSrv)
					masterIP := h.masterPublicIP()
					masterURL := h.masterBaseURL()
					for _, pt := range []int{24819, 7483, 847, 84, 2054} {
						res := sshclient.InstallPeerAgentV4(target, masterIP, masterURL, peerCode, pt, stCfg.PanelMirrorURL, false, true)
						if res.Err == nil {
							if pr := sshclient.ProbePeer(ip, pt, peerCode, h.serverSocksProxy(fullSrv)); pr.Online {
								_ = h.store.SetServerPeerMode(sid, peerCode, pt)
								if pr.AgentCertFP != "" {
									h.saveAgentCertPin(ip, pt, pr.AgentCertFP)
								}
								h.applyProbe(fullSrv, pr)
								break
							}
						}
					}
				}(id, createdIP, rootPass, req.Provider, createdID)
			}
		}
	}

	h.logAudit(r.Header.Get("X-MC-User"), "POST", r.URL.Path, fmt.Sprintf("Created %s server %s (%s)", req.Provider, req.Name, createdIP))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"id":            createdID,
		"ipv4":          createdIP,
		"root_password": rootPass,
		"imported_id":   importedID,
		"message":       fmt.Sprintf("Server %s created on %s!", req.Name, req.Provider),
	})
}

func (h *Handler) handleDCAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider     string `json:"provider"`
		ServerID     string `json:"server_id"`
		Action       string `json:"action"` // rebuild | reboot | poweron | poweroff | delete | open_peer_fw
		Image        string `json:"image"`
		RootPassword string `json:"root_password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.ServerID = strings.TrimSpace(req.ServerID)
	if req.ServerID == "" {
		fail(w, http.StatusBadRequest, "server_id is required")
		return
	}

	cfg := h.loadDCConfig()
	c := h.dcHTTPClient(cfg.UseOutbound, 25*time.Second)
	newPass := ""
	msg := ""

	switch req.Provider {
	case "hetzner":
		if cfg.Hetzner.Token == "" {
			fail(w, http.StatusBadRequest, "Hetzner API token not configured")
			return
		}
		hdr := map[string]string{"Authorization": "Bearer " + cfg.Hetzner.Token}
		base := "https://api.hetzner.cloud/v1/servers/" + req.ServerID
		switch req.Action {
		case "rebuild":
			if req.Image == "" {
				req.Image = "ubuntu-24.04"
			}
			raw, _, err := dcDoJSON(c, "POST", base+"/actions/rebuild", hdr, map[string]any{"image": req.Image})
			if err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			var o struct{ RootPassword string `json:"root_password"` }
			_ = json.Unmarshal(raw, &o)
			newPass = o.RootPassword
			msg = "Hetzner OS rebuild started (" + req.Image + ")!"
		case "reboot":
			if _, _, err := dcDoJSON(c, "POST", base+"/actions/reboot", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Hetzner server rebooting..."
		case "poweron":
			if _, _, err := dcDoJSON(c, "POST", base+"/actions/poweron", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Hetzner server powering on..."
		case "poweroff":
			if _, _, err := dcDoJSON(c, "POST", base+"/actions/poweroff", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Hetzner server powered off."
		case "delete":
			if _, _, err := dcDoJSON(c, "DELETE", base, hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Hetzner server deleted permanently."
		case "open_peer_fw":
			srvInt, _ := strconv.ParseInt(req.ServerID, 10, 64)
			rules := []map[string]any{
				{"direction": "in", "protocol": "tcp", "port": "22", "source_ips": []string{"0.0.0.0/0", "::/0"}, "description": "SSH"},
			}
			for _, pt := range peerV4FirewallPorts {
				rules = append(rules, map[string]any{
					"direction": "in", "protocol": "tcp", "port": strconv.Itoa(pt),
					"source_ips": []string{"0.0.0.0/0", "::/0"}, "description": "MControl Peer v4",
				})
			}
			fwBody := map[string]any{
				"name":  fmt.Sprintf("mcontrol-peer-%s", req.ServerID),
				"rules": rules,
				"apply_to": []map[string]any{
					{"type": "server", "server": map[string]any{"id": srvInt}},
				},
			}
			if _, _, err := dcDoJSON(c, "POST", "https://api.hetzner.cloud/v1/firewalls", hdr, fwBody); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Hetzner Cloud Firewall created & applied (Peer v4 ports 84, 847, 7483, 24819, 2054 open)!"
		default:
			fail(w, http.StatusBadRequest, "unsupported action")
			return
		}

	case "digitalocean":
		if cfg.DigitalOcean.Token == "" {
			fail(w, http.StatusBadRequest, "DigitalOcean API token not configured")
			return
		}
		hdr := map[string]string{"Authorization": "Bearer " + cfg.DigitalOcean.Token}
		base := "https://api.digitalocean.com/v2/droplets/" + req.ServerID
		switch req.Action {
		case "rebuild":
			if req.Image == "" {
				req.Image = "ubuntu-24-04-x64"
			}
			if _, _, err := dcDoJSON(c, "POST", base+"/actions", hdr, map[string]any{"type": "rebuild", "image": req.Image}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "DigitalOcean droplet rebuild started (" + req.Image + ")!"
		case "reboot":
			if _, _, err := dcDoJSON(c, "POST", base+"/actions", hdr, map[string]any{"type": "reboot"}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "DigitalOcean droplet rebooting..."
		case "poweron":
			if _, _, err := dcDoJSON(c, "POST", base+"/actions", hdr, map[string]any{"type": "power_on"}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "DigitalOcean droplet powering on..."
		case "poweroff":
			if _, _, err := dcDoJSON(c, "POST", base+"/actions", hdr, map[string]any{"type": "power_off"}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "DigitalOcean droplet powered off."
		case "delete":
			if _, _, err := dcDoJSON(c, "DELETE", base, hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "DigitalOcean droplet deleted."
		case "open_peer_fw":
			srvInt, _ := strconv.ParseInt(req.ServerID, 10, 64)
			inRules := []map[string]any{
				{"protocol": "tcp", "ports": "22", "sources": map[string]any{"addresses": []string{"0.0.0.0/0", "::/0"}}},
			}
			for _, pt := range peerV4FirewallPorts {
				inRules = append(inRules, map[string]any{
					"protocol": "tcp", "ports": strconv.Itoa(pt),
					"sources": map[string]any{"addresses": []string{"0.0.0.0/0", "::/0"}},
				})
			}
			fwBody := map[string]any{
				"name":          fmt.Sprintf("mcontrol-peer-%s", req.ServerID),
				"inbound_rules": inRules,
				"outbound_rules": []map[string]any{
					{"protocol": "tcp", "ports": "all", "destinations": map[string]any{"addresses": []string{"0.0.0.0/0", "::/0"}}},
					{"protocol": "udp", "ports": "all", "destinations": map[string]any{"addresses": []string{"0.0.0.0/0", "::/0"}}},
				},
				"droplet_ids": []int64{srvInt},
			}
			if _, _, err := dcDoJSON(c, "POST", "https://api.digitalocean.com/v2/firewalls", hdr, fwBody); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "DigitalOcean Cloud Firewall created & applied for Peer v4 ports!"
		default:
			fail(w, http.StatusBadRequest, "unsupported action")
			return
		}

	case "linode":
		if cfg.Linode.Token == "" {
			fail(w, http.StatusBadRequest, "Linode API token not configured")
			return
		}
		hdr := map[string]string{"Authorization": "Bearer " + cfg.Linode.Token}
		base := "https://api.linode.com/v4/linode/instances/" + req.ServerID
		switch req.Action {
		case "rebuild":
			if req.Image == "" {
				req.Image = "linode/ubuntu24.04"
			}
			if len(req.RootPassword) < 8 {
				req.RootPassword = "Mc!" + shortRandID() + "9A#"
			}
			if _, _, err := dcDoJSON(c, "POST", base+"/rebuild", hdr, map[string]any{"image": req.Image, "root_pass": req.RootPassword}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			newPass = req.RootPassword
			msg = "Linode OS rebuild started (" + req.Image + ")!"
		case "reboot":
			if _, _, err := dcDoJSON(c, "POST", base+"/reboot", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Linode instance rebooting..."
		case "poweron":
			if _, _, err := dcDoJSON(c, "POST", base+"/boot", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Linode instance booting..."
		case "poweroff":
			if _, _, err := dcDoJSON(c, "POST", base+"/shutdown", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Linode instance shutting down..."
		case "delete":
			if _, _, err := dcDoJSON(c, "DELETE", base, hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Linode instance deleted."
		case "open_peer_fw":
			srvInt, _ := strconv.ParseInt(req.ServerID, 10, 64)
			inRules := []map[string]any{
				{"action": "ACCEPT", "protocol": "TCP", "ports": "22, 84, 847, 7483, 24819, 2054", "addresses": map[string]any{"ipv4": []string{"0.0.0.0/0"}, "ipv6": []string{"::/0"}}},
			}
			fwBody := map[string]any{
				"label": fmt.Sprintf("mcontrol-peer-%s", req.ServerID),
				"rules": map[string]any{
					"inbound_policy":  "ACCEPT",
					"outbound_policy": "ACCEPT",
					"inbound":         inRules,
				},
				"devices": map[string]any{"linodes": []int64{srvInt}},
			}
			if _, _, err := dcDoJSON(c, "POST", "https://api.linode.com/v4/networking/firewalls", hdr, fwBody); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "Linode Cloud Firewall created & applied for Peer v4 ports!"
		default:
			fail(w, http.StatusBadRequest, "unsupported action")
			return
		}

	case "ovh":
		if cfg.OVH.AppKey == "" || cfg.OVH.AppSecret == "" || cfg.OVH.ConsumerKey == "" {
			fail(w, http.StatusBadRequest, "OVH API credentials not configured")
			return
		}
		base := "/vps/" + req.ServerID
		switch req.Action {
		case "rebuild":
			if req.Image == "" {
				fail(w, http.StatusBadRequest, "select an OVH OS image first")
				return
			}
			if _, err := ovhDoJSON(c, cfg.OVH, "POST", base+"/rebuild", map[string]any{"imageId": req.Image, "doNotSendPassword": false}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "OVH VPS rebuild initiated (credentials emailed by OVH)!"
		case "reboot":
			if _, err := ovhDoJSON(c, cfg.OVH, "POST", base+"/reboot", nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "OVH VPS rebooting..."
		case "poweron":
			if _, err := ovhDoJSON(c, cfg.OVH, "POST", base+"/start", nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "OVH VPS starting..."
		case "poweroff":
			if _, err := ovhDoJSON(c, cfg.OVH, "POST", base+"/stop", nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "OVH VPS stopping..."
		default:
			fail(w, http.StatusBadRequest, "unsupported action for OVH VPS")
			return
		}

	case "yottasrc":
		if cfg.YottaSrc.Endpoint == "" || cfg.YottaSrc.Token == "" {
			fail(w, http.StatusBadRequest, "YottaSrc API not configured")
			return
		}
		hdr := yottaHeaders(cfg.YottaSrc)
		base := cfg.YottaSrc.Endpoint + "/servers/" + req.ServerID
		switch req.Action {
		case "rebuild":
			if _, _, err := dcDoJSON(c, "POST", base+"/rebuild", hdr, map[string]any{"os": req.Image, "password": req.RootPassword}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			newPass = req.RootPassword
			msg = "YottaSrc OS rebuild requested!"
		case "reboot":
			if _, _, err := dcDoJSON(c, "POST", base+"/reboot", hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "YottaSrc server rebooting..."
		case "poweron":
			if _, _, err := dcDoJSON(c, "POST", base+"/power", hdr, map[string]any{"action": "start"}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "YottaSrc server powering on..."
		case "poweroff":
			if _, _, err := dcDoJSON(c, "POST", base+"/power", hdr, map[string]any{"action": "stop"}); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "YottaSrc server powering off..."
		case "delete":
			if _, _, err := dcDoJSON(c, "DELETE", base, hdr, nil); err != nil {
				fail(w, http.StatusBadGateway, err.Error())
				return
			}
			msg = "YottaSrc server deleted."
		default:
			fail(w, http.StatusBadRequest, "unsupported action")
			return
		}

	default:
		fail(w, http.StatusBadRequest, "unknown provider")
		return
	}

	h.logAudit(r.Header.Get("X-MC-User"), "POST", r.URL.Path, fmt.Sprintf("DC %s %s on %s", req.Provider, req.Action, req.ServerID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg, "root_password": newPass})
}

func (h *Handler) handleDCImportServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		Provider   string `json:"provider"`
		Region     string `json:"region"`
		Role       string `json:"role"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Host == "" {
		fail(w, http.StatusBadRequest, "server IPv4/host is required")
		return
	}
	if req.Port <= 0 {
		req.Port = 22
	}
	if req.Username == "" {
		req.Username = "root"
	}
	if req.Role != "iran" {
		req.Role = "foreign"
	}
	if req.Name == "" {
		req.Name = req.Host
	}
	dcLbl := strings.ToUpper(req.Provider)
	if req.Region != "" {
		dcLbl += " (" + req.Region + ")"
	}
	srv := &db.Server{
		Name: req.Name, Role: req.Role, Host: req.Host, Port: req.Port,
		Username: req.Username, Password: req.Password, AuthType: "password",
		Datacenter: dcLbl,
	}
	id, err := h.store.AddServer(srv)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.logAudit(r.Header.Get("X-MC-User"), "POST", r.URL.Path, "Imported cloud server "+req.Name+" ("+req.Host+")")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": "Imported " + req.Name + " into MControl Servers!"})
}
