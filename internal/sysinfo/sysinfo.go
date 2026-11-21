package sysinfo

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Stats struct {
	Hostname    string  `json:"hostname"`
	OS          string  `json:"os"`
	Arch        string  `json:"arch"`
	Cores       int     `json:"cores"`
	IPv4        string  `json:"ipv4"`
	IPv6        string  `json:"ipv6"`
	CPUPercent  float64 `json:"cpu_percent"`
	LoadAvg     string  `json:"load_avg"`
	RAMUsedMB   uint64  `json:"ram_used_mb"`
	RAMTotalMB  uint64  `json:"ram_total_mb"`
	RAMPercent  float64 `json:"ram_percent"`
	DiskUsedGB  float64 `json:"disk_used_gb"`
	DiskTotalGB float64 `json:"disk_total_gb"`
	DiskPercent float64 `json:"disk_percent"`
	NetRxSpeed  uint64  `json:"net_rx_speed"` // bytes/sec
	NetTxSpeed  uint64  `json:"net_tx_speed"` // bytes/sec
	NetRxTotal  uint64  `json:"net_rx_total"` // bytes
	NetTxTotal  uint64  `json:"net_tx_total"` // bytes
	Uptime      string  `json:"uptime"`
	TCPConns    int     `json:"tcp_conns"`
	UDPConns    int     `json:"udp_conns"`
	BBR         string  `json:"bbr"`
}

var (
	mu          sync.Mutex
	prevTotal   uint64
	prevIdle    uint64
	prevRx      uint64
	prevTx      uint64
	prevNetTime time.Time
	cachedIPv4  string
	cachedIPv6  string
	ipCacheTime time.Time
)

func getIPs() (string, string) {
	if time.Since(ipCacheTime) < 60*time.Second && (cachedIPv4 != "" || cachedIPv6 != "") {
		return cachedIPv4, cachedIPv6
	}
	var v4, v6 string
	if c, err := net.DialTimeout("udp4", "1.1.1.1:80", time.Second); err == nil {
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			v4 = a.IP.String()
		}
		c.Close()
	}
	if c, err := net.DialTimeout("udp6", "[2606:4700:4700::1111]:80", time.Second); err == nil {
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			v6 = a.IP.String()
		}
		c.Close()
	}
	if v4 == "" {
		v4 = "N/A"
	}
	if v6 == "" {
		v6 = "N/A"
	}
	cachedIPv4, cachedIPv6, ipCacheTime = v4, v6, time.Now()
	return v4, v6
}

func readCPU() float64 {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 {
		return 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}
	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		total += v
		if i == 4 || i == 5 { // idle + iowait
			idle += v
		}
	}
	if prevTotal == 0 {
		prevTotal, prevIdle = total, idle
		return 0
	}
	dt := total - prevTotal
	di := idle - prevIdle
	prevTotal, prevIdle = total, idle
	if dt == 0 {
		return 0
	}
	pct := (float64(dt-di) / float64(dt)) * 100.0
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

func readRAM() (usedMB, totalMB uint64, pct float64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0
	}
	var totalKB, availKB uint64
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalKB, _ = strconv.ParseUint(fields[1], 10, 64)
		case "MemAvailable:":
			availKB, _ = strconv.ParseUint(fields[1], 10, 64)
		}
	}
	if totalKB == 0 {
		return 0, 0, 0
	}
	usedKB := totalKB - availKB
	return usedKB / 1024, totalKB / 1024, (float64(usedKB) / float64(totalKB)) * 100.0
}

func readDisk() (usedGB, totalGB, pct float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return 0, 0, 0
	}
	total := float64(st.Blocks) * float64(st.Bsize)
	free := float64(st.Bavail) * float64(st.Bsize)
	if total == 0 {
		return 0, 0, 0
	}
	used := total - free
	return used / (1024 * 1024 * 1024), total / (1024 * 1024 * 1024), (used / total) * 100.0
}

func readNet() (rxTotal, txTotal, rxSpeed, txSpeed uint64) {
	raw, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" || strings.HasPrefix(iface, "docker") || strings.HasPrefix(iface, "veth") {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 10 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		rxTotal += rx
		txTotal += tx
	}
	now := time.Now()
	if !prevNetTime.IsZero() {
		dt := now.Sub(prevNetTime).Seconds()
		if dt > 0.2 {
			if rxTotal >= prevRx {
				rxSpeed = uint64(float64(rxTotal-prevRx) / dt)
			}
			if txTotal >= prevTx {
				txSpeed = uint64(float64(txTotal-prevTx) / dt)
			}
		}
	}
	prevRx, prevTx, prevNetTime = rxTotal, txTotal, now
	return rxTotal, txTotal, rxSpeed, txSpeed
}

func readUptime() string {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "---"
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "---"
	}
	secF, _ := strconv.ParseFloat(fields[0], 64)
	sec := int64(secF)
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func readLoadAvg() string {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "---"
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return "---"
	}
	return fmt.Sprintf("%s / %s / %s", fields[0], fields[1], fields[2])
}


func countProcSockets(path string, onlyEstablished bool) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	lines := strings.Split(string(raw), "\n")
	count := 0
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if onlyEstablished {
			fields := strings.Fields(line)
			if len(fields) >= 4 && fields[3] == "01" {
				count++
			}
		} else {
			count++
		}
	}
	return count
}

func readBBR() string {
	raw, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(raw))
}

func Collect() Stats {
	mu.Lock()
	defer mu.Unlock()

	host, _ := os.Hostname()
	v4, v6 := getIPs()
	cpuPct := readCPU()
	ramUsed, ramTotal, ramPct := readRAM()
	diskUsed, diskTotal, diskPct := readDisk()
	rxTot, txTot, rxSpd, txSpd := readNet()

	return Stats{
		Hostname:    host,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Cores:       runtime.NumCPU(),
		IPv4:        v4,
		IPv6:        v6,
		CPUPercent:  cpuPct,
		LoadAvg:     readLoadAvg(),
		RAMUsedMB:   ramUsed,
		RAMTotalMB:  ramTotal,
		RAMPercent:  ramPct,
		DiskUsedGB:  diskUsed,
		DiskTotalGB: diskTotal,
		DiskPercent: diskPct,
		NetRxSpeed:  rxSpd,
		NetTxSpeed:  txSpd,
		NetRxTotal:  rxTot,
		NetTxTotal:  txTot,
		Uptime:      readUptime(),
		TCPConns:    countProcSockets("/proc/net/tcp", true) + countProcSockets("/proc/net/tcp6", true),
		UDPConns:    countProcSockets("/proc/net/udp", false) + countProcSockets("/proc/net/udp6", false),
		BBR:         readBBR(),
	}
}
