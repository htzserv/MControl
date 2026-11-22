package api

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/geo"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/sysinfo"
	"github.com/htzserv/MControl/internal/updater"
)

const tokenTTL = 12 * time.Hour

var (
	validWebPathRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,48}$`)
	validURLRe     = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/@%+-]{1,300}$`)
	validServiceRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)
	validHostRe    = regexp.MustCompile(`^[A-Za-z0-9.:_-]{1,253}$`)
	validUserRe    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

type Handler struct {
	store       *db.Store
	currentPort string
	limiter     *limiter
	setupToken  string
	distMu      sync.Mutex
	distTokens  map[string]time.Time
}

func New(store *db.Store, currentPort string) *Handler {
	h := &Handler{store: store, currentPort: currentPort, limiter: newLimiter()}
	if !store.IsSetupDone() {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		h.setupToken = hex.EncodeToString(b)
		log.Printf("FIRST-TIME SETUP TOKEN: %s", h.setupToken)
	}
	return h
}

func randomPath() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "mc_" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// Login throttling
// ---------------------------------------------------------------------------

type attempt struct {
	fails int
	until time.Time
	seen  time.Time
}

type limiter struct {
	mu sync.Mutex
	m  map[string]*attempt
}

func newLimiter() *limiter {
	return &limiter{m: map[string]*attempt{}}
}

func (l *limiter) wait(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[ip]
	if a == nil {
		return 0
	}
	if d := time.Until(a.until); d > 0 {
		return d
	}
	return 0
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > 5000 {
		for k, v := range l.m {
			if time.Since(v.seen) > time.Hour {
				delete(l.m, k)
			}
		}
	}
	a := l.m[ip]
	if a == nil {
		a = &attempt{}
		l.m[ip] = a
	}
	a.fails++
	if a.fails > 30 {
		a.fails = 30
	}
	a.seen = time.Now()
	if a.fails >= 5 {
		d := (30 * time.Second) << uint(a.fails-5)
		if d <= 0 || d > 15*time.Minute {
			d = 15 * time.Minute
		}
		a.until = time.Now().Add(d)
	}
}

func (l *limiter) ok(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------------------------------------------------------------------
// Session tokens: HMAC-signed, expiring, bound to the current password
// ---------------------------------------------------------------------------

type claims struct {
	U string `json:"u"`
	E int64  `json:"e"`
	T string `json:"t"`
}

func (h *Handler) sign(payload string) string {
	mac := hmac.New(sha256.New, h.store.SignKey())
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Handler) makeToken(username string) string {
	tag, _ := h.store.PasswordTag(username)
	raw, _ := json.Marshal(claims{U: username, E: time.Now().Add(tokenTTL).Unix(), T: tag})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + h.sign(payload)
}

func (h *Handler) verifyToken(token string) (string, bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	if !hmac.Equal([]byte(parts[1]), []byte(h.sign(parts[0]))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	var c claims
	if json.Unmarshal(raw, &c) != nil {
		return "", false
	}
	if c.E < time.Now().Unix() {
		return "", false
	}
	tag, ok := h.store.PasswordTag(c.U)
	if !ok || subtle.ConstantTimeCompare([]byte(tag), []byte(c.T)) != 1 {
		return "", false
	}
	return c.U, true
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHdr, "Bearer ")
		user, ok := h.verifyToken(token)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "unauthorized"})
			return
		}
		r.Header.Set("X-MC-User", user)
		next(w, r)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func targetOf(srv *db.Server) sshclient.Target {
	return sshclient.Target{
		Host:     srv.Host,
		Port:     srv.Port,
		User:     srv.Username,
		Secret:   srv.Password,
		AuthType: srv.AuthType,
		KnownKey: srv.HostKey,
	}
}

// applyProbe stores the probe outcome and pins the host key on first successful contact.
func (h *Handler) applyProbe(srv *db.Server, p sshclient.ProbeResult) {
	status := "offline"
	if p.Online {
		status = "online"
	}
	pingMs := sshclient.MeasurePingFromMaster(srv.Host, srv.Port)
	_ = h.store.UpdateServerRuntime(srv.ID, status, p.CPU, p.RAM, p.Uptime, p.IPv4, p.IPv6, p.NetRx, p.NetTx, p.InstalledTools, pingMs, p.CoreInstalled, p.CoreVersion, p.Error)
	if srv.HostKey == "" && p.HostKey != "" && p.Online {
		_ = h.store.SetHostKey(srv.ID, p.HostKey)
	}
}

func (h *Handler) probeServer(id int64) (*sshclient.ProbeResult, error) {
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		return nil, err
	}
	var p sshclient.ProbeResult
	if srv.AuthMode == "peer" && srv.PeerCode != "" {
		p = sshclient.ProbePeer(srv.Host, srv.AgentPort, srv.PeerCode)
	} else {
		p = sshclient.Probe(targetOf(srv))
	}
	h.applyProbe(srv, p)
	return &p, nil
}

func (h *Handler) probeAll() {
	list, err := h.store.ListServers()
	if err != nil {
		return
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, item := range list {
		wg.Add(1)
		sem <- struct{}{}
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			_, _ = h.probeServer(id)
		}(item.ID)
	}
	wg.Wait()
}

