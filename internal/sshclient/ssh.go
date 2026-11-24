package sshclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"errors"
	"net/http"
	"os/exec"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyChanged is returned when a server presents a different host key than the pinned one.
var ErrHostKeyChanged = errors.New("HOST KEY CHANGED: possible man-in-the-middle, or the server was reinstalled (reset the pinned key to trust the new one)")

// Target describes one SSH endpoint.
type Target struct {
	Host     string
	Port     int
	User     string
	Secret   string // password, or an unencrypted PEM private key when AuthType == "key"
	AuthType string // "password" or "key"
	KnownKey   string // pinned SHA256 host-key fingerprint; "" means trust on first use
	SocksProxy string // optional local SOCKS5 proxy e.g. 127.0.0.1:10808
}

type ProbeResult struct {
	Online         bool   `json:"online"`
	CPU            string `json:"cpu"`
	RAM            string `json:"ram"`
	Uptime         string `json:"uptime"`
	IPv4           string `json:"ipv4"`
	IPv6           string `json:"ipv6"`
	NetRx          string `json:"net_rx"`
	NetTx          string `json:"net_tx"`
	InstalledTools string `json:"installed_tools"`
	CoreInstalled  bool   `json:"core_installed"`
	CoreVersion    string `json:"core_version"`
	HostKey        string `json:"host_key"`
	HostKeyChanged bool   `json:"host_key_changed"`
	Error          string `json:"error,omitempty"`
}

type Result struct {
	Output  string
	HostKey string
	Err     error
}

const maxOutput = 256 * 1024

type limitedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *limitedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := maxOutput - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ValidateKey checks that a PEM private key can be parsed (passphrase-protected keys are rejected).
func ValidateKey(pem string) error {
	_, err := ssh.ParsePrivateKey([]byte(pem))
	return err
}

// ShQuote wraps a string in single quotes for safe use inside a POSIX shell command.
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func authMethods(t Target) ([]ssh.AuthMethod, error) {
	if t.AuthType == "key" {
		signer, err := ssh.ParsePrivateKey([]byte(t.Secret))
		if err != nil {
			return nil, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	return []ssh.AuthMethod{
		ssh.Password(t.Secret),
		ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = t.Secret
			}
			return answers, nil
		}),
	}, nil
}

// dial connects with a hard deadline covering TCP connect + SSH handshake and enforces host-key pinning.

func dialViaSOCKS5(proxyAddr, targetAddr string, timeout time.Duration) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %s", portStr)
	}
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil || resp[0] != 0x05 || resp[1] != 0x00 {
		conn.Close()
		return nil, errors.New("socks5 handshake failed")
	}
	if len(host) > 255 {
		conn.Close()
		return nil, errors.New("hostname too long")
	}
	req := make([]byte, 0, 7+len(host))
	req = append(req, 0x05, 0x01, 0x00, 0x03, byte(len(host)))
	req = append(req, host...)
	var pbuf [2]byte
	binary.BigEndian.PutUint16(pbuf[:], uint16(port))
	req = append(req, pbuf[0], pbuf[1])
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 0x05 || hdr[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5 connect failed (rep=%d)", hdr[1])
	}
	var skip int
	switch hdr[3] {
	case 0x01:
		skip = 6
	case 0x04:
		skip = 18
	case 0x03:
		lb := make([]byte, 1)
		if _, err := io.ReadFull(conn, lb); err != nil {
			conn.Close()
			return nil, err
		}
		skip = int(lb[0]) + 2
	}
	if skip > 0 {
		if _, err := io.ReadFull(conn, make([]byte, skip)); err != nil {
			conn.Close()
			return nil, err
		}
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func dial(t Target, timeout time.Duration) (*ssh.Client, string, error) {
	auth, err := authMethods(t)
	if err != nil {
		return nil, "", err
	}
	seen := ""
	cfg := &ssh.ClientConfig{
		User: t.User,
		Auth: auth,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			seen = fp
			if t.KnownKey != "" && t.KnownKey != fp {
				return ErrHostKeyChanged
			}
			return nil
		},
		Timeout: timeout,
	}
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	var conn net.Conn
	if t.SocksProxy != "" {
		conn, err = dialViaSOCKS5(t.SocksProxy, addr, timeout)
	} else {
		conn, err = net.DialTimeout("tcp", addr, timeout)
	}
	if err != nil {
		return nil, "", err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, seen, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sc, chans, reqs), seen, nil
}

