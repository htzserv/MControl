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
	Label     string `json:"label"`
}

type BenchResult struct {
	Rank      int     `json:"rank"`
	Engine    string  `json:"engine"`
	Transport string  `json:"transport"`
	IPMode    string  `json:"ip_mode"`
	Label     string  `json:"label"`
	OK        bool    `json:"ok"`
	Mbps      float64 `json:"mbps"`
	PingMs    float64 `json:"ping_ms"`
	Score     int     `json:"score"`
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

var (
	benchMu  sync.Mutex
	benchJob = BenchJob{Logs: []string{}, Results: []BenchResult{}}
)

func benchLog(line string) {
	benchMu.Lock()
	defer benchMu.Unlock()
	benchJob.Logs = append(benchJob.Logs, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), line))
	if len(benchJob.Logs) > 120 {
		benchJob.Logs = benchJob.Logs[len(benchJob.Logs)-120:]
	}
}

func (h *Handler) handleBenchmarkStatus(w http.ResponseWriter, r *http.Request) {
	benchMu.Lock()
	cp := benchJob
	cp.Logs = append([]string(nil), benchJob.Logs...)
	cp.Results = append([]BenchResult(nil), benchJob.Results...)
	benchMu.Unlock()
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
			{Engine: "backhaul", Transport: "tcp", Label: "Backhaul TCP"},
			{Engine: "backhaul", Transport: "tcpmux", Label: "Backhaul TCPMUX"},
			{Engine: "backhaul", Transport: "ws", Label: "Backhaul WS"},
			{Engine: "backhaul", Transport: "kcp", Label: "Backhaul KCP"},
			{Engine: "rathole", Transport: "tcp", Label: "Rathole TCP"},
			{Engine: "gre", Transport: "raw", Label: "Kernel GRE"},
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

	benchMu.Lock()
	if benchJob.Running {
		benchMu.Unlock()
		fail(w, http.StatusConflict, "a benchmark is already running")
		return
	}
	benchJob = BenchJob{
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
	benchMu.Unlock()

	go h.runBenchmarkJob(iranSrv, forSrv, req.Profile, req.Candidates)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var iperfMbpsRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s+(G|M|K)bits/sec`)
var tcpPingRe = regexp.MustCompile(`RTT_MS=([0-9]+(?:\.[0-9]+)?)`)

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
		benchID  = int64(999)
		ctrlPort = 49150
		dataPort = 49151
	)
	var results []BenchResult

	cleanCmd := `systemctl disable --now mcontrol-tun-999.service 2>/dev/null || true
rm -f /etc/systemd/system/mcontrol-tun-999.service /etc/mcontrol/tunnels/mcontrol-tun-999.toml /etc/mcontrol/tunnels/mcontrol-tun-999.sh
ip link del mctun999 2>/dev/null || true
ip tunnel del mctun999 2>/dev/null || true
iptables-save -t nat | grep -v "mc_tun_999" | iptables-restore -T nat 2>/dev/null || true
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
		benchLog("IPv6 candidate selected: ensuring IPv6 is enabled on both nodes & probing IPv6 reachability...")
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
		if iran.IPv6 != "" && iran.IPv6 != "N/A" && foreign.IPv6 != "" && foreign.IPv6 != "N/A" {
			p6 := h.runOnServer(iran, fmt.Sprintf("ping -6 -c 2 -W 2 %s 2>&1 | tail -n 2", sshclient.ShQuote(foreign.IPv6)), 8*time.Second)
			benchLog(fmt.Sprintf("IPv6 detected: Iran=%s | Foreign=%s | %s", iran.IPv6, foreign.IPv6, strings.ReplaceAll(strings.TrimSpace(p6.Output), "\n", " ")))
		} else {
			benchLog(fmt.Sprintf("IPv6 notice: Iran=%s | Foreign=%s (missing global IPv6 on one side may cause IPv6 candidates to fail)", iran.IPv6, foreign.IPv6))
		}
	}

	for idx, c := range cands {
		pct := int(float64(idx)/float64(len(cands))*92) + 5
		benchMu.Lock()
		benchJob.Current = fmt.Sprintf("Testing [%d/%d] %s...", idx+1, len(cands), c.Label)
		benchJob.Percent = pct
		benchMu.Unlock()
		benchLog(fmt.Sprintf("Deploying temporary %s tunnel...", c.Label))

		tun := &db.Tunnel{
			ID:         benchID,
			Name:       "bench-" + c.Engine,
			Engine:     c.Engine,
			Transport:  c.Transport + "@" + c.IPMode,
			TunnelPort: ctrlPort,
			Token:      "mcbench" + randTunnelToken()[:8],
			Subnet:     "10.10.250",
		}
		iranDeploy, forDeploy := buildTunnelDeployScripts(tun, iran, foreign, []int{dataPort})

		var wg sync.WaitGroup
		var rI, rF sshclient.Result
		wg.Add(2)
		go func() { defer wg.Done(); rI = h.runOnServer(iran, iranDeploy, 35*time.Second) }()
		go func() { defer wg.Done(); rF = h.runOnServer(foreign, forDeploy, 35*time.Second) }()
		wg.Wait()

		res := BenchResult{Engine: c.Engine, Transport: c.Transport, IPMode: c.IPMode, Label: c.Label}
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
			benchLog(fmt.Sprintf("✖ %s failed to deploy: %s", c.Label, res.Error))
		} else {
			time.Sleep(2500 * time.Millisecond)

			targetHost := "127.0.0.1"
			if c.Engine == "gre" || c.Engine == "ip6gre" || c.Engine == "vxlan" {
				targetHost = "10.10.250.2"
			}
			// Built-in zero-dependency Python3 sink server on Foreign (listens for 14s, accepts parallel streams)
			srvSink := fmt.Sprintf(`nohup python3 -c "
import socket, threading, time
s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(('0.0.0.0',%d)); s.listen(16); s.settimeout(14)
def h(c):
  try:
    while c.recv(65536): pass
  except: pass
  finally: c.close()
end=time.time()+14
while time.time()<end:
  try:
    c,_=s.accept(); threading.Thread(target=h,args=(c,),daemon=True).start()
  except: break
s.close()
" >/dev/null 2>&1 & sleep 0.4`, dataPort)
			_ = h.runOnServer(foreign, srvSink, 8*time.Second)

			// Measure TCP RTT + 4-stream throughput THROUGH the tunnel using built-in Python3 (no iperf3 needed!)
			cliTest := fmt.Sprintf(`python3 -c "
import socket, threading, time
host, port = '%s', %d
rtts = []
for _ in range(3):
  try:
    t0=time.time(); s=socket.create_connection((host,port),3); rtts.append((time.time()-t0)*1000); s.close()
  except: pass
if rtts: print(f'RTT_MS={min(rtts):.1f}')
total = [0]
buf = b'x'*65536
def worker():
  try:
    s=socket.create_connection((host,port),3)
    end=time.time()+3.5
    sent=0
    while time.time()<end:
      sent += s.send(buf)
    s.close()
    total[0] += sent
  except: pass
t0=time.time()
ts=[threading.Thread(target=worker) for _ in range(4)]
for t in ts: t.start()
for t in ts: t.join()
dt=max(0.1, time.time()-t0)
mbps=(total[0]*8)/(dt*1e6)
print(f'{mbps:.1f} Mbits/sec receiver')
" 2>&1`, targetHost, dataPort)

			testOut := h.runOnServer(iran, cliTest, 20*time.Second)
			mbps := parseIperfMbps(testOut.Output)
			pingMs := 0.0
			if m := tcpPingRe.FindStringSubmatch(testOut.Output); len(m) == 2 {
				pingMs, _ = strconv.ParseFloat(m[1], 64)
			}
			if mbps > 0 {
				res.OK = true
				res.Mbps = mbps
				res.PingMs = pingMs
				benchLog(fmt.Sprintf("✔ %s -> %.1f Mbps | RTT %.1f ms", c.Label, mbps, pingMs))
			} else {
				res.OK = false
				res.Error = "no throughput through tunnel"
				benchLog(fmt.Sprintf("✖ %s -> 0 Mbps (tunnel did not pass traffic)", c.Label))
			}
		}

		wg.Add(2)
		go func() { defer wg.Done(); _ = h.runOnServer(iran, cleanCmd, 15*time.Second) }()
		go func() { defer wg.Done(); _ = h.runOnServer(foreign, cleanCmd, 15*time.Second) }()
		wg.Wait()

		results = append(results, res)
		rankBenchResults(results, profile)
		benchMu.Lock()
		benchJob.Results = append([]BenchResult(nil), results...)
		benchMu.Unlock()
	}

	rankBenchResults(results, profile)
	benchLog("All candidates tested and ranked. Saving run to history...")
	benchMu.Lock()
	benchJob.Results = results
	benchJob.Running = false
	benchJob.Done = true
	benchJob.Percent = 100
	benchJob.Current = "Benchmark Complete!"
	logsCopy := strings.Join(benchJob.Logs, "\n")
	benchMu.Unlock()

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
		results[i].Score = int(math.Round((sNorm*wSpeed + pNorm*wPing + 1.0*wStab) * 100))
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
