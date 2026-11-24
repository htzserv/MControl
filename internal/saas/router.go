package saas

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/api"
	"github.com/htzserv/MControl/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func bearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

// isHostOnlyEndpoint blocks host-level / SuperAdmin-only actions when called by a tenant.
func isHostOnlyEndpoint(method, path string) bool {
	if strings.HasPrefix(path, "/api/update/") ||
		strings.HasPrefix(path, "/api/cache/") ||
		strings.HasPrefix(path, "/api/outbound/") ||
		strings.HasPrefix(path, "/api/settings/tls") ||
		path == "/api/settings/address" ||
		path == "/api/users/viewer" ||
		path == "/api/backup/restore" {
		return true
	}
	if strings.HasPrefix(path, "/api/local/") && path != "/api/local/stats" {
		return true
	}
	return false
}

// Wrap wraps the primary v1 innerMux with full multi-tenant routing, quota enforcement,
// SuperAdmin tenant management (/api/saas/*), and per-user Drag & Drop order persistence.
func (m *Manager) Wrap(primaryMux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		// 1. SaaS Control Plane & Per-User Drag-and-Drop Order endpoints
		if strings.HasPrefix(p, "/api/saas/") {
			m.handleSaaS(w, r)
			return
		}

		// 2. Unified Login: check tenant registry first, then delegate to SuperAdmin login
		if p == "/api/login" && r.Method == http.MethodPost {
			m.handleUnifiedLogin(w, r, primaryMux)
			return
		}

		// 3. Tenant-authenticated requests ("Bearer sa.<...>")
		tok := bearerToken(r)
		if strings.HasPrefix(tok, "sa.") && strings.HasPrefix(p, "/api/") {
			m.handleTenantAPI(w, r, tok)
			return
		}

		// 4. Default: SuperAdmin / Public static UI
		primaryMux.ServeHTTP(w, r)
	})
}

// ServePublicStatus routes /status, /status/<username>, /api/public/status, /api/public/status/<username>.
func (m *Manager) ServePublicStatus(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimRight(r.URL.Path, "/")
	var username string
	if strings.HasPrefix(p, "/status/") {
		username = strings.TrimPrefix(p, "/status/")
	} else if strings.HasPrefix(p, "/api/public/status/") {
		username = strings.TrimPrefix(p, "/api/public/status/")
	}
	if username == "" || strings.Contains(username, "/") {
		m.primary.ServePublicStatus(w, r)
		return
	}
	t, err := m.GetTenantByUsername(username)
	if err != nil || !t.Active {
		http.NotFound(w, r)
		return
	}
	inst, err := m.getOrOpenTenant(t.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inst.handler.ServePublicStatus(w, r)
}

// ServeDist serves /_mc_dist/<token>/<path> checking SuperAdmin cache first, then active tenants.
func (m *Manager) ServeDist(w http.ResponseWriter, r *http.Request) {
	if m.primary.HasDistToken(r) {
		m.primary.ServeDist(w, r)
		return
	}
	m.mu.RLock()
	var matched *api.Handler
	for _, inst := range m.instances {
		if inst.handler.HasDistToken(r) {
			matched = inst.handler
			break
		}
	}
	m.mu.RUnlock()
	if matched != nil {
		matched.ServeDist(w, r)
		return
	}
	http.NotFound(w, r)
}

func (m *Manager) handleUnifiedLogin(w http.ResponseWriter, r *http.Request, primaryMux http.Handler) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.Unmarshal(body, &req) == nil && strings.TrimSpace(req.Username) != "" {
		if t, err := m.GetTenantByUsername(req.Username); err == nil {
			if bcrypt.CompareHashAndPassword([]byte(t.PassHash), []byte(req.Password)) != nil {
				time.Sleep(350 * time.Millisecond)
				fail(w, http.StatusUnauthorized, "invalid credentials")
				return
			}
			if !t.Active {
				fail(w, http.StatusForbidden, "account suspended by administrator")
				return
			}
			if t.ExpiresAt > 0 && time.Now().Unix() > t.ExpiresAt {
				fail(w, http.StatusForbidden, "subscription expired")
				return
			}
			if _, err := m.getOrOpenTenant(t.ID); err != nil {
				fail(w, http.StatusInternalServerError, "failed to open tenant workspace")
				return
			}
			tok := m.IssueTenantToken(t, "")
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":         true,
				"token":      tok,
				"role":       "tenant",
				"expires_in": 12 * 3600,
			})
			return
		}
	}
	// Restore body and delegate to SuperAdmin / Viewer login
	r.Body = io.NopCloser(bytes.NewReader(body))
	primaryMux.ServeHTTP(w, r)
}

