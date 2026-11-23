# MControl v2 (v1.0.0) — Multi-Tenant Infrastructure Panel

Built with **Go 1.23 + React 18 (TypeScript + Vite) + MDesign + Isolated Per-Tenant SQLite (WAL)** and an abstracted `storage` interface ready for future **PostgreSQL + TimescaleDB** support.

## Features in v1.0.0 Core
- **True Database-per-Tenant Isolation:** Central `master.db` for SuperAdmin + physically isolated `tenants/u_<id>.db` per customer with WAL mode and per-plan quotas.
- **Commercial SuperAdmin Control Plane:** Create, edit, suspend, and delete tenant accounts, configure server/tunnel/monitor quotas and expiry dates, 1-click **Impersonate**, and **White-Label Branding**.
- **React + TypeScript + MDesign UI:** Embedded directly inside the single static Go binary (`//go:embed`) — zero Node.js required on production servers.
- **Real Drag & Drop:** Reorder Server cards (`@dnd-kit`) and connect/arrange Entry ⇄ Exit nodes on an interactive Topology Canvas (`@xyflow/react`) with persistent layout saving.
- **Zero-Broken-Build CI:** GitHub Actions runs `tsc --noEmit`, `vite build`, `go vet`, and `go test -race` before publishing static `amd64`/`arm64` binaries.

## Quick Local Development
```bash
# 1. Build React UI into internal/webui/dist
cd frontend && npm install && npm run build && cd ..

# 2. Run Go tests & start panel
go mod tidy
go test -race ./...
go run ./cmd/mcontrol -port 2053 -data ./dev-data
```