// Run executes one command and returns its combined output.
func Run(t Target, cmd string, dialTimeout, runTimeout time.Duration) Result {
	client, fp, err := dial(t, dialTimeout)
	if err != nil {
		return Result{HostKey: fp, Err: err}
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return Result{HostKey: fp, Err: err}
	}
	defer sess.Close()

	out := &limitedBuf{}
	sess.Stdout = out
	sess.Stderr = out

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err = <-done:
	case <-time.After(runTimeout):
		err = errors.New("command timed out")
	}
	return Result{Output: strings.TrimSpace(out.String()), HostKey: fp, Err: err}
}

const probeScript = `
s1=$(head -n1 /proc/stat 2>/dev/null)
n1=$(awk '/:/ && !/lo:|docker|veth/{rx+=$2; tx+=$10} END{printf "%.0f %.0f", rx, tx}' /proc/net/dev 2>/dev/null)
sleep 0.6
s2=$(head -n1 /proc/stat 2>/dev/null)
n2=$(awk '/:/ && !/lo:|docker|veth/{rx+=$2; tx+=$10} END{printf "%.0f %.0f", rx, tx}' /proc/net/dev 2>/dev/null)
cpu=$(printf '%s\n%s\n' "$s1" "$s2" | awk 'NR==1{for(i=2;i<=8;i++)t1+=$i; i1=$5+$6} NR==2{for(i=2;i<=8;i++)t2+=$i; i2=$5+$6; d=t2-t1; if(d>0) printf "%.0f%%", (d-(i2-i1))*100/d}')
[ -z "$cpu" ] && cpu="0%"
ram=$(awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} END{if(t>0) printf "%.0f%%", (t-a)*100/t}' /proc/meminfo 2>/dev/null)
[ -z "$ram" ] && ram="0%"
up=$(awk '{d=int($1/86400); h=int(($1%86400)/3600); m=int(($1%3600)/60); if(d>0) printf "%dd %dh %dm", d, h, m; else if(h>0) printf "%dh %dm", h, m; else printf "%dm", m}' /proc/uptime 2>/dev/null)
[ -z "$up" ] && up="---"
ip4=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')
[ -z "$ip4" ] && ip4="N/A"
ip6=$(ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')
[ -z "$ip6" ] && ip6="N/A"
rx_bps=$(awk -v a="$n1" -v b="$n2" 'BEGIN{split(a,x); split(b,y); d=(y[1]-x[1])/0.6; if(d<0)d=0; if(d>=1048576) printf "%.1f MB/s", d/1048576; else if(d>=1024) printf "%.1f KB/s", d/1024; else printf "%.0f B/s", d}')
tx_bps=$(awk -v a="$n1" -v b="$n2" 'BEGIN{split(a,x); split(b,y); d=(y[2]-x[2])/0.6; if(d<0)d=0; if(d>=1048576) printf "%.1f MB/s", d/1048576; else if(d>=1024) printf "%.1f KB/s", d/1024; else printf "%.0f B/s", d}')
inst=0; ver=""; tools=""
for t in backhaul rathole paqet gost haproxy; do
  if command -v "$t" >/dev/null 2>&1 || [ -x "/usr/bin/$t" ] || [ -x "/usr/local/bin/$t" ] || [ -x "/opt/$t/$t" ]; then
    tools="${tools:+$tools,}$t"
  fi
done
if systemctl is-active --quiet mcontrol-agent.service 2>/dev/null; then
  inst=1
  ver="agent-v2"
fi
printf '%s|%s|%s|%s|%s|%s|%s|%s|%s|%s\n' "$cpu" "$ram" "$up" "$inst" "$ver" "$ip4" "$ip6" "$rx_bps" "$tx_bps" "$tools"
`