// StartAutoProbe refreshes every server on the interval chosen in Settings (0 = disabled).
func (h *Handler) StartAutoProbe() {
	go func() {
		var last time.Time
		for {
			time.Sleep(30 * time.Second)
			iv := h.store.GetSettings().ProbeInterval
			if iv <= 0 || time.Since(last) < time.Duration(iv)*time.Minute {
				continue
			}
			last = time.Now()
			h.probeAll()
		}
	}()
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", h.handleSetupStatus)
	mux.HandleFunc("POST /api/setup/complete", h.handleSetupComplete)
	mux.HandleFunc("POST /api/login", h.handleLogin)
	mux.HandleFunc("POST /api/logout", h.auth(h.handleLogout))
	mux.HandleFunc("GET /api/local/stats", h.auth(h.handleLocalStats))
	mux.HandleFunc("GET /api/local/logs", h.auth(h.handleLocalLogs))
	mux.HandleFunc("POST /api/local/speedtest", h.auth(h.handleLocalSpeedtest))
	mux.HandleFunc("POST /api/local/tweak", h.auth(h.handleLocalTweak))
	mux.HandleFunc("GET /api/backup/db", h.auth(h.handleBackupDB))
	mux.HandleFunc("POST /api/backup/restore", h.auth(h.handleRestoreDB))
	mux.HandleFunc("GET /api/servers", h.auth(h.handleListServers))
	mux.HandleFunc("POST /api/servers", h.auth(h.handleAddServer))
	mux.HandleFunc("POST /api/servers/test", h.auth(h.handleTestServer))
	mux.HandleFunc("PUT /api/servers/{id}", h.auth(h.handleUpdateServer))
	mux.HandleFunc("DELETE /api/servers/{id}", h.auth(h.handleDeleteServer))
	mux.HandleFunc("POST /api/servers/{id}/probe", h.auth(h.handleProbeServer))
	mux.HandleFunc("POST /api/servers/probe-all", h.auth(h.handleProbeAll))
	mux.HandleFunc("POST /api/servers/{id}/install", h.auth(h.handleInstallCore))
	mux.HandleFunc("POST /api/servers/{id}/pair", h.auth(h.handlePairPeer))
	mux.HandleFunc("POST /api/servers/{id}/unpair", h.auth(h.handleUnpairPeer))
	mux.HandleFunc("POST /api/servers/{id}/agent/update", h.auth(h.handleUpdatePeerAgent))
	mux.HandleFunc("POST /api/servers/{id}/speedtest", h.auth(h.handleServerSpeedtest))
	mux.HandleFunc("POST /api/servers/{id}/service", h.auth(h.handleService))
	mux.HandleFunc("POST /api/servers/{id}/exec", h.auth(h.handleExec))
	mux.HandleFunc("POST /api/servers/{id}/hostkey/reset", h.auth(h.handleResetHostKey))
	mux.HandleFunc("GET /api/settings", h.auth(h.handleGetSettings))
	mux.HandleFunc("POST /api/settings", h.auth(h.handleSaveSettings))
	mux.HandleFunc("POST /api/password", h.auth(h.handleChangePassword))
	mux.HandleFunc("POST /api/settings/address", h.auth(h.handleSaveAddress))
	mux.HandleFunc("POST /api/settings/security", h.auth(h.handleSaveSecurity))
	mux.HandleFunc("POST /api/settings/defaults", h.auth(h.handleSaveDefaults))
	mux.HandleFunc("GET /api/profiles/ssh", h.auth(h.handleListSSHProfiles))
	mux.HandleFunc("POST /api/profiles/ssh", h.auth(h.handleAddSSHProfile))
	mux.HandleFunc("DELETE /api/profiles/ssh/{id}", h.auth(h.handleDeleteSSHProfile))
	mux.HandleFunc("GET /api/profiles/ports", h.auth(h.handleListPortProfiles))
	mux.HandleFunc("POST /api/profiles/ports", h.auth(h.handleAddPortProfile))
	mux.HandleFunc("PUT /api/profiles/ssh/{id}", h.auth(h.handleUpdateSSHProfile))
	mux.HandleFunc("PUT /api/profiles/ports/{id}", h.auth(h.handleUpdatePortProfile))
	mux.HandleFunc("DELETE /api/profiles/ports/{id}", h.auth(h.handleDeletePortProfile))
	mux.HandleFunc("GET /api/settings/tls", h.auth(h.handleGetTLS))
	mux.HandleFunc("POST /api/settings/tls", h.auth(h.handleSaveTLS))
	mux.HandleFunc("POST /api/settings/tls/acme", h.auth(h.handleIssueACME))
	mux.HandleFunc("DELETE /api/settings/tls", h.auth(h.handleDeleteTLS))
	mux.HandleFunc("POST /api/settings/panel-name", h.auth(h.handleSavePanelName))
	mux.HandleFunc("POST /api/servers/{id}/tweak", h.auth(h.handleServerTweak))
	mux.HandleFunc("GET /api/servers/{id}/setup/status", h.auth(h.handleServerSetupStatus))
	mux.HandleFunc("POST /api/servers/{id}/setup/run", h.auth(h.handleServerSetupRun))
	mux.HandleFunc("GET /api/cache/list", h.auth(h.handleCacheList))
	mux.HandleFunc("POST /api/cache/upload", h.auth(h.handleCacheUpload))
	mux.HandleFunc("POST /api/cache/delete", h.auth(h.handleCacheDelete))
	mux.HandleFunc("GET /api/outbound/status", h.auth(h.handleOutboundStatus))
	mux.HandleFunc("POST /api/outbound/save", h.auth(h.handleOutboundSave))
	mux.HandleFunc("POST /api/outbound/stop", h.auth(h.handleOutboundStop))
	mux.HandleFunc("POST /api/outbound/test", h.auth(h.handleOutboundTest))
	mux.HandleFunc("GET /api/tunnels", h.auth(h.handleListTunnels))
	mux.HandleFunc("POST /api/tunnels", h.auth(h.handleCreateTunnel))
	mux.HandleFunc("POST /api/tunnels/{id}/action", h.auth(h.handleTunnelAction))
	mux.HandleFunc("POST /api/tunnels/{id}/speedtest", h.auth(h.handleTunnelSpeedtest))
	mux.HandleFunc("DELETE /api/tunnels/{id}", h.auth(h.handleDeleteTunnel))
	mux.HandleFunc("POST /api/tunnels/linktest", h.auth(h.handleLinkTest))
	mux.HandleFunc("POST /api/tunnels/ping8", h.auth(h.handleTunnelPing8))
	mux.HandleFunc("POST /api/tunnels/benchmark", h.auth(h.handleBenchmarkStart))
	mux.HandleFunc("GET /api/tunnels/benchmark/status", h.auth(h.handleBenchmarkStatus))
	mux.HandleFunc("GET /api/tunnels/benchmark/history", h.auth(h.handleBenchmarkHistory))
	mux.HandleFunc("GET /api/update/check", h.auth(h.handleUpdateCheck))
	mux.HandleFunc("POST /api/update/remote", h.auth(h.handleUpdateRemote))
	mux.HandleFunc("POST /api/update/upload", h.auth(h.handleUpdateUpload))
	mux.HandleFunc("GET /api/update/status", h.auth(h.handleUpdateStatus))
}

// ---------------------------------------------------------------------------
// Setup & login
// ---------------------------------------------------------------------------

func (h *Handler) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	done := h.store.IsSetupDone()
	resp := map[string]any{
		"ok":         true,
		"setup_done": done,
	}
	if !done {
		resp["suggested_path"] = randomPath()
		resp["current_port"] = h.currentPort
		resp["token_required"] = h.setupToken != ""
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	if h.store.IsSetupDone() {
		fail(w, http.StatusForbidden, "setup already completed")
		return
	}
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		WebPath    string `json:"web_path"`
		Port       int    `json:"port"`
		SetupToken string `json:"setup_token"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := clientIP(r)
	if d := h.limiter.wait(ip); d > 0 {
		fail(w, http.StatusTooManyRequests, "too many attempts, retry later")
		return
	}
	if h.setupToken == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(req.SetupToken)), []byte(h.setupToken)) != 1 {
		h.limiter.fail(ip)
		fail(w, http.StatusForbidden, "invalid setup token (shown in the install output / service log)")
		return
	}
	req.WebPath = strings.Trim(strings.TrimSpace(req.WebPath), "/")
	if req.WebPath == "" {
		req.WebPath = randomPath()
	}
	if !validWebPathRe.MatchString(req.WebPath) {
		fail(w, http.StatusBadRequest, "web path must be 3-48 chars (letters, numbers, _ or -)")
		return
	}
	if req.Port <= 0 {
		p, _ := strconv.Atoi(h.currentPort)
		if p <= 0 {
			p = 2053
		}
		req.Port = p
	}
	req.Username = strings.TrimSpace(req.Username)
	if err := h.store.CompleteSetup(req.Username, req.Password, req.WebPath, req.Port); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	h.limiter.ok(ip)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"web_path": req.WebPath,
		"port":     req.Port,
		"token":    h.makeToken(req.Username),
	})

	// Exit cleanly so systemd (Restart=always) immediately relaunches MControl on the new port & web_path.
	go func() {
		time.Sleep(1200 * time.Millisecond)
		os.Exit(0)
	}()
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !h.store.IsSetupDone() {
		fail(w, http.StatusForbidden, "initial setup required")
		return
	}
	ip := clientIP(r)
	if d := h.limiter.wait(ip); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
		fail(w, http.StatusTooManyRequests, "too many failed attempts, retry in "+d.Round(time.Second).String())
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totp_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !h.store.VerifyUser(req.Username, req.Password) {
		h.limiter.fail(ip)
		time.Sleep(400 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	st := h.store.GetSettings()
	if st.TwoFAEnabled && st.TwoFASecret != "" {
		code := strings.TrimSpace(req.TOTPCode)
		if code == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "require_2fa": true, "error": "2FA code required"})
			return
		}
		if !verifyTOTP(st.TwoFASecret, code) {
			h.limiter.fail(ip)
			time.Sleep(400 * time.Millisecond)
			writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "require_2fa": true, "error": "invalid 2FA code"})
			return
		}
	}
	h.limiter.ok(ip)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": h.makeToken(req.Username), "expires_in": int(tokenTTL.Seconds())})
}

// ---------------------------------------------------------------------------
// Servers
// ---------------------------------------------------------------------------

type serverReq struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"auth_type"`
	Secret       string `json:"secret"`
	SSHProfileID int64  `json:"ssh_profile_id"`
}

