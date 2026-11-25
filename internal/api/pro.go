package api

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/sshclient"
	"github.com/htzserv/MControl/internal/sysinfo"
)

// ── 1. Replace Server / Disaster Recovery Wizard ─────────────────────────────

func (h *Handler) handleReplaceServer(w http.ResponseWriter, r *http.Request) {
	oldID, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		NewServerID    int64 `json:"new_server_id"`
		MigrateTunnels bool  `json:"migrate_tunnels"`
		MigrateDNS     bool  `json:"migrate_dns"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.NewServerID <= 0 || req.NewServerID == oldID {
		fail(w, http.StatusBadRequest, "select a different destination server")
		return
	}
	oldSrv, err := h.store.GetServerWithSecret(oldID)
	if err != nil {
		fail(w, http.StatusNotFound, "old server not found")
		return
	}
	newSrv, err := h.store.GetServerWithSecret(req.NewServerID)
	if err != nil {
		fail(w, http.StatusNotFound, "new server not found")
		return
	}

	var logs []string
	tunnelsMigrated := 0
	dnsUpdated := 0

	if req.MigrateTunnels {
		allTuns, _ := h.store.ListTunnels()
		var affected []int64
		for _, t := range allTuns {
			if t.IranServerID == oldID || t.ForeignServerID == oldID {
				affected = append(affected, t.ID)
			}
		}
		if len(affected) > 0 {
			n, err := h.store.ReplaceTunnelServerID(oldID, req.NewServerID)
			if err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			logs = append(logs, fmt.Sprintf("Reassigned %d tunnel reference(s) from %s -> %s", n, oldSrv.Name, newSrv.Name))
			for _, tid := range affected {
				tun, err := h.store.GetTunnel(tid)
				if err != nil {
					continue
				}
				irS, e1 := h.store.GetServerWithSecret(tun.IranServerID)
				frS, e2 := h.store.GetServerWithSecret(tun.ForeignServerID)
				if e1 != nil || e2 != nil {
					continue
				}
				ports := parseOpsPorts(tun.Ports)
				if len(ports) == 0 {
					ports = []int{443}
				}
				irCmd, frCmd := buildTunnelDeployScripts(tun, irS, frS, ports)
				var wg sync.WaitGroup
				var r1, r2 sshclient.Result
				wg.Add(2)
				go func() { defer wg.Done(); r1 = h.runOnServer(irS, irCmd, 45*time.Second) }()
				go func() { defer wg.Done(); r2 = h.runOnServer(frS, frCmd, 45*time.Second) }()
				wg.Wait()
				if r1.Err == nil && r2.Err == nil {
					_ = h.store.UpdateTunnelStatus(tid, "active", "")
					tunnelsMigrated++
					logs = append(logs, fmt.Sprintf("✔ Redeployed tunnel %s on %s ⇄ %s", tun.Name, irS.Name, frS.Name))
				} else {
					logs = append(logs, fmt.Sprintf("⚠ Tunnel %s redeploy partial error", tun.Name))
				}
			}
		} else {
			logs = append(logs, "No tunnels linked to old server.")
		}
	}

	if req.MigrateDNS {
		cache := h.loadCFCache()
		for _, rec := range cache.Records {
			matchV4 := rec.Type == "A" && (rec.Content == oldSrv.Host || (oldSrv.IPv4 != "" && rec.Content == oldSrv.IPv4))
			matchV6 := rec.Type == "AAAA" && oldSrv.IPv6 != "" && oldSrv.IPv6 != "N/A" && rec.Content == oldSrv.IPv6
			if !matchV4 && !matchV6 {
				continue
			}
			newIP := newSrv.Host
			if matchV6 {
				if newSrv.IPv6 == "" || newSrv.IPv6 == "N/A" {
					continue
				}
				newIP = newSrv.IPv6
			}
			if err := h.switchDNSRecordIP(rec.ZoneID, rec.ID, rec.Type, rec.Name, newIP); err == nil {
				dnsUpdated++
				logs = append(logs, fmt.Sprintf("✔ Updated DNS %s (%s) -> %s", rec.Name, rec.Type, newIP))
			} else {
				logs = append(logs, fmt.Sprintf("✖ DNS %s update failed: %v", rec.Name, err))
			}
		}
	}

	summary := fmt.Sprintf("Replaced %s -> %s (%d tunnels, %d DNS records)", oldSrv.Name, newSrv.Name, tunnelsMigrated, dnsUpdated)
	_ = h.store.AddIncident(0, oldSrv.Name+" → "+newSrv.Name, "replace", summary, 0)
	h.sendOpsAlert("🔄 <b>Disaster Recovery Complete</b>\n" + summary)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"tunnels_migrated": tunnelsMigrated,
		"dns_updated":      dnsUpdated,
		"message":          summary,
		"logs":             logs,
	})
}

// ── 2. Live Network Topology Map ─────────────────────────────────────────────

func (h *Handler) handleTopology(w http.ResponseWriter, r *http.Request) {
	srvs, _ := h.store.ListServers()
	tuns, _ := h.store.ListTunnels()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"servers": srvs,
		"tunnels": tuns,
	})
}

// ── 3. Multi-Node Reachability Matrix (IPv4 & IPv6) ──────────────────────────

func (h *Handler) handleReachabilityMatrix(w http.ResponseWriter, r *http.Request) {
	srvs, _ := h.store.ListServers()
	var entries, exits []db.Server
	for _, s := range srvs {
		if s.Role == "iran" {
			entries = append(entries, s)
		} else {
			exits = append(exits, s)
		}
	}
	if len(entries) == 0 || len(exits) == 0 {
		// Fallback: test all pairs among existing servers (max 6)
		entries = srvs
		exits = srvs
	}
	if len(entries) > 6 {
		entries = entries[:6]
	}
	if len(exits) > 6 {
		exits = exits[:6]
	}

	type cell struct {
		SrcID   int64  `json:"src_id"`
		SrcName string `json:"src_name"`
		DstID   int64  `json:"dst_id"`
		DstName string `json:"dst_name"`
		V4      string `json:"v4"`
		V6      string `json:"v6"`
	}
	var mu sync.Mutex
	cells := []cell{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)

	for _, src := range entries {
		srcFull, err := h.store.GetServerWithSecret(src.ID)
		if err != nil {
			continue
		}
		for _, dst := range exits {
			if src.ID == dst.ID {
				continue
			}
			wg.Add(1)
			go func(s *db.Server, d db.Server) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				v6Target := ""
				if d.IPv6 != "" && d.IPv6 != "N/A" {
					v6Target = d.IPv6
				}
				cmd := fmt.Sprintf(`P4=$(ping -4 -c 2 -W 2 %s 2>/dev/null | awk -F'/' 'END{print ($5==""?"FILTERED":$5"ms")}')
P6="N/A"
if [ -n %s ]; then P6=$(ping -6 -c 2 -W 2 %s 2>/dev/null | awk -F'/' 'END{print ($5==""?"FILTERED":$5"ms")}'); fi
echo "P4=$P4"
echo "P6=$P6"`, sshclient.ShQuote(d.Host), sshclient.ShQuote(v6Target), sshclient.ShQuote(v6Target))
				res := h.runOnServer(s, cmd, 16*time.Second)
				kv := parseKV(res.Output)
				v4 := kv["P4"]
				if v4 == "" {
					v4 = "ERR"
				}
				v6 := kv["P6"]
				if v6 == "" {
					v6 = "N/A"
				}
				mu.Lock()
				cells = append(cells, cell{
					SrcID: s.ID, SrcName: s.Name,
					DstID: d.ID, DstName: d.Name,
					V4: v4, V6: v6,
				})
				mu.Unlock()
			}(srcFull, dst)
		}
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cells": cells})
}

// ── 4. Web File Manager (List / Read / Write / Delete) ───────────────────────

func (h *Handler) handleServerFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Action  string `json:"action"` // list | read | write | mkdir | rename | copy | delete
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
		Content string `json:"content"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	p := strings.TrimSpace(req.Path)
	if p == "" {
		p = "/root"
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}

	switch req.Action {
	case "read":
		cmd := fmt.Sprintf(`head -c 200000 %s 2>&1`, sshclient.ShQuote(p))
		res := h.runOnServer(srv, cmd, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "content": res.Output, "path": p})
	case "write":
		b64 := base64.StdEncoding.EncodeToString([]byte(req.Content))
		cmd := fmt.Sprintf(`printf '%%s' %s | base64 -d > %s && echo "Saved %s"`, sshclient.ShQuote(b64), sshclient.ShQuote(p), p)
		res := h.runOnServer(srv, cmd, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "mkdir":
		cmd := fmt.Sprintf(`mkdir -p %s 2>&1 && echo "Created folder"`, sshclient.ShQuote(p))
		res := h.runOnServer(srv, cmd, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "rename":
		np := strings.TrimSpace(req.NewPath)
		if np == "" {
			fail(w, http.StatusBadRequest, "new_path is required")
			return
		}
		cmd := fmt.Sprintf(`mv %s %s 2>&1 && echo "Renamed"`, sshclient.ShQuote(p), sshclient.ShQuote(np))
		res := h.runOnServer(srv, cmd, 15*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "copy":
		np := strings.TrimSpace(req.NewPath)
		if np == "" {
			fail(w, http.StatusBadRequest, "new_path is required")
			return
		}
		cmd := fmt.Sprintf(`cp -a %s %s 2>&1 && echo "Copied"`, sshclient.ShQuote(p), sshclient.ShQuote(np))
		res := h.runOnServer(srv, cmd, 25*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	case "delete":
		cleanP := strings.TrimRight(p, "/")
		if cleanP == "" || cleanP == "/" || cleanP == "/etc" || cleanP == "/root" || cleanP == "/usr" || cleanP == "/var" || cleanP == "/home" || cleanP == "/bin" || cleanP == "/sbin" || cleanP == "/lib" || cleanP == "/boot" || cleanP == "/opt" || cleanP == "/tmp" {
			fail(w, http.StatusBadRequest, "refusing to delete protected path")
			return
		}
		cmd := fmt.Sprintf(`rm -rf %s 2>&1 && echo "Deleted"`, sshclient.ShQuote(p))
		res := h.runOnServer(srv, cmd, 20*time.Second)
		writeJSON(w, http.StatusOK, map[string]any{"ok": res.Err == nil, "message": strings.TrimSpace(res.Output)})
	default: // list
		cmd := fmt.Sprintf(`if [ ! -d %s ]; then exit 1; fi
find %s -mindepth 1 -maxdepth 1 -printf '%%y\t%%s\t%%f\n' 2>/dev/null | awk -F'\t' '{t=($1=="d"?"dir":"file"); print (t=="dir"?"0":"1")"\t"t"\t"$2"\t"$3}' | sort -t$'\t' -k1,1 -k4,4f | cut -f2-4 | head -n 200`, sshclient.ShQuote(p), sshclient.ShQuote(p))
		res := h.runOnServer(srv, cmd, 15*time.Second)
		type entry struct {
			Type string `json:"type"`
			Size string `json:"size"`
			Name string `json:"name"`
		}
		items := []entry{}
		for _, ln := range strings.Split(res.Output, "\n") {
			parts := strings.SplitN(strings.TrimSpace(ln), "\t", 3)
			if len(parts) == 3 {
				items = append(items, entry{Type: parts[0], Size: parts[1], Name: parts[2]})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": p, "items": items})
	}
}

func (h *Handler) handleServerFileDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		fail(w, http.StatusBadRequest, "path is required")
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	// Check file exists, is regular file, and <= 20MB, then output base64
	cmd := fmt.Sprintf(`if [ ! -f %s ]; then echo "ERR:NOT_FILE"; exit 1; fi
SZ=$(stat -c %%s %s 2>/dev/null || wc -c < %s)
if [ "$SZ" -gt 20971520 ]; then echo "ERR:TOO_LARGE"; exit 1; fi
base64 %s`, sshclient.ShQuote(p), sshclient.ShQuote(p), sshclient.ShQuote(p), sshclient.ShQuote(p))
	res := h.runOnServer(srv, cmd, 45*time.Second)
	if res.Err != nil {
		out := strings.TrimSpace(res.Output)
		if strings.Contains(out, "ERR:NOT_FILE") {
			fail(w, http.StatusBadRequest, "path is not a downloadable file")
			return
		}
		if strings.Contains(out, "ERR:TOO_LARGE") {
			fail(w, http.StatusRequestEntityTooLarge, "file exceeds 20MB web download limit")
			return
		}
		fail(w, http.StatusBadGateway, "download failed: "+res.Err.Error())
		return
	}
	cleanB64 := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, res.Output)
	raw, err := base64.StdEncoding.DecodeString(cleanB64)
	if err != nil {
		fail(w, http.StatusInternalServerError, "failed to decode remote file: "+err.Error())
		return
	}
	fname := filepath.Base(p)
	if fname == "" || fname == "." || fname == "/" {
		fname = "download.bin"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(fname, `"`, "")))
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	_, _ = w.Write(raw)
}

func (h *Handler) handleServerFileUpload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	srv, err := h.store.GetServerWithSecret(id)
	if err != nil {
		fail(w, http.StatusNotFound, "server not found")
		return
	}
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		fail(w, http.StatusBadRequest, "failed to parse upload (max 25MB): "+err.Error())
		return
	}
	dir := strings.TrimSpace(r.FormValue("path"))
	if dir == "" {
		dir = "/root"
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		fail(w, http.StatusBadRequest, "no file provided")
		return
	}
	uploaded := []string{}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			fail(w, http.StatusBadRequest, "cannot read uploaded file: "+err.Error())
			return
		}
		data, rerr := io.ReadAll(io.LimitReader(f, 20<<20))
		_ = f.Close()
		if rerr != nil {
			fail(w, http.StatusBadRequest, "read error: "+rerr.Error())
			return
		}
		fname := filepath.Base(fh.Filename)
		if fname == "" || fname == "." || fname == ".." {
			continue
		}
		dest := strings.TrimRight(dir, "/") + "/" + fname
		// Chunked base64 upload (<= 48KB raw per chunk -> ~64KB base64) to avoid ARG_MAX & Agent 64KB exec body limit
		const chunkRaw = 36 * 1024
		initCmd := fmt.Sprintf("mkdir -p %s && : > %s", sshclient.ShQuote(dir), sshclient.ShQuote(dest))
		if r0 := h.runOnServer(srv, initCmd, 15*time.Second); r0.Err != nil {
			fail(w, http.StatusBadGateway, "upload init failed for "+fname+": "+r0.Err.Error())
			return
		}
		for off := 0; off < len(data); off += chunkRaw {
			end := off + chunkRaw
			if end > len(data) {
				end = len(data)
			}
			b64 := base64.StdEncoding.EncodeToString(data[off:end])
			cmd := fmt.Sprintf("base64 -d << 'MC_UPLOAD_EOF' >> %s\n%s\nMC_UPLOAD_EOF", sshclient.ShQuote(dest), b64)
			res := h.runOnServer(srv, cmd, 30*time.Second)
			if res.Err != nil {
				fail(w, http.StatusBadGateway, "upload failed for "+fname+": "+res.Err.Error())
				return
			}
		}
		uploaded = append(uploaded, fname)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"files":   uploaded,
		"message": fmt.Sprintf("Uploaded %d file(s) to %s", len(uploaded), dir),
	})
}

