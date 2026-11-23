# MControl — Central Server, Tunnel & DNS Management Panel (MDesign)

[![Release](https://img.shields.io/github/v/release/htzserv/MControl?style=flat-square&color=6366f1)](https://github.com/htzserv/MControl/releases)
[![Architecture](https://img.shields.io/badge/architecture-Go%20Single%20Binary-22d3ee?style=flat-square)](#)

[🇮🇷 نسخه فارسی (Persian README)](README.fa.md)

**MControl** is a standalone, single-binary Go management panel for multi-node infrastructure (Iran & Foreign servers). Engineered with the modular **MDesign** theme engine, it unifies **2-Sided Tunnel Orchestration**, **MTunnel Pro Migration**, **Cloudflare & ArvanCloud DNS Management**, **Master Outbound (Xray Core)**, **Peer Agent v2 (TLS)**, and a **Native Telegram Bot & Auto-Backuper** into a single lightweight binary with an embedded UI and SQLite database.

---

## Quick Install

### Stable Release
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash
```

With custom port (default `2053`):
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- 2053
```

### Rolling Beta Channel
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- --beta
```

---

## Core Features

### 1. Unified 2-Sided Tunnel Orchestrator & Auto-Watchdog
- **Supported Engines:** `Backhaul`, `Rathole`, `Paqet` (native YAML + automatic `raw NOTRACK` / `mangle RST DROP` iptables rules), `GRE` (`4-to-4`, `6-to-4`, `6-to-6`), and `VXLAN`.
- **Stability First:** NAT-friendly keepalives & heartbeats, `StartLimitIntervalSec=0` systemd units, `rp_filter=0` + bidirectional `TCPMSS` clamping, and a built-in **45-second Tunnel Watchdog** that auto-heals dropped services on both nodes.
- **Kernel VIP Load Balancing:** `Round-Robin`, `Random`, `Lowest Ping`, `Lightest Load`, and `Least Traffic`.
- **MSmart Benchmark & 8-Ping Tester:** Compare all protocols between any two nodes in one click, run in-tunnel Speedtests, and review saved benchmark history.

### 2. MTunnel → MControl Pro Migrator
- **Zero-Data-Loss Pipeline:** Automatically detects legacy MTunnel installations when adding a server, saves an AES-GCM encrypted verbatim snapshot (`.json`), installs & verifies MControl engines, and pairs both sides automatically or manually.
- **Live Kernel Tunnel Control:** Preserves existing MTunnel `GRE` / `VXLAN` tunnels (`mgre.service` / `mxlan.service`) without downtime and provides live **`Status / Restart / Stop / Logs`** controls directly in the **Tunnels** tab.

### 3. Dual-Provider Cloud DNS Manager (Cloudflare & ArvanCloud)
- Manage **Cloudflare** and **ArvanCloud** zones side-by-side with independent auto-sync schedules.
- **Auto Server IP Matching:** Automatically links `A`/`AAAA` records to your managed servers.
- **Instant Parallel Bulk Actions:** Select multiple records with checkboxes for parallel bulk **Proxy ON/OFF** and **Bulk Delete**.

### 4. Master Outbound (Xray Core)
- Runs an isolated local SOCKS5 proxy (`127.0.0.1:10808`) strictly for the Master Panel (GitHub updates, Telegram bot, Cloud DNS, and Foreign node SSH/probing).
- Supports Subscription sync, Xray Observatory Load-Balancing, Auto-Failover, and Real Delay testing.

### 5. Native Telegram Bot & Auto-Backuper
- Full inline-keyboard tunnel creation & control, scheduled Auto-Backuper (6/12/24/48h), Pre-Update backups, Send-to-Restore `.zip` backups, actionable outage alerts (with Restart/Logs buttons), and Daily Digest.

### 6. Native Uptime Monitor (Kuma-Style), Dedicated Alert Bot & Public `/status` Page
- **Multi-Point Probes:** Monitor `HTTP/HTTPS` (+ SSL expiry), `TCP`, `Ping (v4/v6)`, and `DNS` directly from **Master**, **Master Outbound**, or any managed **Iran/Foreign server** with 30-tick heartbeat bars, Auto-Heal tunnel restarts, a **Dedicated Telegram Notification Bot** (with Forum Topic ID support), and a login-free **Public Status Page (`/status`)**.

### 7. Fleet Pro Operations & Disaster Recovery
- **1-Click Replace Server:** Migrate all linked tunnels and matching Cloudflare/ArvanCloud DNS records to a replacement server.
- **Live Topology Map & v4/v6 Reachability Matrix:** Visual tunnel map and cross-node IPv4/IPv6 reachability matrix.
- **Web Terminal + File Manager + Script Vault + Firewall/Services + Scheduler + 3x-ui/Marzban Port Sync + Auto SSL Sync + MikroTik GRE/IP6GRE Generator + 24h/7d Metrics History + Viewer RBAC + PWA + `Ctrl+K` Command Palette.**

### 8. Modular MDesign Theme Engine
- **11 Independent Customization Axes** in Theme Studio: Navigation (`Dock`, `Floating Sidebar`, `Sidebar`, `Icon Rail`, `Bottom`), Card Surface (`Glass`, `Prism Bubbles`, `Liquid Glass`, `Neumorphic`, `Brutalist`, etc.), Master Cards (`Unified` vs `Separate Bubbles`), Metric Graphs (`Wave Sparkline`, `Circular Ring`, `Classic Line`), Checkboxes (`Rounded`, `Square`, `Circle`, `Neon Glow`, `Material`, `Mini Switch`), Fonts, Density, and Background effects.
- **Master Toolbox & Symmetric Top Bar:** Floating **Version/Update** bubble (with Rolling Beta toggle & Offline ZIP installer), floating **Backup/Restore** bubble, and an Overview **Toolbox** card (`Speedtest`, `Tweak`, `Clean RAM`, `MSmart`, `Restart Panel`).

---

## CLI Manager
Run `mcontrol` on the Master server for service control, port/web-path updates, emergency 2FA/HTTPS reset, and live logs.

## Automatic Releases (GitHub Actions)
- **Official Release:** Bump `VERSION` (e.g., `1.2.3`) and push to `main` — GitHub Actions compiles static `amd64`/`arm64` binaries + `.sha256` checksums and publishes an official Release.
- **Rolling Beta:** Push to `main` without changing `VERSION` to automatically refresh the `beta-latest` pre-release.