// normalize validates and cleans a server request, returning an error message ("" when fine).
func (req *serverReq) normalize() string {
	req.Host = strings.TrimSpace(req.Host)
	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimSpace(req.Username)
	if req.Host == "" || !validHostRe.MatchString(req.Host) {
		return "a valid host (IP or domain) is required"
	}
	if req.Name == "" {
		req.Name = req.Host
	}
	if len(req.Name) > 64 {
		return "server name is too long (max 64)"
	}
	if req.Port <= 0 {
		req.Port = 22
	}
	if req.Port > 65535 {
		return "invalid SSH port"
	}
	if req.Username == "" {
		req.Username = "root"
	}
	if !validUserRe.MatchString(req.Username) {
		return "invalid SSH username"
	}
	if req.Role != "iran" && req.Role != "foreign" {
		req.Role = "foreign"
	}
	if req.AuthType != "key" {
		req.AuthType = "password"
	}
	if len(req.Secret) > 16*1024 {
		return "secret is too long"
	}
	return ""
}

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListServers()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": list})
}

func (h *Handler) handleAddServer(w http.ResponseWriter, r *http.Request) {
	var req serverReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.SSHProfileID > 0 {
		if prof, err := h.store.GetSSHProfileWithSecret(req.SSHProfileID); err == nil {
			req.Username = prof.Username
			req.Port = prof.Port
			req.AuthType = "password"
			req.Secret = prof.Password
		}
	}

	if msg := req.normalize(); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	if req.Secret == "" {
		fail(w, http.StatusBadRequest, "an SSH password or private key is required")
		return
	}
	if req.AuthType == "key" {
		if err := sshclient.ValidateKey(req.Secret); err != nil {
			fail(w, http.StatusBadRequest, "invalid private key (unencrypted OpenSSH/PEM key required): "+err.Error())
			return
		}
	}

	g := geo.Lookup(req.Host)
	pingMs := sshclient.MeasurePingFromMaster(req.Host, req.Port)
	p := sshclient.Probe(sshclient.Target{Host: req.Host, Port: req.Port, User: req.Username, Secret: req.Secret, AuthType: req.AuthType})

	status := "offline"
	hostKey := ""
	if p.Online {
		status = "online"
		hostKey = p.HostKey
	}
	srv := &db.Server{
		Name:          req.Name,
		Role:          req.Role,
		Host:          req.Host,
		Port:          req.Port,
		Username:      req.Username,
		Password:      req.Secret,
		AuthType:      req.AuthType,
		HostKey:       hostKey,
		LastError:     p.Error,
		Country:       g.Country,
		CountryCode:   g.CountryCode,
		Datacenter:    g.Datacenter,
		CoreInstalled: p.CoreInstalled,
		CoreVersion:   p.CoreVersion,
		Status:        status,
		CPU:            p.CPU,
		RAM:            p.RAM,
		Uptime:         p.Uptime,
		IPv4:           p.IPv4,
		IPv6:           p.IPv6,
		NetRx:          p.NetRx,
		NetTx:          p.NetTx,
		InstalledTools: p.InstalledTools,
		PingMs:         pingMs,
	}
	id, err := h.store.AddServer(srv)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	srv.ID = id
	srv.Password = ""
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "server": srv, "probe_error": p.Error})
}

func (h *Handler) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	old, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	var req serverReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.SSHProfileID > 0 {
		if prof, err := h.store.GetSSHProfileWithSecret(req.SSHProfileID); err == nil {
			req.Username = prof.Username
			req.Port = prof.Port
			req.AuthType = "password"
			req.Secret = prof.Password
		}
	}
	if msg := req.normalize(); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	if req.AuthType == "key" && req.Secret == "" && old.AuthType != "key" {
		fail(w, http.StatusBadRequest, "paste the private key to switch to key authentication")
		return
	}
	if req.AuthType == "key" && req.Secret != "" {
		if err := sshclient.ValidateKey(req.Secret); err != nil {
			fail(w, http.StatusBadRequest, "invalid private key (unencrypted OpenSSH/PEM key required): "+err.Error())
			return
		}
	}
	endpointChanged := req.Host != old.Host || req.Port != old.Port
	if err := h.store.UpdateServer(id, req.Name, req.Role, req.Host, req.Port, req.Username, req.AuthType, req.Secret, endpointChanged); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Host != old.Host {
		g := geo.Lookup(req.Host)
		_ = h.store.SetGeo(id, g.Country, g.CountryCode, g.Datacenter)
	}
	p, _ := h.probeServer(id)
	perr := ""
	if p != nil {
		perr = p.Error
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "probe_error": perr})
}

func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteServer(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleProbeServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := h.probeServer(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "probe": p})
}

func (h *Handler) handleProbeAll(w http.ResponseWriter, r *http.Request) {
	h.probeAll()
	updated, _ := h.store.ListServers()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": updated})
}

func (h *Handler) handleResetHostKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.SetHostKey(id, ""); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := h.probeServer(id)
	perr := ""
	if p != nil {
		perr = p.Error
	}
	log.Printf("host key reset for server %d by %s", id, r.Header.Get("X-MC-User"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "probe_error": perr})
}