func Probe(t Target) ProbeResult {
	res := Run(t, probeScript, 8*time.Second, 20*time.Second)
	if res.Err != nil {
		p := ProbeResult{CPU: "---", RAM: "---", Uptime: "---", HostKey: res.HostKey, Error: res.Err.Error()}
		if strings.Contains(p.Error, "HOST KEY CHANGED") {
			p.HostKeyChanged = true
		}
		return p
	}
	lines := strings.Split(res.Output, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	parts := strings.Split(last, "|")
	if len(parts) < 5 {
		return ProbeResult{Online: true, CPU: "0%", RAM: "0%", Uptime: "---", HostKey: res.HostKey, Error: "unexpected probe output"}
	}
	pr := ProbeResult{
		Online:        true,
		CPU:           parts[0],
		RAM:           parts[1],
		Uptime:        parts[2],
		CoreInstalled: parts[3] == "1",
		CoreVersion:   strings.TrimSpace(parts[4]),
		HostKey:       res.HostKey,
	}
	if len(parts) >= 10 {
		pr.IPv4 = parts[5]
		pr.IPv6 = parts[6]
		pr.NetRx = parts[7]
		pr.NetTx = parts[8]
		pr.InstalledTools = parts[9]
	}
	return pr
}

// InstallCore runs the MTunnel installer on the remote host. All URLs are shell-quoted.
func InstallCore(t Target, source, githubURL, mirrorURL string) Result {
	var cmd string
	if source == "mirror" {
		base := strings.TrimRight(mirrorURL, "/")
		cmd = fmt.Sprintf(`curl -fsSL %s | bash -s -- --remote %s --no-launch`, ShQuote(base+"/install.sh"), ShQuote(base))
	} else {
		base := strings.TrimRight(githubURL, "/")
		cmd = fmt.Sprintf(`curl -fsSL %s | bash -s -- --online --no-launch`, ShQuote(base+"/install.sh"))
	}
	return Run(t, cmd, 10*time.Second, 180*time.Second)
}

// ServiceAction controls the core's systemd service. action must be pre-validated by the caller.
func ServiceAction(t Target, action, service string) Result {
	svc := ShQuote(service)
	var cmd string
	switch action {
	case "logs":
		cmd = "journalctl -u " + svc + " -n 120 --no-pager 2>&1"
	case "status":
		cmd = "systemctl status " + svc + " --no-pager -l 2>&1 | head -n 40"
	default: // start | stop | restart
		cmd = "systemctl " + action + " " + svc + " 2>&1; echo; systemctl is-active " + svc + " 2>&1"
	}
	return Run(t, cmd, 10*time.Second, 40*time.Second)
}

// IsExitError reports whether err means "the command ran but exited non-zero" (as opposed to a connection failure).
func IsExitError(err error) bool {
	var e *ssh.ExitError
	return errors.As(err, &e)
}

type SpeedResult struct {
	PingMs     string `json:"ping_ms"`
	DownloadMB string `json:"download_mbps"`
	UploadMB   string `json:"upload_mbps"`
	Error      string `json:"error,omitempty"`
}

const SpeedtestCmd = `
if command -v speedtest >/dev/null 2>&1 && speedtest --accept-license --accept-gdpr -f csv >/tmp/.mc_spd 2>/dev/null; then
  awk -F',' '{gsub(/"/,""); printf "%.1f|%.1f|%.1f\n", $3, ($6*8)/1000000, ($7*8)/1000000}' /tmp/.mc_spd
  rm -f /tmp/.mc_spd
  exit 0
fi
if command -v speedtest-cli >/dev/null 2>&1; then
  out=$(speedtest-cli --simple 2>/dev/null || true)
  p=$(echo "$out" | awk '/Ping:/{printf "%.1f", $2}')
  d=$(echo "$out" | awk '/Download:/{printf "%.1f", $2}')
  u=$(echo "$out" | awk '/Upload:/{printf "%.1f", $2}')
  if [ -n "$d" ] && [ -n "$u" ]; then
    printf '%s|%s|%s\n' "${p:-N/A}" "$d" "$u"
    exit 0
  fi
fi
ping_ms=$(ping -c 2 -W 2 1.1.1.1 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%.1f", $5; else print "N/A"}')
[ -z "$ping_ms" ] && ping_ms="N/A"
dl_bps=$(curl -4 -s -o /dev/null --max-time 8 -w '%{speed_download}' https://speed.cloudflare.com/__down?bytes=10000000 2>/dev/null || echo 0)
dl_mbps=$(awk -v b="$dl_bps" 'BEGIN{printf "%.1f", (b*8)/1000000}')
ul_bps=$(dd if=/dev/zero bs=1M count=4 2>/dev/null | curl -4 -s -o /dev/null --max-time 8 -X POST --data-binary @- -w '%{speed_upload}' https://speed.cloudflare.com/__up 2>/dev/null || echo 0)
ul_mbps=$(awk -v b="$ul_bps" 'BEGIN{printf "%.1f", (b*8)/1000000}')
printf '%s|%s|%s\n' "$ping_ms" "$dl_mbps" "$ul_mbps"
`

// MeasurePingFromMaster measures latency from the Master server to a target host (ICMP with TCP RTT fallback).
func MeasurePingFromMaster(host string, port int) string {
	out, err := exec.Command("ping", "-c", "1", "-W", "2", host).CombinedOutput()
	if err == nil {
		s := string(out)
		if idx := strings.Index(s, "time="); idx >= 0 {
			rest := s[idx+5:]
			if sp := strings.IndexAny(rest, " \n"); sp > 0 {
				return strings.TrimSpace(rest[:sp]) + " ms"
			}
		}
	}
	if port <= 0 {
		port = 22
	}
	start := time.Now()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		return "N/A"
	}
	c.Close()
	return fmt.Sprintf("%.1f ms", float64(time.Since(start).Microseconds())/1000.0)
}