func isProtectedFMPath(p string) bool {
	cleanP := strings.TrimRight(filepath.Clean(p), "/")
	switch cleanP {
	case "", "/", "/etc", "/root", "/usr", "/var", "/home", "/bin", "/sbin", "/lib", "/boot", "/opt", "/tmp":
		return true
	}
	return false
}

func copyLocalRecursive(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(link, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyLocalRecursive(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	_ = out.Close()
	return cerr
}

func (h *Handler) handleLocalFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action  string `json:"action"` // list | read | write | mkdir | rename | copy | delete
		Path    string `json:"path"`
		NewPath string `json:"new_path"`
		Content string `json:"content"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	p := strings.TrimSpace(req.Path)
	if p == "" {
		p = "/root"
	}
	p = filepath.Clean(p)

	switch req.Action {
	case "read":
		f, err := os.Open(p)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 200000))
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "content": string(data), "path": p})
	case "write":
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := os.WriteFile(p, []byte(req.Content), 0o644); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Saved " + p})
	case "mkdir":
		if err := os.MkdirAll(p, 0o755); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Created folder"})
	case "rename":
		np := strings.TrimSpace(req.NewPath)
		if np == "" {
			fail(w, http.StatusBadRequest, "new_path is required")
			return
		}
		if err := os.Rename(p, filepath.Clean(np)); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Renamed"})
	case "copy":
		np := strings.TrimSpace(req.NewPath)
		if np == "" {
			fail(w, http.StatusBadRequest, "new_path is required")
			return
		}
		if err := copyLocalRecursive(p, filepath.Clean(np)); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Copied"})
	case "delete":
		if isProtectedFMPath(p) {
			fail(w, http.StatusBadRequest, "refusing to delete protected path")
			return
		}
		if err := os.RemoveAll(p); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Deleted"})
	default: // list
		entries, err := os.ReadDir(p)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		type entry struct {
			Type string `json:"type"`
			Size string `json:"size"`
			Name string `json:"name"`
		}
		items := make([]entry, 0, len(entries))
		for _, e := range entries {
			typ := "file"
			sz := int64(0)
			if e.IsDir() {
				typ = "dir"
			} else if info, err := e.Info(); err == nil {
				sz = info.Size()
			}
			items = append(items, entry{Type: typ, Size: strconv.FormatInt(sz, 10), Name: e.Name()})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Type != items[j].Type {
				return items[i].Type == "dir"
			}
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
		if len(items) > 200 {
			items = items[:200]
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": p, "items": items})
	}
}

func (h *Handler) handleLocalFileDownload(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		fail(w, http.StatusBadRequest, "path is required")
		return
	}
	p = filepath.Clean(p)
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		fail(w, http.StatusBadRequest, "path is not a downloadable file")
		return
	}
	if info.Size() > 20<<20 {
		fail(w, http.StatusRequestEntityTooLarge, "file exceeds 20MB web download limit")
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	fname := filepath.Base(p)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(fname, `"`, "")))
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	_, _ = w.Write(raw)
}

func (h *Handler) handleLocalFileUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		fail(w, http.StatusBadRequest, "failed to parse upload (max 25MB): "+err.Error())
		return
	}
	dir := strings.TrimSpace(r.FormValue("path"))
	if dir == "" {
		dir = "/root"
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		fail(w, http.StatusBadRequest, "no file provided")
		return
	}
	uploaded := []string{}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			fail(w, http.StatusBadRequest, "cannot read uploaded file: "+err.Error())
			return
		}
		data, rerr := io.ReadAll(io.LimitReader(f, 20<<20))
		_ = f.Close()
		if rerr != nil {
			fail(w, http.StatusBadRequest, "read error: "+rerr.Error())
			return
		}
		fname := filepath.Base(fh.Filename)
		if fname == "" || fname == "." || fname == ".." {
			continue
		}
		dest := filepath.Join(dir, fname)
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			fail(w, http.StatusInternalServerError, "write failed for "+fname+": "+err.Error())
			return
		}
		uploaded = append(uploaded, fname)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"files":   uploaded,
		"message": fmt.Sprintf("Uploaded %d file(s) to %s", len(uploaded), dir),
	})
}

