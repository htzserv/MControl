package updater

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/version"
)

const (
	maxDownload     = 200 << 20 // 200 MB
	maxUnpacked     = 500 << 20 // 500 MB total uncompressed
	maxZipFiles     = 5000
	buildTimeoutMin = 10
)


var rawGHRe = regexp.MustCompile(`^https?://raw\.githubusercontent\.com/([^/]+)/([^/]+)/.+`)

func normalizeReleaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if m := rawGHRe.FindStringSubmatch(baseURL); len(m) == 3 {
		return fmt.Sprintf("https://github.com/%s/%s/releases/latest/download", m[1], m[2])
	}
	return baseURL
}

var validBase = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/@%+-]{1,300}$`)


// dialSOCKS5 connects to target addr through a local SOCKS5 proxy (e.g. 127.0.0.1:10808)
// using domain-name addressing (ATYP=0x03) so DNS is resolved remotely through the outbound.
func dialSOCKS5(ctx context.Context, proxyAddr, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid port: %s", portStr)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Greeting: VER=5, NMETHODS=1, METHOD=0 (no auth)
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		conn.Close()
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil || resp[0] != 0x05 || resp[1] != 0x00 {
		conn.Close()
		return nil, errors.New("socks5 handshake failed")
	}
	// CONNECT request with domain name (ATYP=0x03)
	if len(host) > 255 {
		conn.Close()
		return nil, errors.New("hostname too long for socks5")
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
	// Drain bound address
	var skip int
	switch hdr[3] {
	case 0x01:
		skip = 4 + 2
	case 0x04:
		skip = 16 + 2
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

func MakeHTTPClient(timeout time.Duration, socksProxy string) *http.Client {
	if socksProxy == "" {
		return &http.Client{Timeout: timeout}
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialSOCKS5(ctx, socksProxy, network, addr)
		},
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: tr}
}

type SourceStatus struct {
	Version   string `json:"version"`
	Available bool   `json:"available"`
	UpToDate  bool   `json:"up_to_date"`
	Error     string `json:"error,omitempty"`
}

type BetaStatus struct {
	Enabled   bool   `json:"enabled"`
	Tag       string `json:"tag"`
	Version   string `json:"version"`
	Build     int    `json:"build"`
	Commit    string `json:"commit"`
	Available bool   `json:"available"`
	Changelog string `json:"changelog,omitempty"`
	Error     string `json:"error,omitempty"`
}

type CheckResult struct {
	CurrentVersion     string       `json:"current_version"`
	InstalledBetaBuild int          `json:"installed_beta_build"`
	ViaOutbound        bool         `json:"via_outbound"`
	UpdateAvailable    bool         `json:"update_available"`
	Changelog          string       `json:"changelog,omitempty"`
	ChangelogTitle     string       `json:"changelog_title,omitempty"`
	ChangelogKind      string       `json:"changelog_kind,omitempty"`
	GitHub          SourceStatus `json:"github"`
	Mirror          SourceStatus `json:"mirror"`
	Beta            BetaStatus   `json:"beta"`
	CheckedAt       int64        `json:"checked_at"`
}

type JobStatus struct {
	Running bool     `json:"running"`
	Done    bool     `json:"done"`
	Success bool     `json:"success"`
	Percent int      `json:"percent"`
	Stage   string   `json:"stage"`
	Error   string   `json:"error,omitempty"`
	Logs    []string `json:"logs"`
}

var (
	jobMu sync.Mutex
	job   = JobStatus{Logs: []string{}}
)

func GetJobStatus() JobStatus {
	jobMu.Lock()
	defer jobMu.Unlock()
	cp := job
	cp.Logs = append([]string(nil), job.Logs...)
	return cp
}

func resetJob(stage string) bool {
	jobMu.Lock()
	defer jobMu.Unlock()
	if job.Running {
		return false
	}
	job = JobStatus{
		Running: true,
		Done:    false,
		Success: false,
		Percent: 5,
		Stage:   stage,
		Logs:    []string{fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), stage)},
	}
	return true
}

func setProgress(pct int, stage string) {
	jobMu.Lock()
	defer jobMu.Unlock()
	job.Percent = pct
	if stage != "" {
		job.Stage = stage
		job.Logs = append(job.Logs, fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), stage))
	}
}

func appendLog(line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	jobMu.Lock()
	defer jobMu.Unlock()
	job.Logs = append(job.Logs, line)
	if len(job.Logs) > 300 {
		job.Logs = job.Logs[len(job.Logs)-300:]
	}
}

func finishJob(success bool, stage, errMsg string) {
	jobMu.Lock()
	defer jobMu.Unlock()
	job.Running = false
	job.Done = true
	job.Success = success
	if success {
		job.Percent = 100
	}
	job.Stage = stage
	job.Error = errMsg
	if errMsg != "" {
		job.Logs = append(job.Logs, "[ERROR] "+errMsg)
	} else {
		job.Logs = append(job.Logs, "[DONE] "+stage)
	}
}

func fetchRemoteVersion(baseURL, socksProxy string) SourceStatus {
	baseURL = normalizeReleaseURL(baseURL)
	if baseURL == "" {
		return SourceStatus{Error: "URL not configured"}
	}
	if !validBase.MatchString(baseURL) {
		return SourceStatus{Error: "invalid URL"}
	}
	client := MakeHTTPClient(8*time.Second, socksProxy)
	resp, err := client.Get(baseURL + "/VERSION?t=" + fmt.Sprint(time.Now().Unix()))
	if err != nil {
		return SourceStatus{Error: "unreachable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return SourceStatus{Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return SourceStatus{Error: "read error"}
	}
	ver := strings.TrimSpace(string(raw))
	ver = strings.TrimPrefix(strings.ToLower(ver), "v")
	if ver == "" || len(ver) > 32 {
		return SourceStatus{Error: "invalid VERSION file"}
	}
	cmp := version.Compare(ver, version.Current)
	return SourceStatus{
		Version:   ver,
		Available: cmp > 0,
		UpToDate:  cmp == 0,
	}
}


func releaseDownloadRoot(baseURL string) string {
	u := normalizeReleaseURL(baseURL)
	u = strings.TrimSuffix(u, "/latest/download")
	return u
}

func fetchBetaStatus(githubURL, mirrorURL, socksProxy string, installedBuild int) BetaStatus {
	st := BetaStatus{Enabled: true}
	var candidates []string
	if ghRoot := releaseDownloadRoot(githubURL); ghRoot != "" && validBase.MatchString(ghRoot) {
		candidates = append(candidates, ghRoot+"/download/beta-latest/beta-meta.json")
	}
	if ir := strings.TrimRight(strings.TrimSpace(mirrorURL), "/"); ir != "" && validBase.MatchString(ir) {
		candidates = append(candidates, ir+"/beta-latest/beta-meta.json")
	}
	var lastErr string
	for idx, u := range candidates {
		proxy := ""
		if idx == 0 {
			proxy = socksProxy
		}
		client := MakeHTTPClient(8*time.Second, proxy)
		resp, err := client.Get(u + "?t=" + fmt.Sprint(time.Now().Unix()))
		if err != nil {
			lastErr = "unreachable"
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
			continue
		}
		var meta struct {
			Version   string `json:"version"`
			Build     int    `json:"build"`
			Commit    string `json:"commit"`
			Channel   string `json:"channel"`
			Tag       string `json:"tag"`
			Changelog string `json:"changelog"`
		}
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&meta)
		resp.Body.Close()
		if decErr != nil || meta.Build <= 0 {
			lastErr = "invalid beta-meta.json"
			continue
		}
		st.Version = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(meta.Version)), "v")
		st.Build = meta.Build
		st.Commit = meta.Commit
		st.Tag = meta.Tag
		st.Changelog = strings.TrimSpace(meta.Changelog)
		if st.Tag == "" {
			st.Tag = fmt.Sprintf("%s-beta.%d", st.Version, st.Build)
		}
		cmp := version.Compare(st.Version, version.Current)
		if cmp > 0 {
			st.Available = true
		} else if cmp == 0 {
			// If beta-latest was just mirrored from the exact same official release, don't nag Stable users.
			// Otherwise (a true post-release Rolling Beta build), offer it whenever build > installedBuild!
			if strings.EqualFold(strings.TrimSpace(meta.Channel), "release") && installedBuild == 0 {
				st.Available = false
			} else {
				st.Available = st.Build > installedBuild
			}
		}
		return st
	}
	if lastErr == "" {
		lastErr = "no beta source configured"
	}
	st.Error = lastErr
	return st
}


var clHeaderRe = regexp.MustCompile(`(?m)^##\s+v?([0-9]+(?:\.[0-9]+)+)`)

