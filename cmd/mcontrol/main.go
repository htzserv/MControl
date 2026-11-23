package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/httpapi"
	"github.com/htzserv/MControl/internal/storage/sqlite"
	"github.com/htzserv/MControl/internal/version"
	"github.com/htzserv/MControl/internal/webui"
)

func loadOrCreateKey(dataDir string) (encKey, signKey []byte, err error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, nil, err
	}
	keyPath := filepath.Join(dataDir, ".secret.key")
	raw, err := os.ReadFile(keyPath)
	if err != nil || len(raw) < 32 {
		raw = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(keyPath, raw, 0o600); err != nil {
			return nil, nil, err
		}
	}
	h := sha256.Sum256(raw)
	mac := hmac.New(sha256.New, h[:])
	_, _ = mac.Write([]byte("mcontrol-v2-signing"))
	return h[:], mac.Sum(nil), nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func main() {
	port := flag.String("port", "2053", "HTTP listen port")
	dataDir := flag.String("data", "/var/lib/mcontrol", "Data directory for master & tenant SQLite DBs")
	flag.Parse()

	encKey, signKey, err := loadOrCreateKey(*dataDir)
	if err != nil {
		log.Fatalf("init secret key: %v", err)
	}

	ctx := context.Background()
	store, err := sqlite.NewManager(ctx, *dataDir, encKey)
	if err != nil {
		log.Fatalf("init sqlite manager: %v", err)
	}
	defer store.Close()

	guard := auth.NewGuard(signKey, store)
	apiSrv := httpapi.New(store, guard, *dataDir)

	mux := http.NewServeMux()
	apiSrv.Register(mux)

	spa, err := webui.SPAHandler()
	if err != nil {
		log.Fatalf("load embedded SPA: %v", err)
	}
	mux.Handle("/", spa)

	httpSrv := &http.Server{
		Addr:              ":" + *port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		log.Printf("MControl v%s (Multi-Tenant Core) listening on http://0.0.0.0:%s", version.Current, *port)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	log.Println("MControl shut down cleanly.")
}
