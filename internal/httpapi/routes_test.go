package httpapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/htzserv/MControl/internal/auth"
	"github.com/htzserv/MControl/internal/storage/sqlite"
)

func TestRegisterRoutesNoConflict(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	mgr, err := sqlite.NewManager(context.Background(), t.TempDir(), key)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	guard := auth.NewGuard(key, mgr)
	srv := New(mgr, guard, t.TempDir())
	mux := http.NewServeMux()
	srv.Register(mux)
}