func (m *Manager) handleTenantAPI(w http.ResponseWriter, r *http.Request, tok string) {
	claims, tenant, ok := m.VerifyTenantToken(tok)
	if !ok {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.URL.Path == "/api/logout" && r.Method == http.MethodPost {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if isHostOnlyEndpoint(r.Method, r.URL.Path) {
		fail(w, http.StatusForbidden, "host-level action restricted to SuperAdmin")
		return
	}
	inst, err := m.getOrOpenTenant(tenant.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "tenant database unavailable")
		return
	}

	// Enforce per-tenant commercial quotas on resource creation
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/api/servers":
			if srvs, err := inst.store.ListServers(); err == nil && len(srvs) >= tenant.MaxServers {
				fail(w, http.StatusForbidden, "server quota limit reached for your plan")
				return
			}
		case "/api/tunnels":
			if tuns, err := inst.store.ListTunnels(); err == nil && len(tuns) >= tenant.MaxTunnels {
				fail(w, http.StatusForbidden, "tunnel quota limit reached for your plan")
				return
			}
		case "/api/monitors":
			if mons, err := inst.store.ListMonitors(); err == nil && len(mons) >= tenant.MaxMonitors {
				fail(w, http.StatusForbidden, "monitor quota limit reached for your plan")
				return
			}
		}
	}

	// Safe non-sensitive local stats stub for tenants so Overview never leaks host details
	if r.URL.Path == "/api/local/stats" && r.Method == http.MethodGet {
		srvs, _ := inst.store.ListServers()
		tuns, _ := inst.store.ListTunnels()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"stats": map[string]any{
				"hostname":    "Tenant Workspace (" + tenant.Username + ")",
				"os":          "Isolated SQLite DB",
				"arch":        "SaaS",
				"ipv4":        "Isolated",
				"ipv6":        "",
				"cpu_percent": 0,
				"ram_percent": 0,
				"uptime":      strconv.Itoa(len(srvs)) + " srv · " + strconv.Itoa(len(tuns)) + " tun",
			},
		})
		return
	}

	// Execute against tenant's completely isolated Handler & SQLite DB
	req := api.WithSaasUser(r, claims.Username)
	inst.mux.ServeHTTP(w, req)
}

func (m *Manager) requireSuperAdmin(r *http.Request) (string, bool) {
	tok := bearerToken(r)
	if tok == "" || strings.HasPrefix(tok, "sa.") {
		return "", false
	}
	u, ok := m.primary.VerifyToken(tok)
	if !ok || strings.HasPrefix(u, "viewer:") {
		return "", false
	}
	return u, true
}

func (m *Manager) activeStoreForRequest(r *http.Request) (*db.Store, string, bool) {
	tok := bearerToken(r)
	if strings.HasPrefix(tok, "sa.") {
		c, t, ok := m.VerifyTenantToken(tok)
		if !ok {
			return nil, "", false
		}
		inst, err := m.getOrOpenTenant(t.ID)
		if err != nil {
			return nil, "", false
		}
		return inst.store, c.Username, true
	}
	u, ok := m.primary.VerifyToken(tok)
	if !ok {
		return nil, "", false
	}
	return m.primary.Store(), u, true
}