func ParseSpeedOutput(out string) SpeedResult {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	parts := strings.Split(last, "|")
	if len(parts) < 3 {
		return SpeedResult{PingMs: "N/A", DownloadMB: "0.0", UploadMB: "0.0", Error: "speedtest failed"}
	}
	return SpeedResult{PingMs: parts[0], DownloadMB: parts[1], UploadMB: parts[2]}
}

func RunSpeedtest(t Target) SpeedResult {
	res := Run(t, SpeedtestCmd, 8*time.Second, 25*time.Second)
	if res.Err != nil {
		return SpeedResult{PingMs: "N/A", DownloadMB: "0.0", UploadMB: "0.0", Error: res.Err.Error()}
	}
	return ParseSpeedOutput(res.Output)
}

func peerClient(timeout time.Duration, socksProxy string) *http.Client {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	if socksProxy != "" {
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialViaSOCKS5(socksProxy, addr, timeout)
		}
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

func peerRequest(method, host string, agentPort int, path, peerCode string, body []byte, timeout time.Duration, socksProxy ...string) (*http.Response, error) {
	if agentPort <= 0 {
		agentPort = 2054
	}
	px := ""
	if len(socksProxy) > 0 {
		px = socksProxy[0]
	}
	client := peerClient(timeout, px)
	hostPort := net.JoinHostPort(host, strconv.Itoa(agentPort))
	var lastErr error
	for _, scheme := range []string{"https", "http"} {
		u := fmt.Sprintf("%s://%s%s", scheme, hostPort, path)
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, u, rdr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Peer-Code", peerCode)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func ProbePeer(host string, agentPort int, peerCode string, socksProxy ...string) ProbeResult {
	resp, err := peerRequest("GET", host, agentPort, "/stats", peerCode, nil, 5*time.Second, socksProxy...)
	if err != nil {
		reason := "peer agent unreachable"
		es := strings.ToLower(err.Error())
		if strings.Contains(es, "timeout") || strings.Contains(es, "deadline exceeded") {
			reason = "timeout (check cloud/server firewall)"
		} else if strings.Contains(es, "connection refused") {
			reason = "connection refused"
		}
		return ProbeResult{Online: false, CPU: "---", RAM: "---", Uptime: "---", Error: reason}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ProbeResult{Online: false, CPU: "---", RAM: "---", Uptime: "---", Error: fmt.Sprintf("agent HTTP %d", resp.StatusCode)}
	}
	var pr ProbeResult
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return ProbeResult{Online: false, CPU: "---", RAM: "---", Uptime: "---", Error: "invalid agent JSON"}
	}
	pr.Online = true
	return pr
}

func RunPeerSpeedtest(host string, agentPort int, peerCode string) SpeedResult {
	resp, err := peerRequest("GET", host, agentPort, "/speedtest", peerCode, nil, 30*time.Second)
	if err != nil {
		return SpeedResult{Error: "peer agent unreachable"}
	}
	defer resp.Body.Close()
	var sr SpeedResult
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return SpeedResult{Error: "invalid speedtest response"}
	}
	return sr
}

