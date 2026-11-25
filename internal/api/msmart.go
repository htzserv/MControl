package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
)

type BenchCandidate struct {
	Engine    string `json:"engine"`
	Transport string `json:"transport"`
	IPMode    string `json:"ip_mode"`
	Underlay  string `json:"underlay"` // "" | "ip" | "gre" | "vxlan"
	Label     string `json:"label"`
}

type BenchResult struct {
	Rank      int     `json:"rank"`
	Engine    string  `json:"engine"`
	Transport string  `json:"transport"`
	IPMode    string  `json:"ip_mode"`
	Underlay  string  `json:"underlay"`
	Label     string  `json:"label"`
	OK        bool    `json:"ok"`
	Mbps      float64 `json:"mbps"`
	PingMs    float64 `json:"ping_ms"`
	JitterMs  float64 `json:"jitter_ms"`
	LossPct   float64 `json:"loss_pct"`
	Score     int     `json:"score"`
	Details   string  `json:"details"`
	Error     string  `json:"error,omitempty"`
}

type BenchJob struct {
	Running         bool          `json:"running"`
	Done            bool          `json:"done"`
	IranServerID    int64         `json:"iran_server_id"`
	ForeignServerID int64         `json:"foreign_server_id"`
	Profile         string        `json:"profile"`
	Current         string        `json:"current"`
	Percent         int           `json:"percent"`
	Logs            []string      `json:"logs"`
	Results         []BenchResult `json:"results"`
}

func (h *Handler) benchLog(line string) {
	h.benchMu.Lock()
	defer h.benchMu.Unlock()
	h.benchJob.Logs = append(h.benchJob.Logs, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), line))
	if len(h.benchJob.Logs) > 160 {
		h.benchJob.Logs = h.benchJob.Logs[len(h.benchJob.Logs)-160:]
	}
}

func (h *Handler) handleBenchmarkStatus(w http.ResponseWriter, r *http.Request) {
	h.benchMu.Lock()
	cp := h.benchJob
	cp.Logs = append([]string(nil), h.benchJob.Logs...)
	cp.Results = append([]BenchResult(nil), h.benchJob.Results...)
	h.benchMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": cp})
}

func (h *Handler) handleBenchmarkStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IranServerID    int64            `json:"iran_server_id"`
		ForeignServerID int64            `json:"foreign_server_id"`
		Profile         string           `json:"profile"`
		Candidates      []BenchCandidate `json:"candidates"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.IranServerID <= 0 || req.ForeignServerID <= 0 || req.IranServerID == req.ForeignServerID {
		fail(w, http.StatusBadRequest, "select two distinct servers (Iran & Foreign)")
		return
	}
	if req.Profile != "speed" && req.Profile != "ping" {
		req.Profile = "balanced"
	}
	if len(req.Candidates) == 0 {
		req.Candidates = []BenchCandidate{
			{Engine: "backhaul", Transport: "tcp", IPMode: "v4", Underlay: "ip", Label: "Backhaul TCP"},
			{Engine: "backhaul", Transport: "tcpmux", IPMode: "v4", Underlay: "ip", Label: "Backhaul TCPMUX"},
			{Engine: "backhaul", Transport: "tls", IPMode: "v4", Underlay: "ip", Label: "Backhaul TLS (uTLS)"},
			{Engine: "backhaul", Transport: "tlsmux", IPMode: "v4", Underlay: "ip", Label: "Backhaul TLSMUX"},
			{Engine: "backhaul", Transport: "xhttps", IPMode: "v4", Underlay: "ip", Label: "Backhaul XHTTPS (H2)"},
			{Engine: "backhaul", Transport: "kcp", IPMode: "v4", Underlay: "ip", Label: "Backhaul KCP (FEC)"},
			{Engine: "backhaul", Transport: "kcpmux", IPMode: "v4", Underlay: "ip", Label: "Backhaul KCPMUX"},
			{Engine: "rathole", Transport: "tcp", IPMode: "v4", Underlay: "ip", Label: "Rathole TCP"},
			{Engine: "gre", Transport: "raw", IPMode: "v4", Underlay: "ip", Label: "GRE 4-to-4"},
			{Engine: "vxlan", Transport: "raw", IPMode: "v4", Underlay: "ip", Label: "VXLAN IPv4"},
			{Engine: "backhaul", Transport: "tcpmux", IPMode: "v4", Underlay: "gre", Label: "Backhaul TCPMUX over GRE"},
			{Engine: "rathole", Transport: "tcp", IPMode: "v4", Underlay: "gre", Label: "Rathole TCP over GRE"},
		}
	}

	iranSrv, err := h.store.GetServerWithSecret(req.IranServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Iran server not found")
		return
	}
	forSrv, err := h.store.GetServerWithSecret(req.ForeignServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "Foreign server not found")
		return
	}

	h.benchMu.Lock()
	if h.benchJob.Running {
		h.benchMu.Unlock()
		fail(w, http.StatusConflict, "a benchmark is already running")
		return
	}
	h.benchJob = BenchJob{
		Running:         true,
		Done:            false,
		IranServerID:    req.IranServerID,
		ForeignServerID: req.ForeignServerID,
		Profile:         req.Profile,
		Current:         "Initializing MSmart Lab...",
		Percent:         3,
		Logs:            []string{fmt.Sprintf("[%s] Starting MSmart Benchmark (%s -> %s, profile=%s, %d candidates)", time.Now().Format("15:04:05"), iranSrv.Name, forSrv.Name, req.Profile, len(req.Candidates))},
		Results:         []BenchResult{},
	}
	h.benchMu.Unlock()

	go h.runBenchmarkJob(iranSrv, forSrv, req.Profile, req.Candidates)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var iperfMbpsRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s+(G|M|K)bits/sec`)
