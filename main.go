package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/htzserv/MControl/internal/api"
	"github.com/htzserv/MControl/internal/db"
	"github.com/htzserv/MControl/internal/saas"
	"github.com/htzserv/MControl/internal/version"
)

//go:embed web/*
var webFS embed.FS

const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if strings.HasSuffix(r.URL.Path, "/sw.js") || r.URL.Path == "/sw.js" {
			h.Set("Cache-Control", "no-cache")
		} else if strings.HasSuffix(r.URL.Path, ".png") || strings.HasSuffix(r.URL.Path, "manifest.json") {
			h.Set("Cache-Control", "public, max-age=86400")
		} else {
			h.Set("Cache-Control", "no-store")
		}
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

func main() {
	defaultPort := flag.String("port", "2053", "Initial HTTP listen port (overridden by saved setup port)")
	dataDir := flag.String("data", "/var/lib/mcontrol", "Data directory for SQLite & encryption key")
	flag.Parse()

	store, err := db.Open(*dataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer store.Close()

	setupDone, webPath, activePort := store.GetPanelConfig(*defaultPort)
	listenDomain, strictDomain := store.GetDomainConfig()

	innerMux := http.NewServeMux()
	apiHandler := api.New(store, activePort)
	apiHandler.Register(innerMux)
	apiHandler.StartAutoProbe()
	apiHandler.StartTelegramBot()
	apiHandler.StartCloudflareAutoSync()
	apiHandler.StartTunnelWatchdog()
	apiHandler.StartOpsDaemon()
	apiHandler.StartMonitorDaemon()
	apiHandler.StartProMetricsDaemon()

	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("failed to load embedded UI: %v", err)
	}
	innerMux.Handle("/", http.FileServer(http.FS(subFS)))

	saasMgr, err := saas.NewManager(*dataDir, activePort, apiHandler)
	if err != nil {
		log.Fatalf("failed to initialize multi-tenant SaaS engine: %v", err)
	}
	defer saasMgr.Close()

	var rootHandler http.Handler = saasMgr.Wrap(innerMux)
	if setupDone && webPath != "" {
		prefix := "/" + webPath
		rootHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == prefix {
				http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
				return
			}
			if !strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.NotFound(w, r)
				return
			}
			http.StripPrefix(prefix, innerMux).ServeHTTP(w, r)
		})
	}

	// Optional HTTPS: drop tls.crt + tls.key into the data directory and restart.
	certPath := filepath.Join(*dataDir, "tls.crt")
	keyPath := filepath.Join(*dataDir, "tls.key")
	useTLS := false
	if _, err1 := os.Stat(certPath); err1 == nil {
		if _, err2 := os.Stat(keyPath); err2 == nil {
			useTLS = true
		}
	}
	scheme := "http"
	if useTLS {
		scheme = "https"
	}

	if setupDone && listenDomain != "" && strictDomain {
		prev := rootHandler
		rootHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := strings.ToLower(r.Host)
			if i := strings.LastIndex(host, ":"); i > 0 && !strings.HasSuffix(host, "]") {
				host = host[:i]
			}
			if host != listenDomain {
				http.NotFound(w, r)
				return
			}
			prev.ServeHTTP(w, r)
		})
	}

	finalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/_mc_dist/") {
			saasMgr.ServeDist(w, r)
			return
		}
		if r.URL.Path == "/status" || strings.HasPrefix(r.URL.Path, "/status/") || r.URL.Path == "/api/public/status" || strings.HasPrefix(r.URL.Path, "/api/public/status/") {
			saasMgr.ServePublicStatus(w, r)
			return
		}
		rootHandler.ServeHTTP(w, r)
	})

	addr := ":" + activePort
	if setupDone && webPath != "" {
		log.Printf("MControl v%s listening on %s://0.0.0.0%s/%s/", version.Current, scheme, addr, webPath)
	} else {
		log.Printf("MControl v%s (First-Time Setup) listening on %s://0.0.0.0%s/", version.Current, scheme, addr)
	}

	// Confirm healthy startup after 5s and clear pending auto-rollback marker
	time.AfterFunc(5*time.Second, func() {
		_ = os.Remove(filepath.Join(*dataDir, ".update_pending"))
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(finalHandler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Minute, // large update uploads
		IdleTimeout:       2 * time.Minute,
	}
	if useTLS {
		err = srv.ListenAndServeTLS(certPath, keyPath)
	} else {
		log.Printf("WARNING: serving plain HTTP. Put tls.crt/tls.key in %s (or use a reverse proxy) to protect passwords in transit.", *dataDir)
		err = srv.ListenAndServe()
	}
	log.Fatalf("server stopped: %v", err)
}