func PeerExec(host string, agentPort int, peerCode, command string, timeout time.Duration, socksProxy ...string) Result {
	payload, _ := json.Marshal(map[string]any{"command": command, "timeout": int(timeout.Seconds())})
	resp, err := peerRequest("POST", host, agentPort, "/exec", peerCode, payload, timeout+5*time.Second, socksProxy...)
	if err != nil {
		return Result{Err: fmt.Errorf("peer exec failed: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{Err: fmt.Errorf("peer agent returned HTTP %d (update agent to v2)", resp.StatusCode)}
	}
	var out struct {
		Output string `json:"output"`
		Exit   int    `json:"exit"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result{Err: errors.New("invalid agent exec response")}
	}
	if out.Error != "" {
		return Result{Output: out.Output, Err: errors.New(out.Error)}
	}
	if out.Exit != 0 {
		return Result{Output: out.Output, Err: fmt.Errorf("exit status %d", out.Exit)}
	}
	return Result{Output: strings.TrimSpace(out.Output)}
}

func PeerAgentInstallScript(peerCode string, agentPort int) string {
	return PeerAgentInstallScriptOpts(peerCode, agentPort, "", false, false)
}

func PeerAgentInstallScriptWithSource(peerCode string, agentPort int, mirrorURL string, isIran bool) string {
	return PeerAgentInstallScriptOpts(peerCode, agentPort, mirrorURL, isIran, false)
}

// PeerAgentInstallScriptOpts installs a per-port systemd service (mcontrol-agent-<port>.service)
// so multiple MControl panels can connect to the same server on different ports simultaneously.
// When skipIfPortBusy is true, any port already actively listening (including another panel's agent)
// is left untouched and exits 42 with PORT_BUSY:<process> so the caller tries the next port.
func PeerAgentInstallScriptOpts(peerCode string, agentPort int, mirrorURL string, isIran bool, skipIfPortBusy bool) string {
	return PeerAgentInstallScriptFull(peerCode, "", agentPort, mirrorURL, isIran, skipIfPortBusy)
}

// PeerAgentInstallScriptFull is the multi-panel installer. The agent on a given port accepts
// several peer codes (the unit's MC_PEER + /etc/mcontrol-agent/peers-<port>.txt), so a second
// panel pairing with the same server ADDS its code and never locks the first panel out.
// dropCode (optional) is this panel's previous code that must be removed (key rotation).
func PeerAgentInstallScriptFull(peerCode, dropCode string, agentPort int, mirrorURL string, isIran bool, skipIfPortBusy bool) string {
	if agentPort <= 0 {
		agentPort = 2054
	}
	svcName := fmt.Sprintf("mcontrol-agent-%d.service", agentPort)
	mirrorClean := strings.TrimRight(strings.TrimSpace(mirrorURL), "/")
	skipFlag := "0"
	if skipIfPortBusy {
		skipFlag = "1"
	}
	prepDeps := `
if ! command -v python3 >/dev/null 2>&1 || ! command -v openssl >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3 openssl >/dev/null 2>&1 || true
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y -q python3 openssl >/dev/null 2>&1 || true
  elif command -v yum >/dev/null 2>&1; then
    yum install -y -q python3 openssl >/dev/null 2>&1 || true
  fi
fi
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi active; then
  ufw allow %d/tcp >/dev/null 2>&1 || true
elif command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld 2>/dev/null; then
  firewall-cmd --permanent --add-port=%d/tcp >/dev/null 2>&1 || true
  firewall-cmd --reload >/dev/null 2>&1 || true
elif command -v iptables >/dev/null 2>&1; then
  iptables -C INPUT -p tcp --dport %d -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport %d -j ACCEPT 2>/dev/null || true
fi

AGENT_PORT=%d
SVC_NAME="%s"
SKIP_BUSY=%s

get_port_pids() {
  local p="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -tlnp 2>/dev/null | awk -v pt=":${p}" '$4 ~ pt"$" {print}' | grep -oE 'pid=[0-9]+' | cut -d= -f2 | sort -u
  elif command -v lsof >/dev/null 2>&1; then
    lsof -t -iTCP:"$p" -sTCP:LISTEN 2>/dev/null | sort -u
  elif command -v fuser >/dev/null 2>&1; then
    fuser "${p}/tcp" 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' | sort -u
  fi
}

BUSY_PIDS=$(get_port_pids "$AGENT_PORT" || true)
if [ -n "$BUSY_PIDS" ]; then
  if [ "$SKIP_BUSY" = "1" ]; then
    for pid in $BUSY_PIDS; do
      [ -d "/proc/$pid" ] || continue
      comm=$(cat "/proc/$pid/comm" 2>/dev/null || true)
      echo "PORT_BUSY:${comm:-unknown}(pid=${pid})"
      exit 42
    done
  else
    systemctl stop "$SVC_NAME" 2>/dev/null || true
    [ "$AGENT_PORT" = "2054" ] && systemctl stop mcontrol-agent.service 2>/dev/null || true
    sleep 0.3
    BUSY_PIDS=$(get_port_pids "$AGENT_PORT" || true)
    for pid in $BUSY_PIDS; do
      [ -d "/proc/$pid" ] || continue
      [ "$pid" = "$$" ] || [ "$pid" = "$PPID" ] && continue
      cmdline=$(tr '\000' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)
      comm=$(cat "/proc/$pid/comm" 2>/dev/null || true)
      if echo "$cmdline $comm" | grep -qiE 'mcontrol-agent|mcontrol_agent'; then
        kill -TERM "$pid" 2>/dev/null || true
        sleep 0.4
        kill -9 "$pid" 2>/dev/null || true
      else
        echo "PORT_BUSY:${comm:-unknown}(pid=${pid})"
        exit 42
      fi
    done
    sleep 0.3
  fi
fi`
	prepRendered := fmt.Sprintf(prepDeps, agentPort, agentPort, agentPort, agentPort, agentPort, svcName, skipFlag)
	mirrorFetch := ""
	if isIran && mirrorClean != "" {
		mirrorFetch = fmt.Sprintf(`
if command -v curl >/dev/null 2>&1; then
  if curl -fsSL --connect-timeout 6 --max-time 20 %s -o /usr/local/bin/mcontrol-agent.py.dl 2>/dev/null && grep -q "AGENT_VER" /usr/local/bin/mcontrol-agent.py.dl 2>/dev/null && grep -q "PEERS_FILE" /usr/local/bin/mcontrol-agent.py.dl 2>/dev/null; then
    mv -f /usr/local/bin/mcontrol-agent.py.dl /usr/local/bin/mcontrol-agent.py
    chmod 755 /usr/local/bin/mcontrol-agent.py
    echo "AGENT_SOURCE=iran_mirror"
  else
    rm -f /usr/local/bin/mcontrol-agent.py.dl 2>/dev/null || true
    echo "AGENT_SOURCE=iran_mirror_embedded"
  fi
fi`, ShQuote(mirrorClean+"/beta-latest/mcontrol-agent.py"))
	}
	peersBlock := fmt.Sprintf(`
NEW_CODE=%s
DROP_CODE=%s
PEERS_FILE="/etc/mcontrol-agent/peers-${AGENT_PORT}.txt"
touch "$PEERS_FILE"; chmod 600 "$PEERS_FILE" 2>/dev/null || true
UNITS="/etc/systemd/system/${SVC_NAME}"
[ "$AGENT_PORT" = "2054" ] && UNITS="$UNITS /etc/systemd/system/mcontrol-agent.service"
for U in $UNITS; do
  [ -f "$U" ] || continue
  OLD=$(grep -oE 'MC_PEER=[A-Za-z0-9_-]+' "$U" 2>/dev/null | head -1 | cut -d= -f2)
  if [ -n "$OLD" ] && [ "$OLD" != "$DROP_CODE" ] && [ "$OLD" != "$NEW_CODE" ]; then
    grep -qxF "$OLD" "$PEERS_FILE" 2>/dev/null || echo "$OLD" >> "$PEERS_FILE"
  fi
done
if [ -n "$DROP_CODE" ]; then
  grep -vxF "$DROP_CODE" "$PEERS_FILE" > "$PEERS_FILE.tmp" 2>/dev/null || true
  mv -f "$PEERS_FILE.tmp" "$PEERS_FILE"
fi
grep -vxF "$NEW_CODE" "$PEERS_FILE" > "$PEERS_FILE.tmp" 2>/dev/null || true
mv -f "$PEERS_FILE.tmp" "$PEERS_FILE"
if [ "$AGENT_PORT" = "2054" ]; then systemctl disable mcontrol-agent.service >/dev/null 2>&1 || true; fi
`, ShQuote(peerCode), ShQuote(dropCode))
	return prepRendered + "\n" + fmt.Sprintf(`mkdir -p /etc/mcontrol-agent
`+peersBlock+`
if command -v openssl >/dev/null 2>&1; then
  if [ ! -f /etc/mcontrol-agent/agent.crt ] || [ ! -f /etc/mcontrol-agent/agent.key ]; then
    openssl req -x509 -newkey rsa:2048 -keyout /etc/mcontrol-agent/agent.key -out /etc/mcontrol-agent/agent.crt -days 3650 -nodes -subj "/CN=mcontrol-agent" 2>/dev/null || true
    chmod 600 /etc/mcontrol-agent/agent.key 2>/dev/null || true
  fi
fi
cat <<'PYEOF' > /usr/local/bin/mcontrol-agent.py
#!/usr/bin/env python3
import http.server, json, os, ssl, subprocess, time

PEER = os.environ.get("MC_PEER", "")
PORT = int(os.environ.get("MC_PORT", "2054"))
AGENT_VER = "v3"
PEERS_FILE = "/etc/mcontrol-agent/peers-" + str(PORT) + ".txt"

def peer_ok(code):
    # v3: several panels can share one agent. The unit's MC_PEER plus every line of PEERS_FILE is accepted.
    if not code: return False
    if PEER and code == PEER: return True
    try:
        with open(PEERS_FILE) as f:
            for ln in f:
                if ln.strip() == code: return True
    except Exception: pass
    return False

def read_net_totals():
    rx = tx = 0
    try:
        with open("/proc/net/dev") as f:
            for line in f:
                if ":" not in line: continue
                iface, rest = line.split(":", 1)
                iface = iface.strip()
                if iface == "lo" or iface.startswith("docker") or iface.startswith("veth"): continue
                p = rest.split()
                if len(p) >= 10:
                    rx += int(p[0]); tx += int(p[8])
    except Exception: pass
    return rx, tx

def fmt_bps(d):
    if d >= 1048576: return f"{d/1048576:.1f} MB/s"
    if d >= 1024: return f"{d/1024:.1f} KB/s"
    return f"{int(d)} B/s"

def read_stats():
    try:
        with open("/proc/stat") as f: a = [int(x) for x in f.readline().split()[1:]]
        rx1, tx1 = read_net_totals()
        time.sleep(0.35)
        with open("/proc/stat") as f: b = [int(x) for x in f.readline().split()[1:]]
        rx2, tx2 = read_net_totals()
        dt = sum(b) - sum(a)
        di = (b[3] + (b[4] if len(b)>4 else 0)) - (a[3] + (a[4] if len(a)>4 else 0))
        cpu = f"{max(0, min(100, int((dt - di) * 100 / dt)))}%%" if dt > 0 else "0%%"
        net_rx = fmt_bps(max(0, rx2 - rx1) / 0.35)
        net_tx = fmt_bps(max(0, tx2 - tx1) / 0.35)
    except Exception:
        cpu, net_rx, net_tx = "0%%", "0 B/s", "0 B/s"
    try:
        m = {}
        with open("/proc/meminfo") as f:
            for line in f:
                p = line.split()
                if len(p) >= 2: m[p[0]] = int(p[1])
        tot = m.get("MemTotal:", 1)
        avail = m.get("MemAvailable:", 0)
        ram = f"{int((tot - avail) * 100 / tot)}%%"
    except Exception: ram = "0%%"
    try:
        with open("/proc/uptime") as f: sec = int(float(f.read().split()[0]))
        d, h, mm = sec // 86400, (sec %% 86400) // 3600, (sec %% 3600) // 60
        up = f"{d}d {h}h {mm}m" if d else f"{h}h {mm}m"
    except Exception: up = "---"
    def cmd_out(c):
        try: return subprocess.check_output(c, shell=True, timeout=2).decode().strip()
        except Exception: return ""
    ip4 = cmd_out("ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i==\"src\") {print $(i+1); exit}}'") or "N/A"
    ip6 = cmd_out("ip -6 route get 2606:4700:4700::1111 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i==\"src\") {print $(i+1); exit}}'") or "N/A"
    return {"online": True, "cpu": cpu, "ram": ram, "uptime": up, "ipv4": ip4, "ipv6": ip6, "net_rx": net_rx, "net_tx": net_tx, "core_installed": True, "core_version": "agent-" + AGENT_VER}

def run_speed():
    c = """
if command -v speedtest >/dev/null 2>&1 && speedtest --accept-license --accept-gdpr -f csv >/tmp/.mc_spd 2>/dev/null; then
  awk -F',' '{gsub(/"/,""); printf "%%.1f|%%.1f|%%.1f\\n", $3, ($6*8)/1000000, ($7*8)/1000000}' /tmp/.mc_spd
  rm -f /tmp/.mc_spd
  exit 0
fi
if command -v speedtest-cli >/dev/null 2>&1; then
  out=$(speedtest-cli --simple 2>/dev/null || true)
  p=$(echo "$out" | awk '/Ping:/{printf "%%.1f", $2}')
  d=$(echo "$out" | awk '/Download:/{printf "%%.1f", $2}')
  u=$(echo "$out" | awk '/Upload:/{printf "%%.1f", $2}')
  if [ -n "$d" ] && [ -n "$u" ]; then printf '%%s|%%s|%%s\\n' "${p:-N/A}" "$d" "$u"; exit 0; fi
fi
ping_ms=$(ping -c 2 -W 2 1.1.1.1 2>/dev/null | awk -F'/' 'END{if(NF>=5) printf "%%.1f", $5; else print "N/A"}')
[ -z "$ping_ms" ] && ping_ms="N/A"
dl_bps=$(curl -4 -s -o /dev/null --max-time 8 -w '%%{speed_download}' https://speed.cloudflare.com/__down?bytes=10000000 2>/dev/null || echo 0)
dl_mbps=$(awk -v b="$dl_bps" 'BEGIN{printf "%%.1f", (b*8)/1000000}')
ul_bps=$(dd if=/dev/zero bs=1M count=4 2>/dev/null | curl -4 -s -o /dev/null --max-time 8 -X POST --data-binary @- -w '%%{speed_upload}' https://speed.cloudflare.com/__up 2>/dev/null || echo 0)
ul_mbps=$(awk -v b="$ul_bps" 'BEGIN{printf "%%.1f", (b*8)/1000000}')
printf '%%s|%%s|%%s' "$ping_ms" "$dl_mbps" "$ul_mbps"
"""
    try:
        out = subprocess.check_output(["bash", "-c", c], timeout=26).decode().strip().splitlines()[-1]
        p = out.split("|")
        return {"ping_ms": p[0], "download_mbps": p[1], "upload_mbps": p[2]} if len(p) >= 3 else {"error": "failed"}
    except Exception as e:
        return {"error": str(e)}

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def _send_json(self, code, data):
        body = json.dumps(data).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):
        if not peer_ok(self.headers.get("X-Peer-Code", "")):
            self.send_response(403); self.end_headers(); return
        data = run_speed() if self.path.startswith("/speedtest") else read_stats()
        self._send_json(200, data)
    def do_POST(self):
        if not peer_ok(self.headers.get("X-Peer-Code", "")):
            self.send_response(403); self.end_headers(); return
        if not self.path.startswith("/exec"):
            self.send_response(404); self.end_headers(); return
        ln = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(min(ln, 65536))
        try:
            req = json.loads(raw.decode() or "{}")
            cmd = str(req.get("command", "")).strip()
            tout = min(max(int(req.get("timeout", 30)), 5), 180)
        except Exception:
            self._send_json(400, {"error": "bad json"}); return
        if not cmd:
            self._send_json(400, {"error": "empty command"}); return
        try:
            proc = subprocess.run(["bash", "-c", cmd], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=tout)
            self._send_json(200, {"output": proc.stdout.decode(errors="replace")[:200000], "exit": proc.returncode})
        except subprocess.TimeoutExpired:
            self._send_json(200, {"output": "", "exit": 124, "error": "command timed out"})
        except Exception as e:
            self._send_json(200, {"output": "", "exit": 1, "error": str(e)})

class ReusableThreadingHTTPServer(http.server.ThreadingHTTPServer):
    allow_reuse_address = True
srv = ReusableThreadingHTTPServer(("0.0.0.0", PORT), H)
crt, key = "/etc/mcontrol-agent/agent.crt", "/etc/mcontrol-agent/agent.key"
if os.path.exists(crt) and os.path.exists(key):
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(crt, key)
    srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
srv.serve_forever()
PYEOF
chmod 755 /usr/local/bin/mcontrol-agent.py
%s
cat <<SVCEOF > /etc/systemd/system/%s
[Unit]
Description=MControl Peer Agent v2 (TLS · Port %d)
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=0

[Service]
Type=simple
Environment="MC_PEER=%s"
Environment="MC_PORT=%d"
ExecStartPre=-/bin/bash -c 'P=${MC_PORT:-2054}; if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi active; then ufw allow ${P}/tcp >/dev/null 2>&1 || true; elif command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld 2>/dev/null; then firewall-cmd --add-port=${P}/tcp >/dev/null 2>&1 || true; elif command -v iptables >/dev/null 2>&1; then iptables -C INPUT -p tcp --dport ${P} -j ACCEPT 2>/dev/null || iptables -I INPUT -p tcp --dport ${P} -j ACCEPT 2>/dev/null || true; fi'
ExecStart=__PYBIN__ /usr/local/bin/mcontrol-agent.py
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
SVCEOF
PYBIN=$(command -v python3 || echo /usr/bin/python3)
sed -i "s|__PYBIN__|${PYBIN}|g" "/etc/systemd/system/%s"
systemctl daemon-reload
systemctl enable "%s"
systemctl restart "%s"
for _i in 1 2 3 4 5 6; do
  if systemctl is-active --quiet "%s" && ss -tln 2>/dev/null | grep -q ":${AGENT_PORT} "; then
    break
  fi
  sleep 0.5
done
systemctl is-active "%s"
`, mirrorFetch, svcName, agentPort, peerCode, agentPort, svcName, svcName, svcName, svcName, svcName)
}

func InstallPeerAgent(t Target, peerCode string, agentPort int) Result {
	return Run(t, PeerAgentInstallScript(peerCode, agentPort), 10*time.Second, 45*time.Second)
}

func InstallPeerAgentFromSource(t Target, peerCode string, agentPort int, mirrorURL string, isIran bool) Result {
	return Run(t, PeerAgentInstallScriptOpts(peerCode, agentPort, mirrorURL, isIran, false), 10*time.Second, 45*time.Second)
}

func InstallPeerAgentPreserveBusy(t Target, peerCode string, agentPort int, mirrorURL string, isIran bool, skipIfPortBusy bool) Result {
	return Run(t, PeerAgentInstallScriptOpts(peerCode, agentPort, mirrorURL, isIran, skipIfPortBusy), 10*time.Second, 45*time.Second)
}