func extractChangelogRange(md, fromVer, toVer string) string {
	locs := clHeaderRe.FindAllStringSubmatchIndex(md, -1)
	if len(locs) == 0 {
		return ""
	}
	var out []string
	for i, loc := range locs {
		secVer := md[loc[2]:loc[3]]
		start := loc[0]
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if version.Compare(secVer, fromVer) > 0 && version.Compare(secVer, toVer) <= 0 {
			out = append(out, strings.TrimSpace(md[start:end]))
		}
	}
	if len(out) == 0 && len(locs) > 0 {
		end := len(md)
		if len(locs) > 1 {
			end = locs[1][0]
		}
		return strings.TrimSpace(md[locs[0][0]:end])
	}
	return strings.Join(out, "\n\n")
}

func fetchReleaseChangelog(githubURL, mirrorURL, socksProxy, fromVer, toVer string) string {
	var urls []string
	if gh := normalizeReleaseURL(githubURL); gh != "" && validBase.MatchString(gh) {
		urls = append(urls, gh+"/CHANGELOG.md")
	}
	urls = append(urls, "https://raw.githubusercontent.com/htzserv/MControl/main/CHANGELOG.md")
	if ir := strings.TrimRight(strings.TrimSpace(mirrorURL), "/"); ir != "" && validBase.MatchString(ir) {
		urls = append(urls, ir+"/CHANGELOG.md")
	}
	for idx, u := range urls {
		px := socksProxy
		if idx == len(urls)-1 && strings.HasPrefix(u, strings.TrimRight(strings.TrimSpace(mirrorURL), "/")) {
			px = ""
		}
		client := MakeHTTPClient(7*time.Second, px)
		resp, err := client.Get(u + "?t=" + fmt.Sprint(time.Now().Unix()))
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		resp.Body.Close()
		if err == nil && len(raw) > 0 {
			if txt := extractChangelogRange(string(raw), fromVer, toVer); txt != "" {
				return txt
			}
		}
	}
	return ""
}

