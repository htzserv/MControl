package saas

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/htzserv/MControl/internal/api"
	"github.com/htzserv/MControl/internal/db"
)

func TestTenantDatabaseIsolation(t *testing.T) {
	dir := t.TempDir()
	primaryStore, err := db.Open(dir)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer primaryStore.Close()
	if err := primaryStore.CompleteSetup("superadmin", "SuperPass123!", "mc_test", 2053); err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	primaryHandler := api.New(primaryStore, "2053")
	defer primaryHandler.Stop()

	mgr, err := NewManager(dir, "2053", primaryHandler)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	t1, err := mgr.CreateTenant("alice", "AlicePass123!", 5, 10, 10, 30)
	if err != nil {
		t.Fatalf("CreateTenant alice: %v", err)
	}
	t2, err := mgr.CreateTenant("bob", "BobPass123!", 5, 10, 10, 0)
	if err != nil {
		t.Fatalf("CreateTenant bob: %v", err)
	}

	// Verify separate SQLite DB files exist
	for _, id := range []int64{t1.ID, t2.ID} {
		dbFile := filepath.Join(dir, "tenants", fmt.Sprintf("u_%d", id), "mcontrol.db")
		if _, err := os.Stat(dbFile); err != nil {
			t.Fatalf("expected isolated DB at %s: %v", dbFile, err)
		}
	}

	// Add a server to Alice and ensure Bob cannot see it
	inst1, err := mgr.getOrOpenTenant(t1.ID)
	if err != nil {
		t.Fatalf("open alice: %v", err)
	}
	inst2, err := mgr.getOrOpenTenant(t2.ID)
	if err != nil {
		t.Fatalf("open bob: %v", err)
	}
	if _, err := inst1.store.AddServer(&db.Server{Name: "Alice-DE", Role: "foreign", Host: "1.1.1.1", Port: 22, Username: "root", Password: "secret"}); err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	s1, _ := inst1.store.ListServers()
	s2, _ := inst2.store.ListServers()
	if len(s1) != 1 || len(s2) != 0 {
		t.Fatalf("isolation violated: alice=%d bob=%d", len(s1), len(s2))
	}

	// Verify tenant token & impersonation token
	tok := mgr.IssueTenantToken(t1, "superadmin")
	claims, got, ok := mgr.VerifyTenantToken(tok)
	if !ok || got.Username != "alice" || claims.ImpersonatedBy != "superadmin" {
		t.Fatalf("impersonation token invalid")
	}

	// Delete Alice and verify DB folder wiped
	if err := mgr.DeleteTenant(t1.ID); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	if _, err := os.Stat(mgr.tenantDir(t1.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected deleted tenant folder to be removed")
	}
}