// ── 5. Script Vault ──────────────────────────────────────────────────────────

type VaultScript struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

func (h *Handler) handleScriptVault(w http.ResponseWriter, r *http.Request) {
	opsMu.Lock()
	defer opsMu.Unlock()
	raw := h.getSettingDef("script_vault_json", "[]")
	var list []VaultScript
	_ = json.Unmarshal([]byte(raw), &list)
	if list == nil {
		list = []VaultScript{}
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scripts": list})
		return
	}
	if r.Method == http.MethodDelete {
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		kept := []VaultScript{}
		for _, s := range list {
			if s.ID != id {
				kept = append(kept, s)
			}
		}
		b, _ := json.Marshal(kept)
		_ = h.store.SetSetting("script_vault_json", string(b))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scripts": kept})
		return
	}
	var req VaultScript
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Command) == "" {
		fail(w, http.StatusBadRequest, "script name and command are required")
		return
	}
	req.ID = time.Now().UnixNano() / 1e6
	list = append(list, req)
	b, _ := json.Marshal(list)
	_ = h.store.SetSetting("script_vault_json", string(b))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "scripts": list})
}

// ── 6. Server Tags / Groups ──────────────────────────────────────────────────

func (h *Handler) handleServerTags(w http.ResponseWriter, r *http.Request) {
	opsMu.Lock()
	defer opsMu.Unlock()
	raw := h.getSettingDef("server_tags_json", "{}")
	m := map[string][]string{}
	_ = json.Unmarshal([]byte(raw), &m)
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tags": m})
		return
	}
	var req struct {
		ServerID int64    `json:"server_id"`
		Tags     []string `json:"tags"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	clean := []string{}
	for _, t := range req.Tags {
		t = strings.TrimSpace(t)
		if t != "" {
			clean = append(clean, t)
		}
	}
	key := strconv.FormatInt(req.ServerID, 10)
	if len(clean) == 0 {
		delete(m, key)
	} else {
		m[key] = clean
	}
	b, _ := json.Marshal(m)
	_ = h.store.SetSetting("server_tags_json", string(b))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tags": m})
}

// ── 7. Bandwidth Quota Guard ─────────────────────────────────────────────────

type ServerQuota struct {
	ServerID      int64   `json:"server_id"`
	Provider      string  `json:"provider"`
	ExpiryDate    string  `json:"expiry_date"` // YYYY-MM-DD
	WarnDays      int     `json:"warn_days"`
	LimitGB       float64 `json:"limit_gb"`
	UsedGB        float64 `json:"used_gb"`
	ResetDay      int     `json:"reset_day"`
	LastMonth     int     `json:"last_month"`
	Alerted80     bool    `json:"alerted_80"`
	Alerted95     bool    `json:"alerted_95"`
	AlertedExpiry bool    `json:"alerted_expiry"`
}

func (h *Handler) loadQuotas() map[string]ServerQuota {
	raw := h.getSettingDef("server_quotas_json", "{}")
	m := map[string]ServerQuota{}
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

func (h *Handler) saveQuotas(m map[string]ServerQuota) {
	b, _ := json.Marshal(m)
	_ = h.store.SetSetting("server_quotas_json", string(b))
}

func (h *Handler) handleQuotas(w http.ResponseWriter, r *http.Request) {
	opsMu.Lock()
	defer opsMu.Unlock()
	m := h.loadQuotas()
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quotas": m})
		return
	}
	var req ServerQuota
	if !readJSON(w, r, &req) {
		return
	}
	key := strconv.FormatInt(req.ServerID, 10)
	req.Provider = strings.TrimSpace(req.Provider)
	req.ExpiryDate = strings.TrimSpace(req.ExpiryDate)
	if req.LimitGB <= 0 && req.ExpiryDate == "" && req.Provider == "" {
		delete(m, key)
	} else {
		if req.ResetDay < 1 || req.ResetDay > 28 {
			req.ResetDay = 1
		}
		if req.WarnDays <= 0 {
			req.WarnDays = 3
		}
		if cur, ok := m[key]; ok {
			if req.UsedGB <= 0 && cur.UsedGB > 0 && req.LimitGB > 0 {
				req.UsedGB = cur.UsedGB
			}
			req.LastMonth = cur.LastMonth
			if cur.ExpiryDate == req.ExpiryDate {
				req.AlertedExpiry = cur.AlertedExpiry
			}
		}
		m[key] = req
	}
	h.saveQuotas(m)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quotas": m})
}

// ── 8. DNS Auto-Failover Test Switch ─────────────────────────────────────────

func (h *Handler) handleFailoverTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RuleID int64 `json:"rule_id"`
		ToBack bool  `json:"to_backup"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	opsMu.Lock()
	auto := h.loadOpsAuto()
	opsMu.Unlock()
	for i := range auto.FailoverRules {
		rule := &auto.FailoverRules[i]
		if rule.ID != req.RuleID {
			continue
		}
		targetSrvID := rule.PrimaryServerID
		if req.ToBack {
			targetSrvID = rule.BackupServerID
		}
		srv, err := h.store.GetServerWithSecret(targetSrvID)
		if err != nil {
			fail(w, http.StatusNotFound, "target server not found")
			return
		}
		newIP := srv.Host
		if rule.RecordType == "AAAA" && srv.IPv6 != "" && srv.IPv6 != "N/A" {
			newIP = srv.IPv6
		}
		if err := h.switchDNSRecordIP(rule.ZoneID, rule.RecordID, rule.RecordType, rule.RecordName, newIP); err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		rule.ActiveOnBackup = req.ToBack
		opsMu.Lock()
		h.saveOpsAuto(auto)
		opsMu.Unlock()
		_ = h.store.AddIncident(0, rule.RecordName, "failover", fmt.Sprintf("Manual DNS switch -> %s (%s)", newIP, srv.Name), 0)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": fmt.Sprintf("Switched %s -> %s (%s)", rule.RecordName, newIP, srv.Name)})
		return
	}
	fail(w, http.StatusNotFound, "failover rule not found")
}