func CheckAll(githubURL, mirrorURL, socksProxy string, betaEnabled bool, installedBetaBuild int) CheckResult {
	res := CheckResult{
		CurrentVersion:     version.Current,
		InstalledBetaBuild: func() int { if betaEnabled { return installedBetaBuild }; return 0 }(),
		ViaOutbound:        socksProxy != "",
		CheckedAt:          time.Now().Unix(),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		res.GitHub = fetchRemoteVersion(githubURL, socksProxy)
	}()
	go func() {
		defer wg.Done()
		res.Mirror = fetchRemoteVersion(mirrorURL, "")
	}()
	if betaEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res.Beta = fetchBetaStatus(githubURL, mirrorURL, socksProxy, installedBetaBuild)
		}()
	}
	wg.Wait()
	res.UpdateAvailable = res.GitHub.Available || res.Mirror.Available || (betaEnabled && res.Beta.Available)
	if res.GitHub.Available || res.Mirror.Available {
		targetVer := res.GitHub.Version
		if targetVer == "" {
			targetVer = res.Mirror.Version
		}
		res.ChangelogKind = "release"
		res.ChangelogTitle = fmt.Sprintf("Release Changelog (v%s → v%s)", version.Current, targetVer)
		res.Changelog = fetchReleaseChangelog(githubURL, mirrorURL, socksProxy, version.Current, targetVer)
	} else if betaEnabled && res.Beta.Available {
		res.ChangelogKind = "beta"
		res.ChangelogTitle = fmt.Sprintf("Beta #%d Changelog (%s)", res.Beta.Build, res.Beta.Commit)
		res.Changelog = res.Beta.Changelog
		if res.Changelog == "" {
			res.Changelog = fmt.Sprintf("Rolling Beta build #%d (%s)", res.Beta.Build, res.Beta.Tag)
		}
	}
	return res
}