var tcpPingRe = regexp.MustCompile(`RTT_MS=([0-9]+(?:\.[0-9]+)?)`)
var tcpJitterRe = regexp.MustCompile(`JITTER_MS=([0-9]+(?:\.[0-9]+)?)`)
var tcpLossRe = regexp.MustCompile(`LOSS_PCT=([0-9]+(?:\.[0-9]+)?)`)

func parseIperfMbps(out string) float64 {
	var best float64
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "receiver") || strings.Contains(line, "SUM") {
			if m := iperfMbpsRe.FindStringSubmatch(line); len(m) == 3 {
				v, _ := strconv.ParseFloat(m[1], 64)
				switch m[2] {
				case "G":
					v *= 1000
				case "K":
					v /= 1000
				}
				if v > best {
					best = v
				}
			}
		}
	}
	if best == 0 {
		for _, m := range iperfMbpsRe.FindAllStringSubmatch(out, -1) {
			v, _ := strconv.ParseFloat(m[1], 64)
			if m[2] == "G" {
				v *= 1000
			} else if m[2] == "K" {
				v /= 1000
			}
			if v > best {
				best = v
			}
		}
	}
	return math.Round(best*10) / 10
}

func (h *Handler) runBenchmarkJob(iran, foreign *db.Server, profile string, cands []BenchCandidate) {
	const (
		underlayID = int64(998)
		benchID    = int64(999)
		ctrlPort   = 49150
		dataPort   = 49151
		underPort  = 49152
	)
	var results []BenchResult

	cleanCmd := `for tid in 998 999; do
  systemctl disable --now mcontrol-tun-${tid}.service 2>/dev/null || true
  rm -f /etc/systemd/system/mcontrol-tun-${tid}.service /etc/mcontrol/tunnels/mcontrol-tun-${tid}.toml /etc/mcontrol/tunnels/mcontrol-tun-${tid}.sh
  ip link del mctun${tid} 2>/dev/null || true
  ip tunnel del mctun${tid} 2>/dev/null || true
  iptables-save -t nat | grep -v "mc_tun_${tid}" | iptables-restore -T nat 2>/dev/null || true
done
pkill -f "iperf3 -s.*49151" 2>/dev/null || true
systemctl daemon-reload`

	needV6 := false
	for _, c := range cands {
		if c.IPMode == "v6" || c.IPMode == "6to4" || c.IPMode == "6to6" || c.Engine == "ip6gre" {
			needV6 = true
			break
		}
	}
	if needV6 {
		h.benchLog("IPv6 candidate selected: enabling IPv6 & probing global addresses...")
		enV6 := `sysctl -w net.ipv6.conf.all.disable_ipv6=0 >/dev/null 2>&1 || true
sysctl -w net.ipv6.conf.default.disable_ipv6=0 >/dev/null 2>&1 || true
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null 2>&1 || true
ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}'`
		var wg6 sync.WaitGroup
		var v6I, v6F sshclient.Result
		wg6.Add(2)
		go func() { defer wg6.Done(); v6I = h.runOnServer(iran, enV6, 10*time.Second) }()
		go func() { defer wg6.Done(); v6F = h.runOnServer(foreign, enV6, 10*time.Second) }()
		wg6.Wait()
		if s := strings.TrimSpace(v6I.Output); s != "" && strings.Contains(s, ":") {
			iran.IPv6 = s
		}
		if s := strings.TrimSpace(v6F.Output); s != "" && strings.Contains(s, ":") {
			foreign.IPv6 = s
		}
	}

	for idx, c := range cands {
		if c.Underlay == "" {
			c.Underlay = "ip"
		}
		if c.IPMode == "" {
			c.IPMode = "v4"
		}
		pct := int(float64(idx)/float64(len(cands))*92) + 5
		h.benchMu.Lock()
		h.benchJob.Current = fmt.Sprintf("Testing [%d/%d] %s...", idx+1, len(cands), c.Label)
		h.benchJob.Percent = pct
		h.benchMu.Unlock()
		h.benchLog(fmt.Sprintf("Deploying temporary %s...", c.Label))

		res := BenchResult{
			Engine:    c.Engine,
			Transport: c.Transport,
			IPMode:    c.IPMode,
			Underlay:  c.Underlay,
			Label:     c.Label,
		}

		// Step A: If underlay is GRE or VXLAN (for Backhaul/Rathole/Paqet), deploy temporary carrier tunnel #998 first
		underlayOK := true
		var rI, rF sshclient.Result
		if (c.Underlay == "gre" || c.Underlay == "vxlan") && (c.Engine == "backhaul" || c.Engine == "rathole" || c.Engine == "paqet") {
			uEng := c.Underlay
			uMode := "v4"
			if c.IPMode == "v6" {
				if uEng == "gre" {
					uEng = "ip6gre"
					uMode = "6to4"
				} else {
					uMode = "v6"
				}
			}
			uTun := &db.Tunnel{
				ID:         underlayID,
				Name:       "bench-underlay-" + c.Underlay,
				Engine:     uEng,
				Transport:  "raw@" + uMode,
				TunnelPort: underPort,
				Token:      "mcunder" + randTunnelToken()[:6],
				Subnet:     "10.10.249",
			}
			uIranCmd, uForCmd := buildTunnelDeployScripts(uTun, iran, foreign, []int{ctrlPort})
			var uwg sync.WaitGroup
			uwg.Add(2)
			go func() { defer uwg.Done(); rI = h.runOnServer(iran, uIranCmd, 35*time.Second) }()
			go func() { defer uwg.Done(); rF = h.runOnServer(foreign, uForCmd, 35*time.Second) }()
			uwg.Wait()
			if rI.Err != nil || rF.Err != nil {
				underlayOK = false
			} else {
				time.Sleep(1200 * time.Millisecond)
			}
		}

		if !underlayOK {
			res.OK = false
			res.Error = "Underlay " + strings.ToUpper(c.Underlay) + " failed to establish"
			h.benchLog("✖ " + c.Label + ": " + res.Error)
		} else {
			transMode := c.Transport + "@" + c.IPMode
			subNet := "10.10.250"
			if (c.Underlay == "gre" || c.Underlay == "vxlan") && (c.Engine == "backhaul" || c.Engine == "rathole" || c.Engine == "paqet") {
				transMode = c.Transport + "@" + c.Underlay
				subNet = "10.10.249"
			}
			tun := &db.Tunnel{
				ID:         benchID,
				Name:       "bench-" + c.Engine,
				Engine:     c.Engine,
				Transport:  transMode,
				TunnelPort: ctrlPort,
				Token:      "mcbench" + randTunnelToken()[:8],
				Subnet:     subNet,
			}
			iranDeploy, forDeploy := buildTunnelDeployScripts(tun, iran, foreign, []int{dataPort})

			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); rI = h.runOnServer(iran, iranDeploy, 35*time.Second) }()
			go func() { defer wg.Done(); rF = h.runOnServer(foreign, forDeploy, 35*time.Second) }()
			wg.Wait()

			if rI.Err != nil || rF.Err != nil {
				errMsg := ""
				if rI.Err != nil {
					errMsg += "Iran: " + strings.TrimSpace(rI.Output) + " "
				}
				if rF.Err != nil {
					errMsg += "Foreign: " + strings.TrimSpace(rF.Output)
				}
				if errMsg == "" {
					errMsg = "deploy failed"
				}
				res.OK = false
				res.Error = strings.TrimSpace(errMsg)
				h.benchLog(fmt.Sprintf("✖ %s failed to deploy: %s", c.Label, res.Error))
			} else {
				time.Sleep(2400 * time.Millisecond)

				targetHost := "127.0.0.1"
				if c.Engine == "gre" || c.Engine == "ip6gre" || c.Engine == "vxlan" {
					targetHost = "10.10.250.2"
				}

				srvSink := fmt.Sprintf(`nohup python3 -c "
import socket, threading, time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(('0.0.0.0',%d)); s.listen(24); s.settimeout(15)
def h(c):
  try:
    while c.recv(65536): pass
  except: pass
  finally: c.close()
end=time.time()+15
while time.time()<end:
  try:
    c,_=s.accept(); threading.Thread(target=h,args=(c,),daemon=True).start()
  except: break
s.close()
" >/dev/null 2>&1 & sleep 0.4`, dataPort)
				_ = h.runOnServer(foreign, srvSink, 8*time.Second)

				cliTest := fmt.Sprintf(`python3 -c "
import socket, threading, time
host, port = '%s', %d
rtts = []
ok_p = 0
for _ in range(10):
  try:
    t0=time.time(); s=socket.create_connection((host,port),2.5); rtts.append((time.time()-t0)*1000); ok_p += 1; s.close()
  except: pass
  time.sleep(0.05)
loss = round(((10 - ok_p) / 10.0) * 100.0, 1)
print(f'LOSS_PCT={loss:.1f}')
if rtts:
  avg_r = sum(rtts)/len(rtts)
  jit = sum(abs(rtts[i]-rtts[i-1]) for i in range(1,len(rtts)))/max(1,len(rtts)-1) if len(rtts)>1 else 0.0
  print(f'RTT_MS={avg_r:.1f}')
  print(f'JITTER_MS={jit:.1f}')
total = [0]
buf = b'x'*65536
t_start = time.time()
t_warm = t_start + 1.5
t_end = t_start + 5.0
def worker():
  try:
    s=socket.create_connection((host,port),3)
    sent=0
    while True:
      now=time.time()
      if now>=t_end: break
      n=s.send(buf)
      if now>=t_warm: sent += n
    s.close()
    total[0] += sent
  except: pass
ts=[threading.Thread(target=worker) for _ in range(4)]
for t in ts: t.start()
for t in ts: t.join()
dt=max(0.5, min(time.time(), t_end)-t_warm)
mbps=(total[0]*8)/(dt*1e6)
print(f'{mbps:.1f} Mbits/sec receiver')
" 2>&1`, targetHost, dataPort)

				testOut := h.runOnServer(iran, cliTest, 22*time.Second)
				mbps := parseIperfMbps(testOut.Output)
				pingMs, jitterMs, lossPct := 0.0, 0.0, 100.0
				if m := tcpPingRe.FindStringSubmatch(testOut.Output); len(m) == 2 {
					pingMs, _ = strconv.ParseFloat(m[1], 64)
				}
				if m := tcpJitterRe.FindStringSubmatch(testOut.Output); len(m) == 2 {
					jitterMs, _ = strconv.ParseFloat(m[1], 64)
				}
				if m := tcpLossRe.FindStringSubmatch(testOut.Output); len(m) == 2 {
					lossPct, _ = strconv.ParseFloat(m[1], 64)
				}
				if mbps > 0 {
					res.OK = true
					res.Mbps = mbps
					res.PingMs = math.Round(pingMs*10) / 10
					res.JitterMs = math.Round(jitterMs*10) / 10
					res.LossPct = lossPct
					carrierLbl := "Direct " + strings.ToUpper(c.IPMode)
					if c.Underlay == "gre" || c.Underlay == "vxlan" {
						carrierLbl = "Over " + strings.ToUpper(c.Underlay) + " (" + strings.ToUpper(c.IPMode) + ")"
					}
					res.Details = fmt.Sprintf("%s • %s • Jitter %.1fms • Loss %.0f%%", strings.ToUpper(c.Engine), carrierLbl, res.JitterMs, res.LossPct)
					h.benchLog(fmt.Sprintf("✔ %s -> %.1f Mbps | RTT %.1f ms | Loss %.0f%%", c.Label, mbps, res.PingMs, lossPct))
				} else {
					res.OK = false
					res.LossPct = lossPct
					res.Error = "No throughput passed through tunnel"
					h.benchLog(fmt.Sprintf("✖ %s -> 0 Mbps (no traffic passed)", c.Label))
				}
			}
		}

		var cwg sync.WaitGroup
		cwg.Add(2)
		go func() { defer cwg.Done(); _ = h.runOnServer(iran, cleanCmd, 15*time.Second) }()
		go func() { defer cwg.Done(); _ = h.runOnServer(foreign, cleanCmd, 15*time.Second) }()
		cwg.Wait()

		results = append(results, res)
		rankBenchResults(results, profile)
		h.benchMu.Lock()
		h.benchJob.Results = append([]BenchResult(nil), results...)
		h.benchMu.Unlock()
	}

	rankBenchResults(results, profile)
	h.benchLog("All candidates tested and ranked.")
	h.benchMu.Lock()
	h.benchJob.Results = results
	h.benchJob.Running = false
	h.benchJob.Done = true
	h.benchJob.Percent = 100
	h.benchJob.Current = "Benchmark Complete!"
	logsCopy := strings.Join(h.benchJob.Logs, "\n")
	h.benchMu.Unlock()

	if resBytes, err := json.Marshal(results); err == nil {
		_, _ = h.store.AddBenchmarkRun(&db.BenchmarkRun{
			IranName:    iran.Name,
			ForeignName: foreign.Name,
			Profile:     profile,
			ResultsJSON: string(resBytes),
			LogsText:    logsCopy,
		})
	}
}