// ── 9. Auto SSL Cert Sync ────────────────────────────────────────────────────

func (h *Handler) handleSSLSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ServerIDs []int64 `json:"server_ids"`
		CertPEM   string  `json:"cert_pem"`
		KeyPEM    string  `json:"key_pem"`
		DestDir   string  `json:"dest_dir"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.ServerIDs) == 0 {
		fail(w, http.StatusBadRequest, "select at least one server")
		return
	}
	cert := strings.TrimSpace(req.CertPEM)
	key := strings.TrimSpace(req.KeyPEM)
	if cert == "" || key == "" {
		b1, e1 := os.ReadFile(filepath.Join(h.store.DataDir(), "tls.crt"))
		b2, e2 := os.ReadFile(filepath.Join(h.store.DataDir(), "tls.key"))
		if e1 != nil || e2 != nil {
			fail(w, http.StatusBadRequest, "Master TLS cert not found; paste Cert & Key PEM manually")
			return
		}
		cert = string(b1)
		key = string(b2)
	}
	dest := strings.TrimSpace(req.DestDir)
	if dest == "" {
		dest = "/etc/mcontrol/ssl"
	}
	cB64 := base64.StdEncoding.EncodeToString([]byte(cert))
	kB64 := base64.StdEncoding.EncodeToString([]byte(key))
	cmd := fmt.Sprintf(`mkdir -p %s && printf '%%s' %s | base64 -d > %s/fullchain.pem && printf '%%s' %s | base64 -d > %s/privkey.pem && chmod 600 %s/privkey.pem && echo "SSL synced to %s"`,
		sshclient.ShQuote(dest), sshclient.ShQuote(cB64), sshclient.ShQuote(dest), sshclient.ShQuote(kB64), sshclient.ShQuote(dest), sshclient.ShQuote(dest), dest)

	var logs []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, sid := range req.ServerIDs {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			srv, err := h.store.GetServerWithSecret(id)
			if err != nil {
				return
			}
			res := h.runOnServer(srv, cmd, 20*time.Second)
			mu.Lock()
			if res.Err == nil {
				logs = append(logs, "✔ "+srv.Name+": "+strings.TrimSpace(res.Output))
			} else {
				logs = append(logs, "✖ "+srv.Name+": "+res.Err.Error())
			}
			mu.Unlock()
		}(sid)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "logs": logs, "message": fmt.Sprintf("Synced SSL to %d server(s).", len(logs))})
}

// ── 10. 3x-ui / Marzban Active Inbound Port Sync ─────────────────────────────

func (h *Handler) handlePanelPortSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PanelType string `json:"panel_type"` // 3x-ui | marzban
		BaseURL   string `json:"base_url"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	base := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if base == "" || req.Username == "" || req.Password == "" {
		fail(w, http.StatusBadRequest, "panel URL, username, and password are required")
		return
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Timeout: 15 * time.Second,
		Jar:     jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	portsSet := map[int]bool{}
	ports := []int{}
	addPort := func(p int) {
		if p > 0 && p <= 65535 && !portsSet[p] {
			portsSet[p] = true
			ports = append(ports, p)
		}
	}

	if req.PanelType == "marzban" {
		form := url.Values{"username": {req.Username}, "password": {req.Password}}
		resp, err := client.PostForm(base+"/api/admin/token", form)
		if err != nil {
			fail(w, http.StatusBadGateway, "Marzban login failed: "+err.Error())
			return
		}
		defer resp.Body.Close()
		var tokRes struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&tokRes)
		if tokRes.AccessToken == "" {
			fail(w, http.StatusUnauthorized, "Marzban authentication failed")
			return
		}
		ireq, _ := http.NewRequest(http.MethodGet, base+"/api/inbounds", nil)
		ireq.Header.Set("Authorization", "Bearer "+tokRes.AccessToken)
		iresp, err := client.Do(ireq)
		if err != nil {
			fail(w, http.StatusBadGateway, err.Error())
			return
		}
		defer iresp.Body.Close()
		var raw map[string][]struct {
			Port int `json:"port"`
		}
		_ = json.NewDecoder(iresp.Body).Decode(&raw)
		for _, arr := range raw {
			for _, inb := range arr {
				addPort(inb.Port)
			}
		}
	} else {
		// 3x-ui
		body, _ := json.Marshal(map[string]string{"username": req.Username, "password": req.Password})
		resp, err := client.Post(base+"/login", "application/json", bytes.NewReader(body))
		if err != nil {
			fail(w, http.StatusBadGateway, "3x-ui login failed: "+err.Error())
			return
		}
		_, _ = io.ReadAll(resp.Body)
		resp.Body.Close()

		iresp, err := client.Get(base + "/panel/api/inbounds/list")
		if err != nil {
			fail(w, http.StatusBadGateway, "3x-ui list failed: "+err.Error())
			return
		}
		defer iresp.Body.Close()
		var listRes struct {
			Success bool `json:"success"`
			Obj     []struct {
				Port   int  `json:"port"`
				Enable bool `json:"enable"`
			} `json:"obj"`
		}
		if err := json.NewDecoder(iresp.Body).Decode(&listRes); err != nil || !listRes.Success {
			fail(w, http.StatusUnauthorized, "3x-ui authentication or API response failed")
			return
		}
		for _, inb := range listRes.Obj {
			if inb.Enable {
				addPort(inb.Port)
			}
		}
	}

	strs := make([]string, 0, len(ports))
	for _, p := range ports {
		strs = append(strs, strconv.Itoa(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"ports":     ports,
		"ports_csv": strings.Join(strs, ","),
		"count":     len(ports),
	})
}