// ---------------------------------------------------------------------------
// Integrity helpers
// ---------------------------------------------------------------------------

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fetchSHA256(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "", errors.New("empty checksum file")
	}
	sum := strings.ToLower(fields[0])
	if len(sum) != 64 {
		return "", errors.New("invalid checksum format")
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", errors.New("invalid checksum format")
	}
	return sum, nil
}

// verifyChecksum compares the file with <fileURL>.sha256. A present-but-wrong checksum always fails;
// a missing checksum only passes when allowUnverified is set.
func verifyChecksum(client *http.Client, fileURL, path string, allowUnverified bool) error {
	expected, err := fetchSHA256(client, fileURL+".sha256")
	if err != nil {
		if allowUnverified {
			appendLog("[WARN] no checksum available (" + err.Error() + "); continuing because unverified updates are allowed")
			return nil
		}
		return fmt.Errorf("checksum file %s.sha256 is unavailable (%v). Publish it next to the package, or enable \"Allow unverified updates\" in Settings", fileURL, err)
	}
	actual, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("SHA-256 mismatch: the downloaded file is corrupted or has been tampered with")
	}
	appendLog("[OK] SHA-256 verified: " + actual)
	return nil
}

// downloadTo fetches url into a temp file. For a non-200 answer it returns ("", status, nil).
func downloadTo(client *http.Client, url, pattern string) (string, int, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", resp.StatusCode, err
	}
	n, copyErr := io.Copy(tmp, io.LimitReader(resp.Body, maxDownload+1))
	tmp.Close()
	if copyErr == nil && n > maxDownload {
		copyErr = fmt.Errorf("download exceeds the %d MB limit", maxDownload>>20)
	}
	if copyErr != nil {
		os.Remove(tmp.Name())
		return "", resp.StatusCode, copyErr
	}
	return tmp.Name(), resp.StatusCode, nil
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return false
	}
	return bytes.Equal(hdr, []byte{0x7f, 'E', 'L', 'F'})
}

// ---------------------------------------------------------------------------
// Archive handling
// ---------------------------------------------------------------------------

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	if len(r.File) > maxZipFiles {
		return errors.New("zip contains too many files")
	}

	cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)
	var total int64
	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest) && filepath.Clean(target) != filepath.Clean(destDir) {
			return fmt.Errorf("illegal path in zip: %s", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not allowed in update packages: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(rc, maxUnpacked-total+1))
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		total += n
		if total > maxUnpacked {
			return errors.New("zip expands beyond the allowed size")
		}
	}
	return nil
}

func findRoot(dir string) (binPath string, srcDir string) {
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == "mcontrol" || base == "mcontrol-linux-"+runtime.GOARCH || base == "mcontrol-linux-amd64" {
			if isELF(path) {
				binPath = path
			}
		}
		if base == "go.mod" && srcDir == "" {
			srcDir = filepath.Dir(path)
		}
		return nil
	})
	return binPath, srcDir
}

// replaceCurrentBinary swaps the running executable and keeps the previous one as <exe>.old
// so an operator can roll back manually.
func replaceCurrentBinary(newBinPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		exePath = "/opt/mcontrol/mcontrol"
	}
	if realPath, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = realPath
	}
	tmpDst := exePath + ".new"
	in, err := os.Open(newBinPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(tmpDst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmpDst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmpDst)
		return err
	}

	oldPath := exePath + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		_ = os.Remove(tmpDst)
		return err
	}
	if err := os.Rename(tmpDst, exePath); err != nil {
		_ = os.Rename(oldPath, exePath)
		_ = os.Remove(tmpDst)
		return err
	}
	return nil
}

