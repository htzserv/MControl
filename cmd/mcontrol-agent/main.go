package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"net"
	"crypto/cipher"
	"crypto/aes"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const agentVersion = "v4.1-veil"

type statsPayload struct {
	Online        bool   `json:"online"`
	CPU           string `json:"cpu"`
	RAM           string `json:"ram"`
	Uptime        string `json:"uptime"`
	IPv4          string `json:"ipv4"`
	IPv6          string `json:"ipv6"`
	NetRx         string `json:"net_rx"`
	NetTx         string `json:"net_tx"`
	CoreInstalled bool   `json:"core_installed"`
	CoreVersion   string `json:"core_version"`
}

var (
	primaryPeer string
	masterURL    string
	masterCertFP string
	listenPort  int
	peersFile   string

	statsMu     sync.RWMutex
	cachedStats = statsPayload{
		Online: true, CPU: "0%", RAM: "0%", Uptime: "---",
		IPv4: "N/A", IPv6: "N/A", NetRx: "0 B/s", NetTx: "0 B/s",
		CoreInstalled: true, CoreVersion: "agent-" + agentVersion,
	}
)

func loadPeerCodes() []string {
	var codes []string
	if primaryPeer != "" {
		codes = append(codes, primaryPeer)
	}
	b, err := os.ReadFile(peersFile)
	if err != nil {
		return codes
	}
	for _, ln := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(ln)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if idx := strings.Index(s, "|"); idx >= 0 {
			s = strings.TrimSpace(s[idx+1:])
		}
		if s != "" {
			codes = append(codes, s)
		}
	}
	return codes
}

func peerOK(code string) bool {
	if code == "" {
		return false
	}
	for _, c := range loadPeerCodes() {
		if c == code {
			return true
		}
	}
	return false
}

func readCPU() (uint64, uint64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	first := strings.SplitN(string(b), "\n", 2)[0]
	f := strings.Fields(first)
	if len(f) < 5 {
		return 0, 0
	}
	var total, idle uint64
	for i := 1; i < len(f); i++ {
		v, _ := strconv.ParseUint(f[i], 10, 64)
		total += v
		if i == 4 || i == 5 {
			idle += v
		}
	}
	return total, idle
}

func readNetTotals() (uint64, uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	var rx, tx uint64
	for _, ln := range strings.Split(string(b), "\n") {
		idx := strings.Index(ln, ":")
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(ln[:idx])
		if iface == "lo" || strings.HasPrefix(iface, "docker") || strings.HasPrefix(iface, "veth") {
			continue
		}
		f := strings.Fields(ln[idx+1:])
		if len(f) >= 10 {
			r, _ := strconv.ParseUint(f[0], 10, 64)
			t, _ := strconv.ParseUint(f[8], 10, 64)
			rx += r
			tx += t
		}
	}
	return rx, tx
}

func fmtBps(d float64) string {
	if d >= 1048576 {
		return fmt.Sprintf("%.1f MB/s", d/1048576)
	}
	if d >= 1024 {
		return fmt.Sprintf("%.1f KB/s", d/1024)
	}
	return fmt.Sprintf("%d B/s", int(d))
}