func (h *Handler) handleInstallCore(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Source string `json:"source"` // "mirror" or "github"
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	if req.Source != "mirror" {
		req.Source = "github"
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	st := h.store.GetSettings()
	if !validURLRe.MatchString(st.GitHubRepoURL) || !validURLRe.MatchString(st.IranMirrorURL) {
		fail(w, http.StatusBadRequest, "core download URLs in Settings are invalid")
		return
	}
	var cmd string
	if req.Source == "mirror" {
		base := strings.TrimRight(st.IranMirrorURL, "/")
		cmd = fmt.Sprintf(`curl -fsSL %s | bash -s -- --remote %s --no-launch`, sshclient.ShQuote(base+"/install.sh"), sshclient.ShQuote(base))
	} else {
		base := strings.TrimRight(st.GitHubRepoURL, "/")
		cmd = fmt.Sprintf(`curl -fsSL %s | bash -s -- --online --no-launch`, sshclient.ShQuote(base+"/install.sh"))
	}
	res := h.runOnServer(srv, cmd, 180*time.Second)
	if res.Err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": res.Err.Error(), "output": res.Output})
		return
	}
	p, _ := h.probeServer(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": res.Output, "probe": p})
}

func (h *Handler) handleService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	switch req.Action {
	case "status", "start", "stop", "restart", "logs":
	default:
		fail(w, http.StatusBadRequest, "unsupported action")
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	svc := h.store.GetSettings().ServiceName
	if !validServiceRe.MatchString(svc) {
		fail(w, http.StatusBadRequest, "invalid service name in Settings")
		return
	}
	if req.Action != "status" && req.Action != "logs" {
		log.Printf("service %s %s on server %d by %s", req.Action, svc, id, r.Header.Get("X-MC-User"))
	}
	var svcCmd string
	svcQ := sshclient.ShQuote(svc)
	switch req.Action {
	case "logs":
		svcCmd = "journalctl -u " + svcQ + " -n 120 --no-pager 2>&1"
	case "status":
		svcCmd = "systemctl status " + svcQ + " --no-pager -l 2>&1 | head -n 40"
	default:
		svcCmd = "systemctl " + req.Action + " " + svcQ + " 2>&1; echo; systemctl is-active " + svcQ + " 2>&1"
	}
	res := h.runOnServer(srv, svcCmd, 40*time.Second)
	if res.Err != nil && !sshclient.IsExitError(res.Err) {
		fail(w, http.StatusBadGateway, res.Err.Error())
		return
	}
	if srv.HostKey == "" && res.HostKey != "" {
		_ = h.store.SetHostKey(srv.ID, res.HostKey)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": res.Output})
}

func (h *Handler) handleExec(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Command string `json:"command"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	if req.Command == "" || len(req.Command) > 4000 {
		fail(w, http.StatusBadRequest, "command is empty or too long (max 4000 chars)")
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	log.Printf("remote exec on server %d by %s: %q", id, r.Header.Get("X-MC-User"), req.Command)
	res := h.runOnServer(srv, req.Command, 30*time.Second)
	exit := ""
	if res.Err != nil {
		if !sshclient.IsExitError(res.Err) {
			fail(w, http.StatusBadGateway, res.Err.Error())
			return
		}
		exit = res.Err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": res.Output, "exit": exit})
}

// ---------------------------------------------------------------------------
// Settings & password
// ---------------------------------------------------------------------------

func (h *Handler) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": h.store.GetSettings()})
}

func (h *Handler) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var st db.Settings
	if !readJSON(w, r, &st) {
		return
	}
	for _, u := range []string{st.GitHubRepoURL, st.IranMirrorURL, st.PanelGitHubURL, st.PanelMirrorURL} {
		u = strings.TrimSpace(u)
		if u != "" && !validURLRe.MatchString(strings.TrimRight(u, "/")) {
			fail(w, http.StatusBadRequest, "invalid URL: only http(s) URLs with letters, digits and . _ ~ : / @ % + - are allowed")
			return
		}
	}
	st.GitHubRepoURL = strings.TrimSpace(st.GitHubRepoURL)
	st.IranMirrorURL = strings.TrimSpace(st.IranMirrorURL)
	st.PanelGitHubURL = strings.TrimSpace(st.PanelGitHubURL)
	st.PanelMirrorURL = strings.TrimSpace(st.PanelMirrorURL)
	st.ServiceName = strings.TrimSpace(st.ServiceName)
	if st.ServiceName != "" && !validServiceRe.MatchString(st.ServiceName) {
		fail(w, http.StatusBadRequest, "invalid service name")
		return
	}
	if st.ProbeInterval < 0 {
		st.ProbeInterval = 0
	}
	if st.ProbeInterval > 1440 {
		st.ProbeInterval = 1440
	}
	if err := h.store.SaveSettings(st); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	user := r.Header.Get("X-MC-User")
	if user == "" {
		user = "admin"
	}
	ip := clientIP(r)
	if d := h.limiter.wait(ip); d > 0 {
		fail(w, http.StatusTooManyRequests, "too many attempts, retry later")
		return
	}
	if !h.store.VerifyUser(user, req.CurrentPassword) {
		h.limiter.fail(ip)
		fail(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	h.limiter.ok(ip)
	if len(req.NewPassword) < db.MinPasswordLen {
		fail(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if err := h.store.UpdateUserPassword(user, req.NewPassword); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// All previously issued tokens are now invalid (they embed the old password tag); hand out a fresh one.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": h.makeToken(user)})
}

// ---------------------------------------------------------------------------
// Updates
// ---------------------------------------------------------------------------

func (h *Handler) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	st := h.store.GetSettings()
	res := updater.CheckAll(st.PanelGitHubURL, st.PanelMirrorURL, st.BetaBuilds, st.InstalledBetaBuild)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "check": res})
}

func (h *Handler) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": updater.GetJobStatus()})
}

func (h *Handler) handleUpdateRemote(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"` // "github" or "mirror"
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)
	if req.Source != "mirror" && req.Source != "beta" {
		req.Source = "github"
	}
	st := h.store.GetSettings()
	log.Printf("remote update requested from %s by %s", req.Source, r.Header.Get("X-MC-User"))
	err := updater.StartRemoteUpdate(req.Source, st.PanelGitHubURL, st.PanelMirrorURL, st.AllowUnverified, func(build int) {
		_ = h.store.SetInstalledBetaBuild(build)
	}, func() {
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	})
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "started": true})
}

func (h *Handler) handleUpdateUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 140<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, "failed to parse upload")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "zip file is required")
		return
	}
	defer file.Close()

	tmpFile, err := os.CreateTemp("", "mcontrol-upload-*.zip")
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpFile.Close()

	log.Printf("manual ZIP update uploaded by %s", r.Header.Get("X-MC-User"))
	err = updater.StartZipUpdate(tmpPath, func() {
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	})
	if err != nil {
		os.Remove(tmpPath)
		fail(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "started": true})
}

// ---------------------------------------------------------------------------
// Local info, backup & restore
// ---------------------------------------------------------------------------

func (h *Handler) handleLocalStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": sysinfo.Collect()})
}

func (h *Handler) handleLocalLogs(w http.ResponseWriter, r *http.Request) {
	out, err := exec.Command("journalctl", "-u", "mcontrol.service", "-n", "80", "--no-pager").CombinedOutput()
	if err != nil && len(out) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": "journalctl unavailable or no logs yet."})
		return
	}
	// The one-time setup token is only meaningful before setup; never echo it back through the panel.
	logs := regexp.MustCompile(`FIRST-TIME SETUP TOKEN: [0-9a-f]+`).ReplaceAllString(string(out), "FIRST-TIME SETUP TOKEN: [hidden]")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": logs})
}

