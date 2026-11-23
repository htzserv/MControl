package sqlite

import (
	"context"
	"testing"

	"github.com/htzserv/MControl/internal/storage"
)

func TestTenantPhysicalIsolation(t *testing.T) {
	ctx := context.Background()
	key := []byte("0123456789abcdef0123456789abcdef")
	mgr, err := NewManager(ctx, t.TempDir(), key)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	u1, err := mgr.CreateUser(ctx, &storage.User{Username: "alice", PassHash: "x", Role: storage.RoleTenant, Active: true, MaxServers: 5, MaxTunnels: 5})
	if err != nil {
		t.Fatalf("create u1: %v", err)
	}
	u2, err := mgr.CreateUser(ctx, &storage.User{Username: "bob", PassHash: "x", Role: storage.RoleTenant, Active: true, MaxServers: 5, MaxTunnels: 5})
	if err != nil {
		t.Fatalf("create u2: %v", err)
	}

	if _, err := mgr.AddServer(ctx, u1, &storage.Server{Name: "Alice-DE1", Role: "foreign", Host: "1.1.1.1"}); err != nil {
		t.Fatalf("add server u1: %v", err)
	}

	s1, err := mgr.ListServers(ctx, u1)
	if err != nil || len(s1) != 1 {
		t.Fatalf("expected 1 server for u1, got %d (err=%v)", len(s1), err)
	}
	s2, err := mgr.ListServers(ctx, u2)
	if err != nil || len(s2) != 0 {
		t.Fatalf("ISOLATION FAILURE: expected 0 servers for u2, got %d (err=%v)", len(s2), err)
	}
}