func runStreamedCmd(dir, shellCmd string) error {
	ctx, cancel := context.WithTimeout(context.Background(), buildTimeoutMin*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", shellCmd)
	cmd.Dir = dir
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			appendLog(sc.Text())
		}
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

func applyZipInternal(zipPath string, enforceNewer bool) error {
	tmpDir, err := os.MkdirTemp("", "mcontrol-upd-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	setProgress(20, "Extracting ZIP archive...")
	if err := extractZip(zipPath, tmpDir); err != nil {
		return fmt.Errorf("invalid zip archive: %w", err)
	}

	binPath, srcDir := findRoot(tmpDir)
	if binPath != "" {
		setProgress(75, "Pre-built binary detected. Installing...")
		if cliBytes, err := os.ReadFile(filepath.Join(filepath.Dir(binPath), "mcontrol-cli.sh")); err == nil {
			_ = os.WriteFile("/usr/local/bin/mcontrol", cliBytes, 0755)
		}
		if err := replaceCurrentBinary(binPath); err != nil {
			return fmt.Errorf("failed to install binary: %w", err)
		}
		setProgress(95, "Binary replaced (previous one kept as mcontrol.old). Restarting MControl service...")
		return nil
	}

	if srcDir == "" {
		return errors.New("zip does not contain a valid mcontrol binary or Go source (go.mod)")
	}

	if enforceNewer {
		if raw, err := os.ReadFile(filepath.Join(srcDir, "VERSION")); err == nil {
			pkgVer := strings.TrimSpace(string(raw))
			if pkgVer != "" && version.Compare(pkgVer, version.Current) <= 0 {
				return fmt.Errorf("package version %s is not newer than the installed version %s", pkgVer, version.Current)
			}
		}
	}

	envPrefix := `export HOME=/root GOPATH=/root/go GOMODCACHE=/root/go/pkg/mod GOCACHE=/tmp/mcontrol-gocache PATH=$PATH:/usr/local/go/bin; mkdir -p "$GOCACHE" "$GOMODCACHE"; `

	setProgress(40, "Resolving Go modules (go mod tidy)...")
	if err := runStreamedCmd(srcDir, envPrefix+`go mod tidy`); err != nil {
		return fmt.Errorf("go mod tidy failed: %w", err)
	}

	setProgress(68, "Compiling MControl single binary (go build)...")
	if err := runStreamedCmd(srcDir, envPrefix+`CGO_ENABLED=0 GOOS=linux go build -v -trimpath -ldflags="-s -w" -o mcontrol .`); err != nil {
		return fmt.Errorf("go build failed: %w", err)
	}

	setProgress(90, "Installing compiled binary & CLI...")
	if cliBytes, err := os.ReadFile(filepath.Join(srcDir, "mcontrol-cli.sh")); err == nil {
		_ = os.WriteFile("/usr/local/bin/mcontrol", cliBytes, 0755)
	}
	if err := replaceCurrentBinary(filepath.Join(srcDir, "mcontrol")); err != nil {
		return fmt.Errorf("failed to install built binary: %w", err)
	}
	setProgress(96, "Update installed! Restarting MControl service...")
	return nil
}

// StartZipUpdate installs a package uploaded by the administrator (no checksum; the admin vouches for it).
func StartZipUpdate(zipPath string, onRestart func()) error {
	if !resetJob("Starting offline ZIP update...") {
		return errors.New("an update is already in progress")
	}
	go func() {
		defer os.Remove(zipPath)
		if err := applyZipInternal(zipPath, false); err != nil {
			finishJob(false, "Update failed", err.Error())
			return
		}
		finishJob(true, "Update complete! Restarting panel...", "")
		if onRestart != nil {
			onRestart()
		}
	}()
	return nil
}

// StartRemoteUpdate downloads and installs the latest package from the chosen source, verifying its SHA-256.
func StartRemoteUpdate(source, githubURL, mirrorURL, socksProxy string, allowUnverified bool, onBetaInstalled func(int), onRestart func()) error {
	if !resetJob("Starting remote update from " + source + "...") {
		return errors.New("an update is already in progress")
	}
	go func() {
		baseURL := normalizeReleaseURL(githubURL)
		betaBuildNum := 0
		if source == "mirror" {
			baseURL = strings.TrimRight(strings.TrimSpace(mirrorURL), "/")
		} else if source == "beta" {
			bs := fetchBetaStatus(githubURL, mirrorURL, socksProxy, 0)
			betaBuildNum = bs.Build
			ghRoot := releaseDownloadRoot(githubURL)
			if ghRoot != "" {
				baseURL = ghRoot + "/download/beta-latest"
			}
		}
		if baseURL == "" {
			finishJob(false, "Update failed", "update source URL is empty")
			return
		}
		if !validBase.MatchString(baseURL) {
			finishJob(false, "Update failed", "update source URL is invalid")
			return
		}

		activeProxy := socksProxy
		if source == "mirror" {
			activeProxy = ""
		}
		if activeProxy != "" {
			appendLog("[OUTBOUND] Routing download through Master Outbound (" + activeProxy + ")")
		}
		client := MakeHTTPClient(5*time.Minute, activeProxy)
		restart := func() {
			if onBetaInstalled != nil {
				if source == "beta" && betaBuildNum > 0 {
					onBetaInstalled(betaBuildNum)
				} else if source != "beta" {
					onBetaInstalled(0)
				}
			}
			finishJob(true, "Update complete! Restarting panel...", "")
			if onRestart != nil {
				onRestart()
			}
		}

		setProgress(12, "Downloading MControl-latest.zip from "+source+"...")
		zipName := "MControl-latest.zip"
		if runtime.GOARCH == "arm64" {
			zipName = "MControl-linux-arm64.zip"
		}
		zipURL := baseURL + "/" + zipName
		zipPath, status, err := downloadTo(client, zipURL, "mcontrol-remote-*.zip")
		if (err != nil || status != http.StatusOK) && source == "beta" {
			if ir := strings.TrimRight(strings.TrimSpace(mirrorURL), "/"); ir != "" && validBase.MatchString(ir) {
				appendLog("[MIRROR] GitHub beta-latest unreachable, falling back to Iran Mirror: " + ir + "/beta-latest")
				baseURL = ir + "/beta-latest"
				client = MakeHTTPClient(5*time.Minute, "")
				zipURL = baseURL + "/" + zipName
				zipPath, status, err = downloadTo(client, zipURL, "mcontrol-remote-*.zip")
			}
		}
		if err == nil && status == http.StatusOK {
			defer os.Remove(zipPath)
			setProgress(18, "Verifying package checksum...")
			if err := verifyChecksum(client, zipURL, zipPath, allowUnverified); err != nil {
				finishJob(false, "Update failed", err.Error())
				return
			}
			if err := applyZipInternal(zipPath, source != "beta"); err != nil {
				finishJob(false, "Update failed", err.Error())
				return
			}
			restart()
			return
		}

		setProgress(25, "ZIP not found, trying pre-built binary mcontrol-linux-amd64...")
		binURL := baseURL + "/mcontrol-linux-" + runtime.GOARCH
		binPath, status, err := downloadTo(client, binURL, "mcontrol-bin-*")
		if err != nil {
			finishJob(false, "Update failed", err.Error())
			return
		}
		if status != http.StatusOK {
			finishJob(false, "Update failed", fmt.Sprintf("remote package not found (HTTP %d)", status))
			return
		}
		defer os.Remove(binPath)
		setProgress(60, "Verifying binary checksum...")
		if err := verifyChecksum(client, binURL, binPath, allowUnverified); err != nil {
			finishJob(false, "Update failed", err.Error())
			return
		}
		if !isELF(binPath) {
			finishJob(false, "Update failed", "downloaded file is not a Linux executable")
			return
		}
		setProgress(85, "Installing downloaded binary...")
		if err := replaceCurrentBinary(binPath); err != nil {
			finishJob(false, "Update failed", err.Error())
			return
		}
		restart()
	}()
	return nil
}