func cmdOut(c string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "sh", "-c", c).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func sampleStats(prevTot, prevIdle, prevRx, prevTx uint64, dtSec float64) (statsPayload, uint64, uint64, uint64, uint64) {
	curTot, curIdle := readCPU()
	curRx, curTx := readNetTotals()

	cpuStr := "0%"
	if curTot > prevTot && prevTot > 0 {
		dt := curTot - prevTot
		di := uint64(0)
		if curIdle >= prevIdle {
			di = curIdle - prevIdle
		}
		pct := int((dt - di) * 100 / dt)
		if pct < 0 {
			pct = 0
		} else if pct > 100 {
			pct = 100
		}
		cpuStr = fmt.Sprintf("%d%%", pct)
	}
	netRx, netTx := "0 B/s", "0 B/s"
	if dtSec > 0 && prevRx > 0 && curRx >= prevRx {
		netRx = fmtBps(float64(curRx-prevRx) / dtSec)
	}
	if dtSec > 0 && prevTx > 0 && curTx >= prevTx {
		netTx = fmtBps(float64(curTx-prevTx) / dtSec)
	}

	ramStr := "0%"
	if mb, err := os.ReadFile("/proc/meminfo"); err == nil {
		var tot, avail uint64
		for _, ln := range strings.Split(string(mb), "\n") {
			f := strings.Fields(ln)
			if len(f) < 2 {
				continue
			}
			if f[0] == "MemTotal:" {
				tot, _ = strconv.ParseUint(f[1], 10, 64)
			} else if f[0] == "MemAvailable:" {
				avail, _ = strconv.ParseUint(f[1], 10, 64)
			}
		}
		if tot > 0 && tot >= avail {
			ramStr = fmt.Sprintf("%d%%", (tot-avail)*100/tot)
		}
	}

	upStr := "---"
	if ub, err := os.ReadFile("/proc/uptime"); err == nil {
		f := strings.Fields(string(ub))
		if len(f) > 0 {
			secF, _ := strconv.ParseFloat(f[0], 64)
			sec := int(secF)
			d, h, m := sec/86400, (sec%86400)/3600, (sec%3600)/60
			if d > 0 {
				upStr = fmt.Sprintf("%dd %dh %dm", d, h, m)
			} else {
				upStr = fmt.Sprintf("%dh %dm", h, m)
			}
		}
	}

	ip4 := cmdOut(`ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}'`)
	if ip4 == "" {
		ip4 = "N/A"
	}
	ip6 := cmdOut(`ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}'`)
	if ip6 == "" {
		ip6 = "N/A"
	}

	st := statsPayload{
		Online: true, CPU: cpuStr, RAM: ramStr, Uptime: upStr,
		IPv4: ip4, IPv6: ip6, NetRx: netRx, NetTx: netTx,
		CoreInstalled: true, CoreVersion: "agent-" + agentVersion,
	}
	return st, curTot, curIdle, curRx, curTx
}

func startStatsLoop() {
	tot, idle := readCPU()
	rx, tx := readNetTotals()
	time.Sleep(300 * time.Millisecond)
	st, tot, idle, rx, tx := sampleStats(tot, idle, rx, tx, 0.3)
	statsMu.Lock()
	cachedStats = st
	statsMu.Unlock()

	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			st, tot, idle, rx, tx = sampleStats(tot, idle, rx, tx, 2.0)
			statsMu.Lock()
			cachedStats = st
			statsMu.Unlock()
		}
	}()
}

func startHeartbeatLoop() {
	if masterURL == "" || primaryPeer == "" {
		return
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: true}
	if masterCertFP != "" {
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("missing master certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			if hex.EncodeToString(sum[:]) != masterCertFP {
				return fmt.Errorf("master TLS certificate pin mismatch")
			}
			return nil
		}
	}
	client := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsCfg,
		},
	}
	target := strings.TrimRight(masterURL, "/") + "/_mc_agent/heartbeat"
	go func() {
		for {
			time.Sleep(10 * time.Second)
			statsMu.RLock()
			st := cachedStats
			statsMu.RUnlock()
			body, _ := json.Marshal(st)
			req, err := http.NewRequest("POST", target, bytes.NewReader(body))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Peer-Code", primaryPeer)
			resp, err := client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}()
}

