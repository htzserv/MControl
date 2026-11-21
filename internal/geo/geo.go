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

var (
	cacheMu sync.Mutex
	cache   = map[string]Info{}
)

func unknown() Info {
	return Info{Country: "Unknown", CountryCode: "", Datacenter: "Unknown"}
}

// Lookup accepts an IP or a hostname. Hostnames are resolved first (IPv4 preferred);
// loopback / private addresses are never sent to the external GeoIP service.
func Lookup(host string) Info {
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
	cacheMu.Lock()
	if v, ok := cache[key]; ok {
		cacheMu.Unlock()
		return v
	}
	cacheMu.Unlock()

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("https://ipwho.is/" + url.PathEscape(key))
	if err != nil {
		return unknown()
	}
	defer resp.Body.Close()

	var data ipWhoResp
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || !data.Success {
		return unknown()
	}
	dc := strings.TrimSpace(data.Connection.Org)
	if dc == "" {
		dc = strings.TrimSpace(data.Connection.ISP)
	}
	if dc == "" {
		dc = "Unknown DC"
	}
	info := Info{
		Country:     data.Country,
		CountryCode: strings.ToUpper(data.CountryCode),
		Datacenter:  dc,
	}
	cacheMu.Lock()
	cache[key] = info
	cacheMu.Unlock()
	return info
}
