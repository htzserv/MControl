package geo

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Info struct {
	Country     string
	CountryCode string
	Datacenter  string
}

type ipWhoResp struct {
	Success     bool   `json:"success"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	Connection  struct {
		Org string `json:"org"`
		ISP string `json:"isp"`
	} `json:"connection"`
}

type ipAPIResp struct {
	Status      string `json:"status"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	ISP         string `json:"isp"`
	Org         string `json:"org"`
}

type ipAPICoResp struct {
	CountryName string `json:"country_name"`
	CountryCode string `json:"country_code"`
	Org         string `json:"org"`
	Error       bool   `json:"error"`
}

var (
	cacheMu sync.Mutex
	cache   = map[string]Info{}
)

func unknown() Info {
	return Info{Country: "Unknown", CountryCode: "", Datacenter: "Unknown"}
}

func makeClient(socksProxy string) *http.Client {
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
	}
	if sp := strings.TrimSpace(socksProxy); sp != "" {
		if !strings.Contains(sp, "://") {
			sp = "socks5://" + sp
		}
		if u, err := url.Parse(sp); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: tr,
	}
}

func queryProviders(client *http.Client, ipStr string) (Info, bool) {
	esc := url.PathEscape(ipStr)

	// Provider 1: ipwho.is
	if resp, err := client.Get("https://ipwho.is/" + esc); err == nil {
		var d ipWhoResp
		decErr := json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if decErr == nil && d.Success && strings.TrimSpace(d.Country) != "" {
			dc := strings.TrimSpace(d.Connection.Org)
			if dc == "" {
				dc = strings.TrimSpace(d.Connection.ISP)
			}
			if dc == "" {
				dc = "Unknown DC"
			}
			return Info{
				Country:     strings.TrimSpace(d.Country),
				CountryCode: strings.ToUpper(strings.TrimSpace(d.CountryCode)),
				Datacenter:  dc,
			}, true
		}
	}

	// Provider 2: ip-api.com
	if resp, err := client.Get("http://ip-api.com/json/" + esc + "?fields=status,country,countryCode,isp,org"); err == nil {
		var d ipAPIResp
		decErr := json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if decErr == nil && strings.EqualFold(d.Status, "success") && strings.TrimSpace(d.Country) != "" {
			dc := strings.TrimSpace(d.Org)
			if dc == "" {
				dc = strings.TrimSpace(d.ISP)
			}
			if dc == "" {
				dc = "Unknown DC"
			}
			return Info{
				Country:     strings.TrimSpace(d.Country),
				CountryCode: strings.ToUpper(strings.TrimSpace(d.CountryCode)),
				Datacenter:  dc,
			}, true
		}
	}

	// Provider 3: ipapi.co
	if resp, err := client.Get("https://ipapi.co/" + esc + "/json/"); err == nil {
		var d ipAPICoResp
		decErr := json.NewDecoder(resp.Body).Decode(&d)
		resp.Body.Close()
		if decErr == nil && !d.Error && strings.TrimSpace(d.CountryName) != "" {
			dc := strings.TrimSpace(d.Org)
			if dc == "" {
				dc = "Unknown DC"
			}
			return Info{
				Country:     strings.TrimSpace(d.CountryName),
				CountryCode: strings.ToUpper(strings.TrimSpace(d.CountryCode)),
				Datacenter:  dc,
			}, true
		}
	}

	return unknown(), false
}

// Lookup accepts an IP or hostname without a proxy.
func Lookup(host string) Info {
	return LookupWithProxy(host, "")
}

// LookupWithProxy resolves host and queries GeoIP providers (using socksProxy if provided,
// with automatic direct fallback if the proxy fails).
func LookupWithProxy(host, socksProxy string) Info {
	return lookupInternal(host, socksProxy, false)
}

// ForceLookupWithProxy bypasses the in-memory cache for manual re-checks.
func ForceLookupWithProxy(host, socksProxy string) Info {
	return lookupInternal(host, socksProxy, true)
}

func lookupInternal(host, socksProxy string, force bool) Info {
	host = strings.TrimSpace(host)
	if host == "" {
		return unknown()
	}
	if strings.EqualFold(host, "localhost") {
		return Info{Country: "Local", CountryCode: "", Datacenter: "Localhost"}
	}

	ip := net.ParseIP(host)
	if ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return unknown()
		}
		ip = addrs[0].IP
		for _, a := range addrs {
			if a.IP.To4() != nil {
				ip = a.IP
				break
			}
		}
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return Info{Country: "Private network", CountryCode: "", Datacenter: "Local"}
	}

	key := ip.String()
	if !force {
		cacheMu.Lock()
		if v, ok := cache[key]; ok {
			cacheMu.Unlock()
			return v
		}
		cacheMu.Unlock()
	}

	if strings.TrimSpace(socksProxy) != "" {
		if info, ok := queryProviders(makeClient(socksProxy), key); ok {
			cacheMu.Lock()
			cache[key] = info
			cacheMu.Unlock()
			return info
		}
	}
	if info, ok := queryProviders(makeClient(""), key); ok {
		cacheMu.Lock()
		cache[key] = info
		cacheMu.Unlock()
		return info
	}
	return unknown()
}