func ensureTLSCert(crtPath, keyPath string) error {
	if _, e1 := os.Stat(crtPath); e1 == nil {
		if _, e2 := os.Stat(keyPath); e2 == nil {
			return nil
		}
	}
	_ = os.MkdirAll(filepath.Dir(crtPath), 0700)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "mcontrol-agent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	crtPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(crtPath, crtPEM, 0644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyPEM, 0600)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func runSpeedtest() map[string]any {
	script := `
if command -v speedtest >/dev/null 2>&1 && speedtest --accept-license --accept-gdpr -f csv >/tmp/.mc_spd 2>/dev/null; then
  awk -F',' '{gsub(/"/,""); printf "%.1f|%.1f|%.1f\n", $3, ($6*8)/1000000, ($7*8)/1000000}' /tmp/.mc_spd
  rm -f /tmp/.mc_spd; exit 0
fi
ping_ms=$(ping -c 2 -W 2 1.1.1.1 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%.1f", $5; else print "N/A"}')
[ -z "$ping_ms" ] && ping_ms="N/A"
dl_bps=$(curl -4 -s -o /dev/null --max-time 8 -w '%{speed_download}' https://speed.cloudflare.com/__down?bytes=10000000 2>/dev/null || echo 0)
dl_mbps=$(awk -v b="$dl_bps" 'BEGIN{printf "%.1f", (b*8)/1000000}')
ul_bps=$(dd if=/dev/zero bs=1M count=4 2>/dev/null | curl -4 -s -o /dev/null --max-time 8 -X POST --data-binary @- -w '%{speed_upload}' https://speed.cloudflare.com/__up 2>/dev/null || echo 0)
ul_mbps=$(awk -v b="$ul_bps" 'BEGIN{printf "%.1f", (b*8)/1000000}')
printf '%s|%s|%s' "$ping_ms" "$dl_mbps" "$ul_mbps"
`
	ctx, cancel := context.WithTimeout(context.Background(), 26*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "bash", "-c", script).Output()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	p := strings.Split(lines[len(lines)-1], "|")
	if len(p) >= 3 {
		return map[string]any{"ping_ms": p[0], "download_mbps": p[1], "upload_mbps": p[2]}
	}
	return map[string]any{"error": "failed"}
}


// ── Dual-Mode Anti-DPI Veil Listener (Veil-wrapped TLS + Legacy Plain TLS) ──

type peekBufConn struct {
	net.Conn
	pre []byte
}

