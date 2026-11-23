package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/storage"
	"golang.org/x/crypto/bcrypt"
)

type ctxKey struct{}

type Claims struct {
	UserID         int64        `json:"uid"`
	Role           storage.Role `json:"role"`
	ImpersonatedBy int64        `json:"imp_by,omitempty"`
	ExpiresAt      int64        `json:"exp"`
}

type Guard struct {
	secret []byte
	engine storage.Engine
}

func NewGuard(secret []byte, engine storage.Engine) *Guard {
	return &Guard{secret: secret, engine: engine}
}

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

func (g *Guard) Sign(c Claims) (string, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, g.secret)
	_, _ = mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig, nil
}

func (g *Guard) Verify(token string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, errors.New("malformed token")
	}
	mac := hmac.New(sha256.New, g.secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
		return c, errors.New("invalid signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	if time.Now().Unix() > c.ExpiresAt {
		return c, errors.New("token expired")
	}
	return c, nil
}

func (g *Guard) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		tok := strings.TrimPrefix(hdr, "Bearer ")
		if tok == "" || tok == hdr {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		claims, err := g.Verify(tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		if g.engine != nil {
			u, err := g.engine.GetUserByID(r.Context(), claims.UserID)
			if err != nil || (!u.Active && claims.ImpersonatedBy == 0) {
				writeErr(w, http.StatusForbidden, "account disabled")
				return
			}
			if u.ExpiresAt != nil && time.Now().After(*u.ExpiresAt) && claims.ImpersonatedBy == 0 {
				writeErr(w, http.StatusForbidden, "account subscription expired")
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, claims)
		next(w, r.WithContext(ctx))
	}
}

func (g *Guard) RequireSuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return g.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		c, ok := ClaimsFromContext(r.Context())
		if !ok || c.Role != storage.RoleSuperAdmin || c.ImpersonatedBy != 0 {
			writeErr(w, http.StatusForbidden, "superadmin access required")
			return
		}
		next(w, r)
	})
}

func ClaimsFromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(Claims)
	return c, ok
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}