// handleBackupDB returns a ZIP with a consistent database snapshot AND the encryption key,
// because the stored SSH credentials cannot be decrypted without the key.
func (h *Handler) handleBackupDB(w http.ResponseWriter, r *http.Request) {
	tmpDir, err := os.MkdirTemp("", "mcontrol-bak-*")
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)

	snap := filepath.Join(tmpDir, "mcontrol.db")
	if err := h.store.Snapshot(snap); err != nil {
		fail(w, http.StatusInternalServerError, "snapshot failed: "+err.Error())
		return
	}
	dbBytes, err := os.ReadFile(snap)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	keyBytes, err := os.ReadFile(h.store.KeyPath())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries := []struct {
		name string
		data []byte
	}{{"mcontrol.db", dbBytes}, {".secret.key", keyBytes}}
	if b, err := os.ReadFile(filepath.Join(h.store.DataDir(), "tls.crt")); err == nil {
		entries = append(entries, struct{ name string; data []byte }{"tls.crt", b})
	}
	if b, err := os.ReadFile(filepath.Join(h.store.DataDir(), "tls.key")); err == nil {
		entries = append(entries, struct{ name string; data []byte }{"tls.key", b})
	}
	for _, f := range entries {
		fw, err := zw.Create(f.name)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := fw.Write(f.data); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := zw.Close(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"mcontrol-backup.zip\"")
	_, _ = w.Write(buf.Bytes())
}

func readZipEntry(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}

func (h *Handler) handleRestoreDB(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 80<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		fail(w, http.StatusBadRequest, "failed to parse upload")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		fail(w, http.StatusBadRequest, "backup file (.zip or .db) is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, 64<<20))
	if err != nil {
		fail(w, http.StatusInternalServerError, "failed to read upload")
		return
	}

	var dbData, keyData, certData, tlsKeyData []byte
	legacy := false
	if bytes.HasPrefix(data, []byte("PK")) {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid backup archive")
			return
		}
		for _, f := range zr.File {
			switch f.Name {
			case "mcontrol.db":
				dbData, err = readZipEntry(f, 64<<20)
			case ".secret.key":
				keyData, err = readZipEntry(f, 4096)
			case "tls.crt":
				certData, err = readZipEntry(f, 65536)
			case "tls.key":
				tlsKeyData, err = readZipEntry(f, 65536)
			}
			if err != nil {
				fail(w, http.StatusBadRequest, "corrupt backup archive")
				return
			}
		}
		if dbData == nil {
			fail(w, http.StatusBadRequest, "backup archive has no mcontrol.db")
			return
		}
		if keyData == nil || len(keyData) < 32 {
			fail(w, http.StatusBadRequest, "backup archive has no valid .secret.key")
			return
		}
	} else {
		dbData = data
		legacy = true
	}
	if len(dbData) < 16 || !bytes.HasPrefix(dbData, []byte("SQLite format 3\x00")) {
		fail(w, http.StatusBadRequest, "invalid SQLite database file")
		return
	}

	dbPath := h.store.DBPath()
	tmpPath := dbPath + ".restore.tmp"
	if err := os.WriteFile(tmpPath, dbData, 0600); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Keep a safety copy of the current database and key.
	if cur, err := os.ReadFile(dbPath); err == nil {
		_ = os.WriteFile(dbPath+".bak", cur, 0600)
	}
	if curKey, err := os.ReadFile(h.store.KeyPath()); err == nil {
		_ = os.WriteFile(h.store.KeyPath()+".bak", curKey, 0600)
	}

	msg := "Backup restored. Restarting MControl..."
	if legacy {
		msg = "Legacy .db restored (no key inside): saved SSH credentials only work if the key on this server is the original one. Restarting..."
	}
	log.Printf("database restore requested by %s", r.Header.Get("X-MC-User"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
	go func() {
		time.Sleep(600 * time.Millisecond)
		if len(certData) > 0 && len(tlsKeyData) > 0 {
			_ = os.WriteFile(filepath.Join(h.store.DataDir(), "tls.crt"), certData, 0600)
			_ = os.WriteFile(filepath.Join(h.store.DataDir(), "tls.key"), tlsKeyData, 0600)
		}
		if err := h.store.ReplaceFiles(tmpPath, keyData); err != nil {
			log.Printf("restore failed: %v", err)
		}
		os.Exit(0)
	}()
}

func (h *Handler) handleSaveAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WebPath      string `json:"web_path"`
		Port         int    `json:"port"`
		ListenDomain string `json:"listen_domain"`
		StrictDomain bool   `json:"strict_domain"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.WebPath = strings.Trim(strings.TrimSpace(req.WebPath), "/")
	req.ListenDomain = strings.ToLower(strings.TrimSpace(req.ListenDomain))
	if req.WebPath != "" && !validWebPathRe.MatchString(req.WebPath) {
		fail(w, http.StatusBadRequest, "web path must be 3-48 chars (letters, numbers, _ or -)")
		return
	}
	if req.ListenDomain != "" && !validHostRe.MatchString(req.ListenDomain) {
		fail(w, http.StatusBadRequest, "invalid listen domain")
		return
	}
	if err := h.store.UpdatePanelAddress(req.WebPath, req.Port, req.ListenDomain, req.StrictDomain); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "web_path": req.WebPath, "port": req.Port, "listen_domain": req.ListenDomain, "message": "Address & Listen Domain updated! Restarting panel..."})
	go func() {
		time.Sleep(1200 * time.Millisecond)
		os.Exit(0)
	}()
}

func (h *Handler) handleSaveSecurity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username     string `json:"username"`
		TwoFAEnabled *bool  `json:"twofa_enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	curUser := r.Header.Get("X-MC-User")
	newToken := ""
	if strings.TrimSpace(req.Username) != "" && strings.TrimSpace(req.Username) != curUser {
		if err := h.store.UpdateUsername(curUser, req.Username); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		curUser = strings.TrimSpace(req.Username)
		newToken = h.makeToken(curUser)
	}
	st := h.store.GetSettings()
	en, sec := st.TwoFAEnabled, st.TwoFASecret
	if req.TwoFAEnabled != nil {
		en = *req.TwoFAEnabled
		if en && sec == "" {
			b := make([]byte, 10)
			_, _ = rand.Read(b)
			sec = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
		}
	}
	if err := h.store.Set2FA(en, sec); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": newToken, "twofa_enabled": en, "twofa_secret": sec})
}

func (h *Handler) handleLocalSpeedtest(w http.ResponseWriter, r *http.Request) {
	out, err := exec.Command("bash", "-c", sshclient.SpeedtestCmd).CombinedOutput()
	if err != nil && len(out) == 0 {
		fail(w, http.StatusInternalServerError, "speedtest failed: "+err.Error())
		return
	}
	sr := sshclient.ParseSpeedOutput(string(out))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "speed": sr})
}