func (p *peekBufConn) Read(b []byte) (int, error) {
	if len(p.pre) > 0 {
		n := copy(b, p.pre)
		p.pre = p.pre[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

type agentVeilConn struct {
	net.Conn
	rmu sync.Mutex
	wmu sync.Mutex
	rs  cipher.Stream
	ws  cipher.Stream
}

func (v *agentVeilConn) Read(p []byte) (int, error) {
	v.rmu.Lock()
	defer v.rmu.Unlock()
	n, err := v.Conn.Read(p)
	if n > 0 {
		v.rs.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (v *agentVeilConn) Write(p []byte) (int, error) {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	buf := make([]byte, len(p))
	v.ws.XORKeyStream(buf, p)
	return v.Conn.Write(buf)
}

func deriveAgentVeilStreams(peerCode string, nonce []byte) (c2s, s2c cipher.Stream) {
	mk := func(dir string) cipher.Stream {
		h := sha256.New()
		h.Write([]byte("mc-veil-" + dir + ":"))
		h.Write([]byte(peerCode))
		h.Write(nonce)
		key := h.Sum(nil)
		ivh := sha256.New()
		ivh.Write([]byte("mc-veil-iv-" + dir + ":"))
		ivh.Write(nonce)
		ivh.Write([]byte(peerCode))
		iv := ivh.Sum(nil)[:aes.BlockSize]
		blk, _ := aes.NewCipher(key)
		return cipher.NewCTR(blk, iv)
	}
	return mk("c2s"), mk("s2c")
}

func unwrapAgentVeil(raw net.Conn) (net.Conn, error) {
	_ = raw.SetReadDeadline(time.Now().Add(8 * time.Second))
	var first [1]byte
	if _, err := io.ReadFull(raw, first[:]); err != nil {
		return nil, err
	}
	if first[0] == 0x16 {
		_ = raw.SetReadDeadline(time.Time{})
		return &peekBufConn{Conn: raw, pre: first[:]}, nil
	}
	nonce := make([]byte, 32)
	nonce[0] = first[0]
	if _, err := io.ReadFull(raw, nonce[1:]); err != nil {
		return nil, err
	}
	var hdr5 [5]byte
	if _, err := io.ReadFull(raw, hdr5[:]); err != nil {
		return nil, err
	}
	for _, code := range loadPeerCodes() {
		c2s, s2c := deriveAgentVeilStreams(code, nonce)
		var dec [5]byte
		c2s.XORKeyStream(dec[:], hdr5[:])
		if string(dec[:4]) == "MCV1" {
			padLen := int(dec[4])
			if padLen > 0 {
				pad := make([]byte, padLen)
				if _, err := io.ReadFull(raw, pad); err != nil {
					return nil, err
				}
				c2s.XORKeyStream(pad, pad)
			}
			_ = raw.SetReadDeadline(time.Time{})
			return &agentVeilConn{Conn: raw, rs: c2s, ws: s2c}, nil
		}
	}
	return nil, fmt.Errorf("veil authentication failed")
}

type veilTLSListener struct {
	net.Listener
	tlsCfg *tls.Config
	connCh chan net.Conn
	errCh  chan error
}

func newVeilTLSListener(ln net.Listener, tlsCfg *tls.Config) net.Listener {
	vl := &veilTLSListener{
		Listener: ln,
		tlsCfg:   tlsCfg,
		connCh:   make(chan net.Conn, 64),
		errCh:    make(chan error, 1),
	}
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				vl.errCh <- err
				return
			}
			go func(c net.Conn) {
				unwrapped, uerr := unwrapAgentVeil(c)
				if uerr != nil {
					c.Close()
					return
				}
				tc := tls.Server(unwrapped, tlsCfg)
				select {
				case vl.connCh <- tc:
				default:
					tc.Close()
				}
			}(raw)
		}
	}()
	return vl
}

func (vl *veilTLSListener) Accept() (net.Conn, error) {
	select {
	case c := <-vl.connCh:
		return c, nil
	case err := <-vl.errCh:
		return nil, err
	}
}

func main() {
	primaryPeer = strings.TrimSpace(os.Getenv("MC_PEER"))
	masterURL = strings.TrimSpace(os.Getenv("MC_MASTER_URL"))
	masterCertFP = strings.ToLower(strings.TrimSpace(os.Getenv("MC_MASTER_CERT_FP")))
	listenPort, _ = strconv.Atoi(os.Getenv("MC_PORT"))
	if listenPort <= 0 {
		listenPort = 2054
	}
	peersFile = fmt.Sprintf("/etc/mcontrol-agent/peers-%d.txt", listenPort)

	startStatsLoop()
	startHeartbeatLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !peerOK(r.Header.Get("X-Peer-Code")) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet {
			if strings.HasPrefix(r.URL.Path, "/speedtest") {
				writeJSON(w, http.StatusOK, runSpeedtest())
				return
			}
			statsMu.RLock()
			st := cachedStats
			statsMu.RUnlock()
			writeJSON(w, http.StatusOK, st)
			return
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/exec") {
			var req struct {
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json"})
				return
			}
			cmdStr := strings.TrimSpace(req.Command)
			if cmdStr == "" {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "empty command"})
				return
			}
			tout := req.Timeout
			if tout < 5 {
				tout = 30
			} else if tout > 180 {
				tout = 180
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(tout)*time.Second)
			defer cancel()
			proc := exec.CommandContext(ctx, "bash", "-c", cmdStr)
			proc.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "CLICOLOR_FORCE=1")
			out, err := proc.CombinedOutput()
			if len(out) > 200000 {
				out = out[:200000]
			}
			if ctx.Err() == context.DeadlineExceeded {
				writeJSON(w, http.StatusOK, map[string]any{"output": string(out), "exit": 124, "error": "command timed out"})
				return
			}
			exitCode := 0
			if err != nil {
				exitCode = 1
				if ee, ok := err.(*exec.ExitError); ok {
					exitCode = ee.ExitCode()
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"output": string(out), "exit": exitCode})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	addr := fmt.Sprintf("0.0.0.0:%d", listenPort)
	crtPath, keyPath := "/etc/mcontrol-agent/agent.crt", "/etc/mcontrol-agent/agent.key"
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := ensureTLSCert(crtPath, keyPath); err == nil {
		cert, cerr := tls.LoadX509KeyPair(crtPath, keyPath)
		if cerr == nil {
			rawLn, lerr := net.Listen("tcp", addr)
			if lerr != nil {
				os.Exit(1)
			}
			vLn := newVeilTLSListener(rawLn, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
			if err := srv.Serve(vLn); err != nil {
				os.Exit(1)
			}
			return
		}
	}
	if err := srv.ListenAndServe(); err != nil {
		os.Exit(1)
	}
}