func rankBenchResults(results []BenchResult, profile string) {
	maxMbps := 1.0
	minPing := 9999.0
	for _, r := range results {
		if r.OK && r.Mbps > maxMbps {
			maxMbps = r.Mbps
		}
		if r.OK && r.PingMs > 0 && r.PingMs < minPing {
			minPing = r.PingMs
		}
	}
	wSpeed, wPing, wStab := 0.45, 0.30, 0.25
	if profile == "speed" {
		wSpeed, wPing, wStab = 0.65, 0.15, 0.20
	} else if profile == "ping" {
		wSpeed, wPing, wStab = 0.20, 0.60, 0.20
	}
	for i := range results {
		if !results[i].OK {
			results[i].Score = 0
			continue
		}
		sNorm := results[i].Mbps / maxMbps
		pNorm := 0.7
		if results[i].PingMs > 0 && minPing < 9999 {
			pNorm = math.Min(1.0, minPing/results[i].PingMs)
		}
		stabNorm := math.Max(0.0, 1.0-(results[i].LossPct/100.0))
		results[i].Score = int(math.Round((sNorm*wSpeed + pNorm*wPing + stabNorm*wStab) * 100))
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Mbps > results[j].Mbps
	})
	for i := range results {
		results[i].Rank = i + 1
	}
}

func (h *Handler) handleBenchmarkHistory(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListBenchmarkRuns(15)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "runs": list})
}