func (h *Handler) handleServerSpeedtest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	var sr sshclient.SpeedResult
	if srv.AuthMode == "peer" && srv.PeerCode != "" {
		sr = sshclient.RunPeerSpeedtest(srv.Host, srv.AgentPort, srv.PeerCode)
	} else {
		sr = sshclient.RunSpeedtest(targetOf(srv))
	}
	if sr.Error == "" {
		speedTxt := fmt.Sprintf("↓ %s Mbps • ↑ %s Mbps • %s ms", sr.DownloadMB, sr.UploadMB, sr.PingMs)
		_ = h.store.SetServerSpeed(id, speedTxt)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "speed": sr})
}

func (h *Handler) handlePairPeer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	if srv.Password == "" {
		fail(w, http.StatusBadRequest, "SSH credential required for initial pairing (use Edit to set it)")
		return
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	peerCode := "MC-" + strings.ToUpper(hex.EncodeToString(b))
	agentPort := srv.AgentPort
	if agentPort <= 0 {
		agentPort = 2054
	}
	res := sshclient.InstallPeerAgent(targetOf(srv), peerCode, agentPort)
	if res.Err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "agent install failed: " + res.Err.Error(), "output": res.Output})
		return
	}
	p := sshclient.ProbePeer(srv.Host, agentPort, peerCode)
	if !p.Online {
		fail(w, http.StatusBadGateway, fmt.Sprintf("agent installed but port %d unreachable (check firewall): %s", agentPort, p.Error))
		return
	}
	if err := h.store.SetServerPeerMode(id, peerCode, agentPort); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.applyProbe(srv, p)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "peer_code": peerCode, "message": "Paired via Peer Code! SSH password wiped from database."})
}

func (h *Handler) handleTestServer(w http.ResponseWriter, r *http.Request) {
	var req serverReq
	if !readJSON(w, r, &req) {
		return
	}
	if req.SSHProfileID > 0 {
		if prof, err := h.store.GetSSHProfileWithSecret(req.SSHProfileID); err == nil {
			req.Username = prof.Username
			req.Port = prof.Port
			req.AuthType = "password"
			req.Secret = prof.Password
		}
	}

	if msg := req.normalize(); msg != "" {
		fail(w, http.StatusBadRequest, msg)
		return
	}
	if req.Secret == "" {
		fail(w, http.StatusBadRequest, "SSH password or key is required to test")
		return
	}
	if req.AuthType == "key" {
		if err := sshclient.ValidateKey(req.Secret); err != nil {
			fail(w, http.StatusBadRequest, "invalid private key: "+err.Error())
			return
		}
	}
	pingMs := sshclient.MeasurePingFromMaster(req.Host, req.Port)
	p := sshclient.Probe(sshclient.Target{Host: req.Host, Port: req.Port, User: req.Username, Secret: req.Secret, AuthType: req.AuthType})
	if !p.Online {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "ping_ms": pingMs, "error": p.Error})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"ping_ms": pingMs,
		"cpu":     p.CPU,
		"ram":     p.RAM,
		"uptime":  p.Uptime,
		"ipv4":    p.IPv4,
	})
}

func normalizePorts(raw string) (string, []int, error) {
	// Split on any comma or whitespace (space, tab, newline)
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';'
	})
	seen := map[int]bool{}
	var ports []int
	var strs []string
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		p, err := strconv.Atoi(tok)
		if err != nil || p < 1 || p > 65535 {
			return "", nil, fmt.Errorf("invalid port: %q (must be 1-65535)", tok)
		}
		if !seen[p] {
			seen[p] = true
			ports = append(ports, p)
			strs = append(strs, strconv.Itoa(p))
		}
	}
	return strings.Join(strs, ", "), ports, nil
}

func (h *Handler) handleSaveDefaults(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DefaultSSHUser string `json:"default_ssh_user"`
		DefaultSSHPort int    `json:"default_ssh_port"`
		DefaultSSHPass string `json:"default_ssh_pass"`
		ClearPass      bool   `json:"clear_pass"`
		SavedPorts     string `json:"saved_ports"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.DefaultSSHUser != "" && !validUserRe.MatchString(strings.TrimSpace(req.DefaultSSHUser)) {
		fail(w, http.StatusBadRequest, "invalid default SSH username")
		return
	}
	normPorts, portList, err := normalizePorts(req.SavedPorts)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.SaveDefaults(req.DefaultSSHUser, req.DefaultSSHPort, req.DefaultSSHPass, req.ClearPass, normPorts); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "saved_ports": normPorts, "ports": portList})
}

func (h *Handler) handleListSSHProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListSSHProfiles()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profiles": list})
}

func (h *Handler) handleAddSSHProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
		Port     int    `json:"port"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimSpace(req.Username)
	if req.Name == "" || req.Password == "" {
		fail(w, http.StatusBadRequest, "profile name and password are required")
		return
	}
	if req.Username == "" {
		req.Username = "root"
	}
	if req.Port <= 0 || req.Port > 65535 {
		req.Port = 22
	}
	id, err := h.store.AddSSHProfile(req.Name, req.Username, req.Password, req.Port)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (h *Handler) handleDeleteSSHProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteSSHProfile(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleListPortProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.ListPortProfiles()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profiles": list})
}

func (h *Handler) handleAddPortProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Ports string `json:"ports"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(w, http.StatusBadRequest, "profile name is required")
		return
	}
	norm, _, err := normalizePorts(req.Ports)
	if err != nil || norm == "" {
		fail(w, http.StatusBadRequest, "at least one valid port (1-65535) is required")
		return
	}
	id, err := h.store.AddPortProfile(req.Name, norm)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "ports": norm})
}

func (h *Handler) handleDeletePortProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeletePortProfile(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleUpdateSSHProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
		Port     int    `json:"port"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimSpace(req.Username)
	if req.Name == "" {
		fail(w, http.StatusBadRequest, "profile name is required")
		return
	}
	if req.Username == "" {
		req.Username = "root"
	}
	if req.Port <= 0 || req.Port > 65535 {
		req.Port = 22
	}
	if err := h.store.UpdateSSHProfile(id, req.Name, req.Username, req.Password, req.Port); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleUpdatePortProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name  string `json:"name"`
		Ports string `json:"ports"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		fail(w, http.StatusBadRequest, "profile name is required")
		return
	}
	norm, _, err := normalizePorts(req.Ports)
	if err != nil || norm == "" {
		fail(w, http.StatusBadRequest, "at least one valid port (1-65535) is required")
		return
	}
	if err := h.store.UpdatePortProfile(id, req.Name, norm); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ports": norm})
}

func (h *Handler) handleGetTLS(w http.ResponseWriter, r *http.Request) {
	dir := h.store.DataDir()
	_, e1 := os.Stat(filepath.Join(dir, "tls.crt"))
	_, e2 := os.Stat(filepath.Join(dir, "tls.key"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": e1 == nil && e2 == nil})
}

func (h *Handler) handleSaveTLS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"` // "custom" or "selfsigned"
		Host string `json:"host"`
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	dir := h.store.DataDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	var certPEM, keyPEM []byte
	if req.Mode == "selfsigned" {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		tmpl := x509.Certificate{
			SerialNumber: serial,
			Subject:      pkix.Name{CommonName: "MControl Panel"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		host := strings.TrimSpace(req.Host)
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if host != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
		der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		keyDER, err := x509.MarshalECPrivateKey(priv)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	} else {
		certPEM = []byte(strings.TrimSpace(req.Cert))
		keyPEM = []byte(strings.TrimSpace(req.Key))
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			fail(w, http.StatusBadRequest, "both certificate (PEM) and private key (PEM) are required")
			return
		}
		if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
			fail(w, http.StatusBadRequest, "invalid certificate/key pair: "+err.Error())
			return
		}
	}

	if err := os.WriteFile(certPath, certPEM, 0600); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "HTTPS enabled! Restarting panel..."})
	go func() {
		time.Sleep(1200 * time.Millisecond)
		os.Exit(0)
	}()
}