// ── 11. 24h / 7d Metrics History + Sampler Daemon ────────────────────────────

func parsePct(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseMs(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.ToLower(s), "ms"))
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func (h *Handler) handleMetricsHistory(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if target == "" {
		target = "master"
	}
	rng := r.URL.Query().Get("range")
	dur := int64(86400)
	if rng == "7d" {
		dur = 7 * 86400
	}
	pts, err := h.store.ListMetricPoints(target, time.Now().Unix()-dur)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "target": target, "points": pts})
}

func (h *Handler) StartProMetricsDaemon() {
	go func() {
		lastPrune := time.Now()
		for {
			if h.isStopped() {
				return
			}
			// Sample Master
			st := sysinfo.Collect()
			_ = h.store.AddMetricPoint("master", st.CPUPercent, st.RAMPercent, 0, int64(st.NetRxTotal), int64(st.NetTxTotal))

			// Sample Servers + check Bandwidth Quota Guard
			srvs, _ := h.store.ListServers()
			opsMu.Lock()
			quotas := h.loadQuotas()
			qChanged := false
			curMonth := int(time.Now().Month())
			for _, s := range srvs {
				_ = h.store.AddMetricPoint(fmt.Sprintf("srv:%d", s.ID), parsePct(s.CPU), parsePct(s.RAM), parseMs(s.PingMs), 0, 0)
				key := strconv.FormatInt(s.ID, 10)
				if q, ok := quotas[key]; ok {
					if q.LimitGB > 0 {
						if q.LastMonth != curMonth && time.Now().Day() >= q.ResetDay {
							q.UsedGB = 0
							q.LastMonth = curMonth
							q.Alerted80 = false
							q.Alerted95 = false
							qChanged = true
						}
						pct := (q.UsedGB / q.LimitGB) * 100.0
						if pct >= 95 && !q.Alerted95 {
							q.Alerted95 = true
							qChanged = true
							msg := fmt.Sprintf("🚨 Bandwidth Quota 95%% reached on %s (%.1f / %.1f GB)", s.Name, q.UsedGB, q.LimitGB)
							_ = h.store.AddIncident(0, s.Name, "quota", msg, 0)
							h.sendOpsAlert(msg)
						} else if pct >= 80 && !q.Alerted80 {
							q.Alerted80 = true
							qChanged = true
							msg := fmt.Sprintf("⚠️ Bandwidth Quota 80%% reached on %s (%.1f / %.1f GB)", s.Name, q.UsedGB, q.LimitGB)
							_ = h.store.AddIncident(0, s.Name, "quota", msg, 0)
							h.sendOpsAlert(msg)
						}
					}
					if q.ExpiryDate != "" && !q.AlertedExpiry {
						if expT, err := time.Parse("2006-01-02", q.ExpiryDate); err == nil {
							daysLeft := int(time.Until(expT).Hours() / 24)
							warn := q.WarnDays
							if warn <= 0 {
								warn = 3
							}
							if daysLeft <= warn {
								q.AlertedExpiry = true
								qChanged = true
								msg := fmt.Sprintf("📅 VPS Expiry Alert: %s (%s) expires on %s (%d days left)", s.Name, q.Provider, q.ExpiryDate, daysLeft)
								_ = h.store.AddIncident(0, s.Name, "expiry", msg, 0)
								h.sendOpsAlert(msg)
							}
						}
					}
					quotas[key] = q
				}
			}
			if qChanged {
				h.saveQuotas(quotas)
			}
			opsMu.Unlock()

			if time.Since(lastPrune) > 6*time.Hour {
				lastPrune = time.Now()
				h.store.PruneMetricPoints(7)
			}
			time.Sleep(5 * time.Minute)
		}
	}()
}

