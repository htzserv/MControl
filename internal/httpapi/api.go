package httpapi

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/storage"
	"github.com/htzserv/MControl/internal/version"
)

type Server struct {
	engine  storage.Engine
	guard   *auth.Guard
	dataDir string
}

func New(engine storage.Engine, guard *auth.Guard, dataDir string) *Server {
	return &Server{engine: engine, guard: guard, dataDir: dataDir}
}

func (s *Server) Register(mux *http.ServeMux) {
	// Public Setup & Auth
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/setup/init", s.handleSetupInit)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	// Authenticated Session
	mux.HandleFunc("GET /api/auth/me", s.guard.RequireAuth(s.handleMe))
	mux.HandleFunc("POST /api/auth/exit-impersonate", s.guard.RequireAuth(s.handleExitImpersonate))

	// SuperAdmin Commercial Control Plane
	mux.HandleFunc("GET /api/admin/overview", s.guard.RequireSuperAdmin(s.handleAdminOverview))
	mux.HandleFunc("GET /api/admin/users", s.guard.RequireSuperAdmin(s.handleAdminListUsers))
	mux.HandleFunc("POST /api/admin/users", s.guard.RequireSuperAdmin(s.handleAdminCreateUser))
	mux.HandleFunc("PUT /api/admin/users/{id}", s.guard.RequireSuperAdmin(s.handleAdminUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.guard.RequireSuperAdmin(s.handleAdminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/impersonate", s.guard.RequireSuperAdmin(s.handleAdminImpersonate))
	mux.HandleFunc("GET /api/admin/branding", s.guard.RequireSuperAdmin(s.handleAdminGetBranding))
	mux.HandleFunc("PUT /api/admin/branding", s.guard.RequireSuperAdmin(s.handleAdminSaveBranding))

	// Per-Tenant Isolated Workspace
	mux.HandleFunc("GET /api/tenant/servers", s.guard.RequireAuth(s.handleTenantListServers))
	mux.HandleFunc("POST /api/tenant/servers", s.guard.RequireAuth(s.handleTenantAddServer))
	mux.HandleFunc("DELETE /api/tenant/servers/{id}", s.guard.RequireAuth(s.handleTenantDeleteServer))

	mux.HandleFunc("GET /api/tenant/tunnels", s.guard.RequireAuth(s.handleTenantListTunnels))
	mux.HandleFunc("POST /api/tenant/tunnels", s.guard.RequireAuth(s.handleTenantAddTunnel))
	mux.HandleFunc("DELETE /api/tenant/tunnels/{id}", s.guard.RequireAuth(s.handleTenantDeleteTunnel))

	mux.HandleFunc("PUT /api/tenant/layout", s.guard.RequireAuth(s.handleTenantSaveLayout))
	mux.HandleFunc("GET /api/tenant/theme", s.guard.RequireAuth(s.handleTenantGetTheme))
	mux.HandleFunc("PUT /api/tenant/theme", s.guard.RequireAuth(s.handleTenantSaveTheme))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON payload")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// ── Setup & Auth Handlers ────────────────────────────────────────────────────

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	inited, err := s.engine.IsInitialized(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	brand, _ := s.engine.GetBranding(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"initialized": inited,
		"version":     version.Current,
		"branding":    brand,
	})
}

func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	inited, err := s.engine.IsInitialized(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if inited {
		fail(w, http.StatusConflict, "panel is already initialized")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		AppName  string `json:"app_name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Password) < 6 {
		fail(w, http.StatusBadRequest, "username (min 3 chars) and password (min 6 chars) are required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	uid, err := s.engine.CreateUser(r.Context(), &storage.User{
		Username:    req.Username,
		PassHash:    hash,
		Role:        storage.RoleSuperAdmin,
		Active:      true,
		MaxServers:  999,
		MaxTunnels:  999,
		MaxMonitors: 999,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if strings.TrimSpace(req.AppName) != "" {
		_ = s.engine.SaveBranding(r.Context(), storage.Branding{
			AppName:  strings.TrimSpace(req.AppName),
			Subtitle: "Multi-Tenant Infrastructure Cloud",
			Accent:   "#6366f1",
		})
	}
	migrated, _ := s.engine.MigrateLegacyV1(filepath.Join(s.dataDir, "mcontrol.db"), uid)

	tok, err := s.guard.Sign(auth.Claims{
		UserID:    uid,
		Role:      storage.RoleSuperAdmin,
		ExpiresAt: time.Now().Add(72 * time.Hour).Unix(),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"token":              tok,
		"legacy_v1_migrated": migrated,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.engine.GetUserByUsername(r.Context(), req.Username)
	if err != nil || !auth.CheckPassword(u.PassHash, req.Password) {
		fail(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if !u.Active {
		fail(w, http.StatusForbidden, "this account is suspended")
		return
	}
	if u.ExpiresAt != nil && time.Now().After(*u.ExpiresAt) {
		fail(w, http.StatusForbidden, "this account has expired")
		return
	}
	tok, err := s.guard.Sign(auth.Claims{
		UserID:    u.ID,
		Role:      u.Role,
		ExpiresAt: time.Now().Add(72 * time.Hour).Unix(),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok, "user": u})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	u, err := s.engine.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusUnauthorized, "user not found")
		return
	}
	brand, _ := s.engine.GetBranding(r.Context())
	theme, _ := s.engine.GetThemePrefs(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"user":            u,
		"impersonated_by": claims.ImpersonatedBy,
		"version":         version.Current,
		"branding":        brand,
		"theme":           theme,
	})
}

func (s *Server) handleExitImpersonate(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	if claims.ImpersonatedBy == 0 {
		fail(w, http.StatusBadRequest, "not currently impersonating")
		return
	}
	admin, err := s.engine.GetUserByID(r.Context(), claims.ImpersonatedBy)
	if err != nil || admin.Role != storage.RoleSuperAdmin {
		fail(w, http.StatusForbidden, "original superadmin not found")
		return
	}
	tok, err := s.guard.Sign(auth.Claims{
		UserID:    admin.ID,
		Role:      storage.RoleSuperAdmin,
		ExpiresAt: time.Now().Add(72 * time.Hour).Unix(),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok})
}

// ── SuperAdmin Control Plane ─────────────────────────────────────────────────

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	st, err := s.engine.GetSystemStats(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": st})
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.engine.ListUsers(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users": users})
}

func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		MaxServers  int    `json:"max_servers"`
		MaxTunnels  int    `json:"max_tunnels"`
		MaxMonitors int    `json:"max_monitors"`
		ExpiresDays int    `json:"expires_days"` // 0 = unlimited
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Password) < 6 {
		fail(w, http.StatusBadRequest, "username (min 3) and password (min 6) required")
		return
	}
	if req.MaxServers <= 0 {
		req.MaxServers = 10
	}
	if req.MaxTunnels <= 0 {
		req.MaxTunnels = 20
	}
	if req.MaxMonitors <= 0 {
		req.MaxMonitors = 20
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	u := &storage.User{
		Username:    req.Username,
		PassHash:    hash,
		Role:        storage.RoleTenant,
		Active:      true,
		MaxServers:  req.MaxServers,
		MaxTunnels:  req.MaxTunnels,
		MaxMonitors: req.MaxMonitors,
	}
	if req.ExpiresDays > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresDays) * 24 * time.Hour)
		u.ExpiresAt = &t
	}
	id, err := s.engine.CreateUser(r.Context(), u)
	if err != nil {
		fail(w, http.StatusConflict, "username already exists or DB error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "message": "Tenant created with isolated SQLite DB!"})
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := s.engine.GetUserByID(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, "user not found")
		return
	}
	var req struct {
		Active      *bool  `json:"active"`
		MaxServers  *int   `json:"max_servers"`
		MaxTunnels  *int   `json:"max_tunnels"`
		MaxMonitors *int   `json:"max_monitors"`
		NewPassword string `json:"new_password"`
		ExpiresDays *int   `json:"expires_days"` // -1 = clear expiry, >0 = set days from now
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Active != nil {
		cur.Active = *req.Active
	}
	if req.MaxServers != nil && *req.MaxServers > 0 {
		cur.MaxServers = *req.MaxServers
	}
	if req.MaxTunnels != nil && *req.MaxTunnels > 0 {
		cur.MaxTunnels = *req.MaxTunnels
	}
	if req.MaxMonitors != nil && *req.MaxMonitors > 0 {
		cur.MaxMonitors = *req.MaxMonitors
	}
	if req.ExpiresDays != nil {
		if *req.ExpiresDays < 0 {
			cur.ExpiresAt = nil
		} else if *req.ExpiresDays > 0 {
			t := time.Now().Add(time.Duration(*req.ExpiresDays) * 24 * time.Hour)
			cur.ExpiresAt = &t
		}
	}
	newHash := ""
	if strings.TrimSpace(req.NewPassword) != "" {
		if len(req.NewPassword) < 6 {
			fail(w, http.StatusBadRequest, "new password must be at least 6 chars")
			return
		}
		h, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		newHash = h
	}
	if err := s.engine.UpdateUser(r.Context(), cur, newHash); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	if id == claims.UserID {
		fail(w, http.StatusBadRequest, "cannot delete your own superadmin account")
		return
	}
	if err := s.engine.DeleteUser(r.Context(), id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminImpersonate(w http.ResponseWriter, r *http.Request) {
	targetID, ok := pathID(w, r)
	if !ok {
		return
	}
	claims, _ := auth.ClaimsFromContext(r.Context())
	target, err := s.engine.GetUserByID(r.Context(), targetID)
	if err != nil {
		fail(w, http.StatusNotFound, "tenant not found")
		return
	}
	tok, err := s.guard.Sign(auth.Claims{
		UserID:         target.ID,
		Role:           target.Role,
		ImpersonatedBy: claims.UserID,
		ExpiresAt:      time.Now().Add(2 * time.Hour).Unix(),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok, "username": target.Username})
}

func (s *Server) handleAdminGetBranding(w http.ResponseWriter, r *http.Request) {
	b, _ := s.engine.GetBranding(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "branding": b})
}

func (s *Server) handleAdminSaveBranding(w http.ResponseWriter, r *http.Request) {
	var b storage.Branding
	if !readJSON(w, r, &b) {
		return
	}
	if err := s.engine.SaveBranding(r.Context(), b); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "branding": b})
}

// ── Per-Tenant Isolated Workspace Handlers ───────────────────────────────────

func (s *Server) handleTenantListServers(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	list, err := s.engine.ListServers(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "servers": list})
}

func (s *Server) handleTenantAddServer(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	u, err := s.engine.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusUnauthorized, "user not found")
		return
	}
	cur, _ := s.engine.ListServers(r.Context(), claims.UserID)
	if len(cur) >= u.MaxServers {
		fail(w, http.StatusForbidden, "server quota limit reached for your plan")
		return
	}
	var req storage.Server
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Host) == "" {
		fail(w, http.StatusBadRequest, "server name and host IP are required")
		return
	}
	if req.Role != "iran" && req.Role != "foreign" {
		req.Role = "foreign"
	}
	id, err := s.engine.AddServer(r.Context(), claims.UserID, &req)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleTenantDeleteServer(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.engine.DeleteServer(r.Context(), claims.UserID, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTenantListTunnels(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	list, err := s.engine.ListTunnels(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tunnels": list})
}

func (s *Server) handleTenantAddTunnel(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	u, err := s.engine.GetUserByID(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusUnauthorized, "user not found")
		return
	}
	cur, _ := s.engine.ListTunnels(r.Context(), claims.UserID)
	if len(cur) >= u.MaxTunnels {
		fail(w, http.StatusForbidden, "tunnel quota limit reached for your plan")
		return
	}
	var req storage.Tunnel
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || req.IranServerID <= 0 || req.ForeignServerID <= 0 || req.IranServerID == req.ForeignServerID {
		fail(w, http.StatusBadRequest, "tunnel name and two distinct servers are required")
		return
	}
	id, err := s.engine.AddTunnel(r.Context(), claims.UserID, &req)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleTenantDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.engine.DeleteTunnel(r.Context(), claims.UserID, id); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTenantSaveLayout(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var req struct {
		Items []storage.LayoutItem `json:"items"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.engine.SaveLayout(r.Context(), claims.UserID, req.Items); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTenantGetTheme(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	p, err := s.engine.GetThemePrefs(r.Context(), claims.UserID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "theme": p})
}

func (s *Server) handleTenantSaveTheme(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFromContext(r.Context())
	var p storage.ThemePrefs
	if !readJSON(w, r, &p) {
		return
	}
	if err := s.engine.SaveThemePrefs(r.Context(), claims.UserID, p); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "theme": p})
}