func (h *Handler) handleDeleteTLS(w http.ResponseWriter, r *http.Request) {
	dir := h.store.DataDir()
	_ = os.Remove(filepath.Join(dir, "tls.crt"))
	_ = os.Remove(filepath.Join(dir, "tls.key"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "HTTPS disabled! Restarting on HTTP..."})
	go func() {
		time.Sleep(1200 * time.Millisecond)
		os.Exit(0)
	}()
}

func (h *Handler) handleSavePanelName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PanelName string `json:"panel_name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.PanelName = strings.TrimSpace(req.PanelName)
	if len(req.PanelName) > 64 {
		fail(w, http.StatusBadRequest, "panel name is too long (max 64 chars)")
		return
	}
	if err := h.store.SetPanelName(req.PanelName); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "panel_name": req.PanelName})
}

func (h *Handler) handleServerTweak(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action   string `json:"action"`
		Password string `json:"password"`
		Username string `json:"username"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	if srv.AuthMode != "peer" && srv.Password == "" {
		fail(w, http.StatusBadRequest, "SSH credential or Peer Agent required for tweaks")
		return
	}

	var cmd string
	switch req.Action {
	case "fw_off":
		cmd = `sudo ufw disable 2>/dev/null || true; sudo systemctl stop firewalld 2>/dev/null || true; sudo systemctl disable firewalld 2>/dev/null || true; sudo iptables -P INPUT ACCEPT 2>/dev/null || true; sudo iptables -F 2>/dev/null || true; echo "Firewall disabled & flushed."`
	case "bbr_on":
		cmd = `sudo sysctl -w net.core.default_qdisc=fq net.ipv4.tcp_congestion_control=bbr && (grep -q "tcp_congestion_control=bbr" /etc/sysctl.conf || printf "\nnet.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n" | sudo tee -a /etc/sysctl.conf >/dev/null) && echo "BBR enabled: $(sysctl -n net.ipv4.tcp_congestion_control)"`
	case "ipv6_on":
		cmd = `sudo sysctl -w net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0 net.ipv6.conf.lo.disable_ipv6=0 && echo "IPv6 enabled."`
	case "ipv6_off":
		cmd = `sudo sysctl -w net.ipv6.conf.all.disable_ipv6=1 net.ipv6.conf.default.disable_ipv6=1 net.ipv6.conf.lo.disable_ipv6=1 && echo "IPv6 disabled."`
	case "net_opt":
		cmd = `cat <<'EOF' | sudo tee /etc/sysctl.d/99-mcontrol-opt.conf >/dev/null
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.tcp_fastopen=3
net.core.somaxconn=65535
net.ipv4.tcp_max_syn_backlog=65535
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.ip_forward=1
EOF
sudo sysctl --system >/dev/null 2>&1 || sudo sysctl -p /etc/sysctl.d/99-mcontrol-opt.conf; echo "Kernel network & tunnel sysctl optimized."`
	case "clear_cache":
		cmd = `sync; echo 3 | sudo tee /proc/sys/vm/drop_caches >/dev/null 2>&1 || true; sudo journalctl --vacuum-time=2d 2>/dev/null || true; echo "RAM cache & old logs cleaned."`
	case "enable_root":
		pass := strings.TrimSpace(req.Password)
		if pass == "" {
			cmd = `sudo sed -i -E 's/^#?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && sudo sed -i -E 's/^#?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && (sudo systemctl restart ssh 2>/dev/null || sudo systemctl restart sshd 2>/dev/null) && echo "Root SSH login enabled."`
		} else {
			cmd = fmt.Sprintf(`echo %s | sudo chpasswd && sudo sed -i -E 's/^#?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config && sudo sed -i -E 's/^#?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config && (sudo systemctl restart ssh 2>/dev/null || sudo systemctl restart sshd 2>/dev/null) && echo "Root SSH login enabled & root password updated."`, sshclient.ShQuote("root:"+pass))
		}
	case "change_pass":
		user := strings.TrimSpace(req.Username)
		if user == "" {
			user = srv.Username
		}
		if !validUserRe.MatchString(user) {
			fail(w, http.StatusBadRequest, "invalid username")
			return
		}
		pass := strings.TrimSpace(req.Password)
		if len(pass) < 4 {
			fail(w, http.StatusBadRequest, "password must be at least 4 characters")
			return
		}
		cmd = fmt.Sprintf(`echo %s | sudo chpasswd && echo "Password updated for user %s."`, sshclient.ShQuote(user+":"+pass), user)
	case "reboot":
		cmd = `nohup bash -c 'sleep 1 && sudo reboot' >/dev/null 2>&1 & echo "Reboot scheduled."`
	default:
		fail(w, http.StatusBadRequest, "unknown tweak action")
		return
	}

	log.Printf("tweak %s on server %d by %s", req.Action, id, r.Header.Get("X-MC-User"))
	res := h.runOnServer(srv, cmd, 35*time.Second)
	if res.Err != nil && !sshclient.IsExitError(res.Err) {
		fail(w, http.StatusBadGateway, res.Err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "output": res.Output, "error": func() string { if res.Err != nil { return res.Err.Error() }; return "" }()})
}