// ── 12. MikroTik RouterOS GRE / IP6GRE Config Generator ──────────────────────

func (h *Handler) handleTunnelMikrotik(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tun, err := h.store.GetTunnel(id)
	if err != nil {
		fail(w, http.StatusNotFound, "tunnel not found")
		return
	}
	iranSrv, _ := h.store.GetServerWithSecret(tun.IranServerID)
	forSrv, _ := h.store.GetServerWithSecret(tun.ForeignServerID)
	localIP := "YOUR_MIKROTIK_PUBLIC_IP"
	remoteIP := "FOREIGN_SERVER_IP"
	if iranSrv != nil {
		localIP = iranSrv.Host
		if tun.Engine == "ip6gre" && iranSrv.IPv6 != "" && iranSrv.IPv6 != "N/A" {
			localIP = iranSrv.IPv6
		}
	}
	if forSrv != nil {
		remoteIP = forSrv.Host
		if tun.Engine == "ip6gre" && forSrv.IPv6 != "" && forSrv.IPv6 != "N/A" {
			remoteIP = forSrv.IPv6
		}
	}
	sub := tun.Subnet
	if sub == "" {
		sub = "10.10.10"
	}
	ifName := "mcontrol-" + tun.Name
	var script string
	if tun.Engine == "ip6gre" {
		script = fmt.Sprintf(`# MikroTik RouterOS v7 — IPv6 GRE Tunnel (%s)
/interface gre6 add name=%s local-address=%s remote-address=%s mtu=1420 clamp-tcp-mss=yes disabled=no
/ip address add address=%s.1/30 interface=%s
/ip firewall mangle add chain=forward out-interface=%s protocol=tcp tcp-flags=syn action=change-mss new-mss=clamp-to-pmtu
/ip firewall mangle add chain=forward in-interface=%s protocol=tcp tcp-flags=syn action=change-mss new-mss=clamp-to-pmtu
# Test peer: /ping %s.2`, tun.Name, ifName, localIP, remoteIP, sub, ifName, ifName, ifName, sub)
	} else {
		script = fmt.Sprintf(`# MikroTik RouterOS v7 — GRE Tunnel (%s)
/interface gre add name=%s local-address=%s remote-address=%s mtu=1440 clamp-tcp-mss=yes keepalive=10s,5 disabled=no
/ip address add address=%s.1/30 interface=%s
/ip firewall mangle add chain=forward out-interface=%s protocol=tcp tcp-flags=syn action=change-mss new-mss=clamp-to-pmtu
/ip firewall mangle add chain=forward in-interface=%s protocol=tcp tcp-flags=syn action=change-mss new-mss=clamp-to-pmtu
# Test peer: /ping %s.2`, tun.Name, ifName, localIP, remoteIP, sub, ifName, ifName, ifName, sub)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "script": script})
}