func (m *Manager) handleSaaS(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path

	// 1. Session Identity (/api/saas/me)
	if p == "/api/saas/me" && r.Method == http.MethodGet {
		tok := bearerToken(r)
		if strings.HasPrefix(tok, "sa.") {
			c, t, ok := m.VerifyTenantToken(tok)
			if !ok {
				fail(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":              true,
				"role":            "tenant",
				"username":        t.Username,
				"tenant_id":       t.ID,
				"impersonating":   c.ImpersonatedBy != "",
				"impersonated_by": c.ImpersonatedBy,
				"max_servers":     t.MaxServers,
				"max_tunnels":     t.MaxTunnels,
				"max_monitors":    t.MaxMonitors,
				"expires_at":      t.ExpiresAt,
				"db_file":         "tenants/u_" + strconv.FormatInt(t.ID, 10) + "/mcontrol.db",
			})
			return
		}
		u, ok := m.primary.VerifyToken(tok)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		role := "superadmin"
		if strings.HasPrefix(u, "viewer:") {
			role = "viewer"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"role":          role,
			"username":      u,
			"tenant_id":     0,
			"impersonating": false,
			"db_file":       "mcontrol.db",
		})
		return
	}

	// 2. Exit Impersonation (/api/saas/exit-impersonate)
	if p == "/api/saas/exit-impersonate" && r.Method == http.MethodPost {
		tok := bearerToken(r)
		c, _, ok := m.VerifyTenantToken(tok)
		if !ok || c.ImpersonatedBy == "" {
			fail(w, http.StatusForbidden, "not in an impersonated session")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"token": m.primary.MakeToken(c.ImpersonatedBy),
		})
		return
	}

	// 3. Per-User Drag & Drop Card Order (/api/saas/order)
	if p == "/api/saas/order" {
		st, _, ok := m.activeStoreForRequest(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method == http.MethodGet {
			var srvs, tuns []int64
			_ = json.Unmarshal([]byte(st.GetSetting("ui_order_servers")), &srvs)
			_ = json.Unmarshal([]byte(st.GetSetting("ui_order_tunnels")), &tuns)
			if srvs == nil {
				srvs = []int64{}
			}
			if tuns == nil {
				tuns = []int64{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": srvs, "tunnels": tuns})
			return
		}
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			var req struct {
				Kind string  `json:"kind"` // servers | tunnels
				IDs  []int64 `json:"ids"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
				fail(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			raw, _ := json.Marshal(req.IDs)
			if req.Kind == "tunnels" {
				_ = st.SetSetting("ui_order_tunnels", string(raw))
			} else {
				_ = st.SetSetting("ui_order_servers", string(raw))
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}

	// 3b. Per-User Theme Sync & Public Status White-Label (/api/saas/theme)
	if p == "/api/saas/theme" {
		st, _, ok := m.activeStoreForRequest(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method == http.MethodGet {
			raw := strings.TrimSpace(st.GetSetting("ui_theme_json"))
			isDefault := false
			if raw == "" && m.primary != nil && m.primary.Store() != nil {
				raw = strings.TrimSpace(m.primary.Store().GetSetting("saas_default_theme_json"))
				if raw != "" {
					isDefault = true
				}
			}
			var parsed any
			if raw != "" {
				_ = json.Unmarshal([]byte(raw), &parsed)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":         true,
				"theme":      parsed,
				"is_default": isDefault,
			})
			return
		}
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			body, err := io.ReadAll(io.LimitReader(r.Body, 48<<10))
			if err != nil {
				fail(w, http.StatusBadRequest, "invalid theme payload")
				return
			}
			var req struct {
				ThemeID    string         `json:"themeId"`
				Mode       string         `json:"mode"`
				Settings   map[string]any `json:"settings"`
				PublicSync bool           `json:"public_sync"`
				PublicVars map[string]any `json:"public_vars,omitempty"`
			}
			if err := json.Unmarshal(body, &req); err != nil || req.ThemeID == "" {
				fail(w, http.StatusBadRequest, "invalid theme JSON")
				return
			}
			_ = st.SetSetting("ui_theme_json", string(body))
			if req.PublicSync && len(req.PublicVars) > 0 {
				if pb, err := json.Marshal(req.PublicVars); err == nil {
					_ = st.SetSetting("ui_public_theme_json", string(pb))
				}
			} else {
				_ = st.SetSetting("ui_public_theme_json", "")
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}

	// 3c. Per-User White-Label Branding (Name & Logo) (/api/saas/brand)
	if p == "/api/saas/brand" {
		st, _, ok := m.activeStoreForRequest(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method == http.MethodGet {
			var b struct {
				BrandName string `json:"brand_name"`
				BrandLogo string `json:"brand_logo"`
			}
			if raw := strings.TrimSpace(st.GetSetting("ui_brand_json")); raw != "" {
				_ = json.Unmarshal([]byte(raw), &b)
			}
			if b.BrandName == "" {
				b.BrandName = strings.TrimSpace(st.GetSetting("panel_name"))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":         true,
				"brand_name": b.BrandName,
				"brand_logo": b.BrandLogo,
			})
			return
		}
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			var req struct {
				BrandName string `json:"brand_name"`
				BrandLogo string `json:"brand_logo"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 320<<10)).Decode(&req); err != nil {
				fail(w, http.StatusBadRequest, "invalid JSON or logo too large (max 240KB)")
				return
			}
			req.BrandName = strings.TrimSpace(req.BrandName)
			req.BrandLogo = strings.TrimSpace(req.BrandLogo)
			if len(req.BrandName) > 48 {
				fail(w, http.StatusBadRequest, "brand name must be 48 characters or fewer")
				return
			}
			if req.BrandLogo != "" {
				if len(req.BrandLogo) > 260000 {
					fail(w, http.StatusBadRequest, "logo image is too large (max ~190KB)")
					return
				}
				validLogo := strings.HasPrefix(req.BrandLogo, "data:image/png;base64,") ||
					strings.HasPrefix(req.BrandLogo, "data:image/jpeg;base64,") ||
					strings.HasPrefix(req.BrandLogo, "data:image/webp;base64,") ||
					strings.HasPrefix(req.BrandLogo, "data:image/svg+xml;base64,") ||
					strings.HasPrefix(req.BrandLogo, "https://") ||
					strings.HasPrefix(req.BrandLogo, "http://")
				if !validLogo {
					fail(w, http.StatusBadRequest, "unsupported logo format (use PNG, JPG, WEBP, SVG or HTTPS URL)")
					return
				}
			}
			raw, _ := json.Marshal(req)
			_ = st.SetSetting("ui_brand_json", string(raw))
			if req.BrandName != "" {
				_ = st.SetPanelName(req.BrandName)
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "brand_name": req.BrandName, "brand_logo": req.BrandLogo})
			return
		}
	}

	// 4. SuperAdmin-only Tenant Management (/api/saas/tenants...)
	superUser, ok := m.requireSuperAdmin(r)
	if !ok {
		fail(w, http.StatusForbidden, "SuperAdmin access required")
		return
	}

	if p == "/api/saas/default-theme" && (r.Method == http.MethodPut || r.Method == http.MethodPost) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 48<<10))
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid payload")
			return
		}
		var check map[string]any
		if json.Unmarshal(body, &check) != nil {
			fail(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		_ = m.primary.Store().SetSetting("saas_default_theme_json", string(body))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Default theme for new tenants saved!"})
		return
	}

	if p == "/api/saas/tenants" && r.Method == http.MethodGet {
		list, err := m.ListTenants(true)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		totalSrvs, totalTuns, activeTenants := 0, 0, 0
		if ps, err := m.primary.Store().ListServers(); err == nil {
			totalSrvs += len(ps)
		}
		if pt, err := m.primary.Store().ListTunnels(); err == nil {
			totalTuns += len(pt)
		}
		for _, t := range list {
			if t.Active {
				activeTenants++
			}
			totalSrvs += t.ServerCount
			totalTuns += t.TunnelCount
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"tenants":        list,
			"active_tenants": activeTenants,
			"total_servers":  totalSrvs,
			"total_tunnels":  totalTuns,
		})
		return
	}

	if p == "/api/saas/tenants" && r.Method == http.MethodPost {
		var req struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			MaxServers  int    `json:"max_servers"`
			MaxTunnels  int    `json:"max_tunnels"`
			MaxMonitors int    `json:"max_monitors"`
			ExpiresDays int    `json:"expires_days"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		t, err := m.CreateTenant(req.Username, req.Password, req.MaxServers, req.MaxTunnels, req.MaxMonitors, req.ExpiresDays)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tenant": t})
		return
	}

	// /api/saas/tenants/{id} or /api/saas/tenants/{id}/impersonate
	if strings.HasPrefix(p, "/api/saas/tenants/") {
		rest := strings.TrimPrefix(p, "/api/saas/tenants/")
		parts := strings.Split(rest, "/")
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || id <= 0 {
			fail(w, http.StatusBadRequest, "invalid tenant id")
			return
		}
		if len(parts) == 2 && parts[1] == "impersonate" && r.Method == http.MethodPost {
			t, err := m.GetTenantByID(id)
			if err != nil {
				fail(w, http.StatusNotFound, "tenant not found")
				return
			}
			if _, err := m.getOrOpenTenant(t.ID); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			tok := m.IssueTenantToken(t, superUser)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok, "username": t.Username})
			return
		}
		if len(parts) == 1 && r.Method == http.MethodPut {
			var req struct {
				Active      *bool  `json:"active"`
				Password    string `json:"password"`
				MaxServers  *int   `json:"max_servers"`
				MaxTunnels  *int   `json:"max_tunnels"`
				MaxMonitors *int   `json:"max_monitors"`
				ExpiresDays *int   `json:"expires_days"`
			}
			if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
				fail(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if err := m.UpdateTenant(id, req.Active, req.Password, req.MaxServers, req.MaxTunnels, req.MaxMonitors, req.ExpiresDays); err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		if len(parts) == 1 && r.Method == http.MethodDelete {
			if err := m.DeleteTenant(id); err != nil {
				fail(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}

	fail(w, http.StatusNotFound, "unknown saas endpoint")
}