func verifyTOTP(secretB32, code string) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return false
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secretB32)))
	if err != nil {
		// Backward compatibility if an older hex secret was stored
		if b, err2 := hex.DecodeString(strings.TrimSpace(secretB32)); err2 == nil {
			key = b
		} else {
			return false
		}
	}
	nowStep := time.Now().Unix() / 30
	for _, delta := range []int64{-1, 0, 1} {
		var msg [8]byte
		binary.BigEndian.PutUint64(msg[:], uint64(nowStep+delta))
		mac := hmac.New(sha1.New, key)
		mac.Write(msg[:])
		sum := mac.Sum(nil)
		off := sum[len(sum)-1] & 0x0f
		binCode := (int(sum[off]&0x7f) << 24) | (int(sum[off+1]) << 16) | (int(sum[off+2]) << 8) | int(sum[off+3])
		otp := fmt.Sprintf("%06d", binCode%1000000)
		if subtle.ConstantTimeCompare([]byte(otp), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func (h *Handler) runOnServer(srv *db.Server, cmd string, timeout time.Duration) sshclient.Result {
	if srv.AuthMode == "peer" && srv.PeerCode != "" {
		return sshclient.PeerExec(srv.Host, srv.AgentPort, srv.PeerCode, cmd, timeout)
	}
	return sshclient.Run(targetOf(srv), cmd, 10*time.Second, timeout)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get("X-MC-User")
	if user != "" {
		_ = h.store.BumpTokenVersion(user)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) handleUnpairPeer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.UnpairServer(id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Server unpaired. Use Edit to set SSH credentials if needed."})
}

func (h *Handler) handleUpdatePeerAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	if srv.PeerCode == "" {
		fail(w, http.StatusBadRequest, "server is not paired with Peer Code")
		return
	}
	script := sshclient.PeerAgentInstallScript(srv.PeerCode, srv.AgentPort)
	var res sshclient.Result
	if srv.AuthMode == "peer" {
		// Run upgrade in background so current HTTP response returns cleanly before agent restarts
		bgCmd := fmt.Sprintf("nohup bash -c %s >/tmp/mc-agent-upd.log 2>&1 & echo 'Agent v2 upgrade triggered.'", sshclient.ShQuote(script))
		res = sshclient.PeerExec(srv.Host, srv.AgentPort, srv.PeerCode, bgCmd, 15*time.Second)
		if res.Err != nil && srv.Password != "" {
			res = sshclient.Run(targetOf(srv), script, 10*time.Second, 35*time.Second)
		}
	} else if srv.Password != "" {
		res = sshclient.Run(targetOf(srv), script, 10*time.Second, 35*time.Second)
	} else {
		fail(w, http.StatusBadRequest, "cannot upgrade agent (old agent lacks /exec and no SSH password stored; Edit server to set SSH password once, then Pair again)")
		return
	}
	if res.Err != nil {
		fail(w, http.StatusBadGateway, "agent update failed: "+res.Err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Peer Agent v2 (TLS + Exec) updated!"})
}

func (h *Handler) handleLocalTweak(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	var cmd string
	switch req.Action {
	case "fw_off":
		cmd = `ufw disable 2>/dev/null || true; systemctl stop firewalld 2>/dev/null || true; systemctl disable firewalld 2>/dev/null || true; iptables -P INPUT ACCEPT 2>/dev/null || true; iptables -F 2>/dev/null || true; echo "Master firewall disabled & flushed."`
	case "bbr_on":
		cmd = `sysctl -w net.core.default_qdisc=fq net.ipv4.tcp_congestion_control=bbr && (grep -q "tcp_congestion_control=bbr" /etc/sysctl.conf || printf "\nnet.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n" >> /etc/sysctl.conf) && echo "BBR enabled on Master: $(sysctl -n net.ipv4.tcp_congestion_control)"`
	case "ipv6_on":
		cmd = `sysctl -w net.ipv6.conf.all.disable_ipv6=0 net.ipv6.conf.default.disable_ipv6=0 net.ipv6.conf.lo.disable_ipv6=0 && echo "IPv6 enabled on Master."`
	case "ipv6_off":
		cmd = `sysctl -w net.ipv6.conf.all.disable_ipv6=1 net.ipv6.conf.default.disable_ipv6=1 net.ipv6.conf.lo.disable_ipv6=1 && echo "IPv6 disabled on Master."`
	case "net_opt":
		cmd = `cat <<'EOF' > /etc/sysctl.d/99-mcontrol-opt.conf
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.tcp_fastopen=3
net.core.somaxconn=65535
net.ipv4.tcp_max_syn_backlog=65535
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.ip_forward=1
EOF
sysctl --system >/dev/null 2>&1 || sysctl -p /etc/sysctl.d/99-mcontrol-opt.conf; echo "Master kernel network optimized."`
	case "clear_cache":
		cmd = `sync; echo 3 > /proc/sys/vm/drop_caches 2>/dev/null || true; journalctl --vacuum-time=2d 2>/dev/null || true; echo "Master RAM cache & old logs cleaned."`
	case "reboot":
		cmd = `nohup bash -c 'sleep 1 && reboot' >/dev/null 2>&1 & echo "Master reboot scheduled."`
	default:
		fail(w, http.StatusBadRequest, "unknown tweak action")
		return
	}
	log.Printf("master tweak %s by %s", req.Action, r.Header.Get("X-MC-User"))
	out, err := exec.Command("bash", "-c", cmd).CombinedOutput()
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": err == nil, "output": strings.TrimSpace(string(out)), "error": errStr})
}

var validDomainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

func (h *Handler) handleIssueACME(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
		Email  string `json:"email"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	email := strings.TrimSpace(req.Email)
	if !validDomainRe.MatchString(domain) {
		fail(w, http.StatusBadRequest, "please enter a valid domain name (e.g. panel.example.com)")
		return
	}
	if email == "" {
		email = "admin@" + domain
	}
	dir := h.store.DataDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	// Install/use certbot (or acme.sh fallback) to issue a real Let's Encrypt certificate on port 80
	script := fmt.Sprintf(`set -e
DOM=%s
MAIL=%s
CRT=%s
KEY=%s
if ! command -v certbot >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq certbot >/dev/null
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y -q certbot >/dev/null
  elif command -v yum >/dev/null 2>&1; then
    yum install -y -q certbot >/dev/null
  fi
fi
if command -v certbot >/dev/null 2>&1; then
  certbot certonly --standalone --non-interactive --agree-tos --email "$MAIL" -d "$DOM" --keep-until-expiring --preferred-challenges http
  cp -f "/etc/letsencrypt/live/$DOM/fullchain.pem" "$CRT"
  cp -f "/etc/letsencrypt/live/$DOM/privkey.pem" "$KEY"
  chmod 600 "$CRT" "$KEY"
  echo "SSL certificate issued successfully via Let's Encrypt (certbot) for $DOM"
else
  if [ ! -x /root/.acme.sh/acme.sh ]; then
    curl -fsSL https://get.acme.sh | sh -s email="$MAIL"
  fi
  /root/.acme.sh/acme.sh --set-default-ca --server letsencrypt
  /root/.acme.sh/acme.sh --issue --standalone -d "$DOM" --force
  /root/.acme.sh/acme.sh --install-cert -d "$DOM" --fullchain-file "$CRT" --key-file "$KEY"
  chmod 600 "$CRT" "$KEY"
  echo "SSL certificate issued successfully via Let's Encrypt (acme.sh) for $DOM"
fi
`, sshclient.ShQuote(domain), sshclient.ShQuote(email), sshclient.ShQuote(certPath), sshclient.ShQuote(keyPath))

	log.Printf("issuing Let's Encrypt SSL for %s by %s", domain, r.Header.Get("X-MC-User"))
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"ok":     false,
			"error":  "SSL issuance failed (ensure DNS A record points to this server & port 80 is free)",
			"output": strings.TrimSpace(string(out)),
		})
		return
	}
	_ = h.store.SetListenDomain(domain)
	st := h.store.GetSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"domain":   domain,
		"port":     st.PanelPort,
		"web_path": st.WebPath,
		"output":   strings.TrimSpace(string(out)),
		"message":  "Let's Encrypt SSL issued for " + domain + "! Restarting on HTTPS...",
	})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	}()
}
